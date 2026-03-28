package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/0funct0ry/coxec/internal/engine"
	"github.com/redis/go-redis/v9"
)

// RedisJobStore implements JobStore using a Redis backend.
type RedisJobStore struct {
	client *redis.Client
	ctx    context.Context
}

const (
	redisKeyPrefix           = "coxec:"
	redisKeyJobs             = redisKeyPrefix + "jobs"              // ZSet: job_id -> createdAt (nano)
	redisKeyJobPrefix        = redisKeyPrefix + "job:"             // Hash: job details
	redisKeyTerminalJobs     = redisKeyPrefix + "terminal_jobs"    // ZSet: job_id -> completedAt or createdAt (nano)
	redisKeyIdempotencyPrefix = redisKeyPrefix + "idempotency:"    // String: mapping to job_id
)

// NewRedisJobStore creates a new RedisJobStore.
func NewRedisJobStore(dsn string) (*RedisJobStore, error) {
	opts, err := redis.ParseURL(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis DSN: %w", err)
	}

	client := redis.NewClient(opts)
	ctx := context.Background()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &RedisJobStore{
		client: client,
		ctx:    ctx,
	}, nil
}

func (s *RedisJobStore) jobKey(id string) string {
	return redisKeyJobPrefix + id
}

func (s *RedisJobStore) idempotencyKey(key string) string {
	return redisKeyIdempotencyPrefix + key
}

func (s *RedisJobStore) Create(job *Job) error {
	jobKey := s.jobKey(job.ID)
	
	data, err := s.marshalJob(job)
	if err != nil {
		return err
	}

	pipe := s.client.Pipeline()
	pipe.HSet(s.ctx, jobKey, data)
	pipe.ZAdd(s.ctx, redisKeyJobs, redis.Z{
		Score:  float64(job.CreatedAt.UnixNano()),
		Member: job.ID,
	})
	
	_, err = pipe.Exec(s.ctx)
	if err != nil {
		return fmt.Errorf("failed to create job in redis: %w", err)
	}
	return nil
}

func (s *RedisJobStore) Get(id string) (*Job, bool) {
	jobKey := s.jobKey(id)
	
	vals, err := s.client.HGetAll(s.ctx, jobKey).Result()
	if err != nil || len(vals) == 0 {
		return nil, false
	}

	job, err := s.unmarshalJob(id, vals)
	if err != nil {
		return nil, false
	}

	return job, true
}

func (s *RedisJobStore) Update(job *Job) error {
	jobKey := s.jobKey(job.ID)
	
	data, err := s.marshalJob(job)
	if err != nil {
		return err
	}

	pipe := s.client.Pipeline()
	pipe.HSet(s.ctx, jobKey, data)
	
	// If the job is terminal, add to terminal jobs index for pruning
	if job.Status == JobStatusCompleted || job.Status == JobStatusFailed || job.Status == JobStatusCancelled {
		score := job.CreatedAt.UnixNano()
		if job.CompletedAt != nil {
			score = job.CompletedAt.UnixNano()
		}
		pipe.ZAdd(s.ctx, redisKeyTerminalJobs, redis.Z{
			Score:  float64(score),
			Member: job.ID,
		})
	} else {
		// Ensure it's not in terminal jobs if status changed back (unlikely but safe)
		pipe.ZRem(s.ctx, redisKeyTerminalJobs, job.ID)
	}

	_, err = pipe.Exec(s.ctx)
	if err != nil {
		return fmt.Errorf("failed to update job in redis: %w", err)
	}
	return nil
}

func (s *RedisJobStore) Delete(id string) error {
	pipe := s.client.Pipeline()
	pipe.Del(s.ctx, s.jobKey(id))
	pipe.ZRem(s.ctx, redisKeyJobs, id)
	pipe.ZRem(s.ctx, redisKeyTerminalJobs, id)
	
	_, err := pipe.Exec(s.ctx)
	if err != nil {
		return fmt.Errorf("failed to delete job in redis: %w", err)
	}
	return nil
}

func (s *RedisJobStore) List(filter ListFilter) ([]*Job, int, error) {
	// 1. Get total matching jobs (respecting TTL if set)
	var jobIDs []string
	var err error

	// We use ZREVRANGE to get newest jobs first
	// If TTL is set, we need to filter by score in the ZSet
	if filter.TTL > 0 {
		// Active jobs are always kept. Terminal jobs are pruned.
		// So we can just list from redisKeyJobs and filter in code or use another index.
		// Let's keep it simple: fetch more and filter.
	}

	// Fetch all job IDs sorted by CreatedAt descending
	jobIDs, err = s.client.ZRevRange(s.ctx, redisKeyJobs, 0, -1).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch job IDs: %w", err)
	}

	total := 0
	matchedJobs := make([]*Job, 0)
	now := time.Now()

	for _, id := range jobIDs {
		job, ok := s.Get(id)
		if !ok {
			continue // Should not happen if indices are synced
		}

		// TTL Filter for terminal jobs
		if filter.TTL > 0 && (job.Status == JobStatusCompleted || job.Status == JobStatusFailed || job.Status == JobStatusCancelled) {
			var age time.Duration
			if job.CompletedAt != nil {
				age = now.Sub(*job.CompletedAt)
			} else {
				age = now.Sub(job.CreatedAt)
			}
			if age > filter.TTL {
				continue // Skip expired
			}
		}

		matchedJobs = append(matchedJobs, job)
	}

	total = len(matchedJobs)

	// Apply pagination
	start := filter.Offset
	if start >= total {
		return []*Job{}, total, nil
	}
	
	end := total
	if filter.Limit > 0 && start+filter.Limit < total {
		end = start + filter.Limit
	}

	return matchedJobs[start:end], total, nil
}

func (s *RedisJobStore) Prune(limit int, ttl time.Duration) (int, error) {
	now := time.Now()
	totalPruned := 0

	// 1. TTL Pruning
	if ttl > 0 {
		cutoff := now.Add(-ttl).UnixNano()
		// Find expired terminal jobs
		toPrune, err := s.client.ZRangeByScore(s.ctx, redisKeyTerminalJobs, &redis.ZRangeBy{
			Min: "-inf",
			Max: fmt.Sprintf("%d", cutoff),
		}).Result()
		
		if err == nil && len(toPrune) > 0 {
			for _, id := range toPrune {
				err := s.Delete(id) // Deletes from all indices and hash
				if err == nil {
					totalPruned++
				}
			}
		}
	}

	// 2. History Limit Pruning
	if limit > 0 {
		count, err := s.client.ZCard(s.ctx, redisKeyTerminalJobs).Result()
		if err == nil && int(count) > limit {
			toDelete := int(count) - limit
			// Delete oldest terminal jobs (lowest scores)
			toPrune, err := s.client.ZRange(s.ctx, redisKeyTerminalJobs, 0, int64(toDelete-1)).Result()
			if err == nil && len(toPrune) > 0 {
				for _, id := range toPrune {
					err := s.Delete(id)
					if err == nil {
						totalPruned++
					}
				}
			}
		}
	}

	return totalPruned, nil
}

func (s *RedisJobStore) GetByIdempotencyKey(key string) (string, bool) {
	id, err := s.client.Get(s.ctx, s.idempotencyKey(key)).Result()
	if err != nil {
		return "", false
	}
	return id, true
}

func (s *RedisJobStore) SetIdempotencyKey(key string, jobID string) {
	// Set with a reasonable expiration (e.g., 24h) to avoid leaking memory, 
	// though coxec cleanup loop will eventually remove jobs.
	s.client.Set(s.ctx, s.idempotencyKey(key), jobID, 24*time.Hour)
}

func (s *RedisJobStore) marshalJob(job *Job) (map[string]interface{}, error) {
	reqJSON, err := json.Marshal(job.Request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	data := map[string]interface{}{
		"status":     string(job.Status),
		"request":    string(reqJSON),
		"created_at": job.CreatedAt.Format(time.RFC3339Nano),
		"error":      job.Error,
	}

	if job.StartedAt != nil {
		data["started_at"] = job.StartedAt.Format(time.RFC3339Nano)
	}
	if job.CompletedAt != nil {
		data["completed_at"] = job.CompletedAt.Format(time.RFC3339Nano)
	}
	if job.Report != nil {
		reportJSON, err := json.Marshal(job.Report)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal report: %w", err)
		}
		data["report"] = string(reportJSON)
	}

	return data, nil
}

func (s *RedisJobStore) unmarshalJob(id string, data map[string]string) (*Job, error) {
	job := &Job{ID: id}
	
	job.Status = JobStatus(data["status"])
	job.Error = data["error"]
	
	if val, ok := data["created_at"]; ok {
		t, _ := time.Parse(time.RFC3339Nano, val)
		job.CreatedAt = t
	}
	
	if val, ok := data["started_at"]; ok {
		t, _ := time.Parse(time.RFC3339Nano, val)
		job.StartedAt = &t
	}
	
	if val, ok := data["completed_at"]; ok {
		t, _ := time.Parse(time.RFC3339Nano, val)
		job.CompletedAt = &t
	}
	
	if val, ok := data["request"]; ok {
		if err := json.Unmarshal([]byte(val), &job.Request); err != nil {
			return nil, fmt.Errorf("failed to unmarshal request: %w", err)
		}
	}
	
	if val, ok := data["report"]; ok {
		var report engine.ExecutionReport
		if err := json.Unmarshal([]byte(val), &report); err != nil {
			return nil, fmt.Errorf("failed to unmarshal report: %w", err)
		}
		job.Report = &report
	}
	
	return job, nil
}

func (s *RedisJobStore) Close() error {
	return s.client.Close()
}
