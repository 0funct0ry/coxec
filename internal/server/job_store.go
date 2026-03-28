package server

import (
	"sort"
	"sync"
	"time"

	"github.com/0funct0ry/coxec/internal/engine"
)

// JobStatus represents the current state of an asynchronous job.
type JobStatus string

const (
	JobStatusQueued    JobStatus = "queued"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCancelled JobStatus = "cancelled"
)

// Job represents an asynchronous execution job.
type Job struct {
	ID          string                  `json:"job_id"`
	Status      JobStatus               `json:"status"`
	Request     ExecRequest             `json:"request"`
	CreatedAt   time.Time               `json:"created_at"`
	StartedAt   *time.Time              `json:"started_at,omitempty"`
	CompletedAt *time.Time              `json:"completed_at,omitempty"`
	Report      *engine.ExecutionReport `json:"report,omitempty"`
	Error       string                  `json:"error,omitempty"`
}

// JobSummary is a lightweight view of a Job returned by GET /jobs.
type JobSummary struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	State       JobStatus  `json:"state"`
	SubmittedAt time.Time  `json:"submitted_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// ListFilter controls filtering and pagination for List().
type ListFilter struct {
	Limit  int
	Offset int
	TTL    time.Duration // retention window; 0 means no expiry filter
}

// JobStore defines the interface for storing and retrieving jobs.
type JobStore interface {
	Create(job *Job) error
	Get(id string) (*Job, bool)
	Update(job *Job) error
	Delete(id string) error
	// List returns a page of jobs matching the filter along with the total count
	// of all matching jobs (before pagination).
	List(filter ListFilter) ([]*Job, int, error)
	Prune(limit int, ttl time.Duration) (int, error)

	// Idempotency support
	GetByIdempotencyKey(key string) (string, bool)
	SetIdempotencyKey(key string, jobID string)
}

// InMemoryJobStore implements JobStore using an in-memory map.
type InMemoryJobStore struct {
	mu             sync.RWMutex
	jobs           map[string]*Job
	idempotencyMap map[string]string
}

// NewInMemoryJobStore creates a new InMemoryJobStore.
func NewInMemoryJobStore() *InMemoryJobStore {
	return &InMemoryJobStore{
		jobs:           make(map[string]*Job),
		idempotencyMap: make(map[string]string),
	}
}

func (s *InMemoryJobStore) Create(job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job.Clone()
	return nil
}

func (s *InMemoryJobStore) Get(id string) (*Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	return job.Clone(), true
}

func (s *InMemoryJobStore) Update(job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job.Clone()
	return nil
}

func (s *InMemoryJobStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.jobs, id)
	return nil
}

func (s *InMemoryJobStore) List(filter ListFilter) ([]*Job, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	matched := make([]*Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		// Active jobs are always included.
		if job.Status == JobStatusQueued || job.Status == JobStatusRunning {
			matched = append(matched, job.Clone())
			continue
		}
		// Terminal jobs: respect TTL if set.
		if filter.TTL > 0 {
			var age time.Duration
			if job.CompletedAt != nil {
				age = now.Sub(*job.CompletedAt)
			} else {
				age = now.Sub(job.CreatedAt)
			}
			if age > filter.TTL {
				continue // expired — skip
			}
		}
		matched = append(matched, job.Clone())
	}

	// Sort newest-first by CreatedAt.
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})

	total := len(matched)

	// Apply offset.
	if filter.Offset >= total {
		return []*Job{}, total, nil
	}
	matched = matched[filter.Offset:]

	// Apply limit.
	if filter.Limit > 0 && len(matched) > filter.Limit {
		matched = matched[:filter.Limit]
	}

	return matched, total, nil
}

func (s *InMemoryJobStore) Prune(limit int, ttl time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var terminalJobs []*Job
	for _, job := range s.jobs {
		if job.Status == JobStatusCompleted || job.Status == JobStatusFailed || job.Status == JobStatusCancelled {
			terminalJobs = append(terminalJobs, job)
		}
	}

	count := 0
	// 1. TTL eviction
	for _, job := range terminalJobs {
		var age time.Duration
		if job.CompletedAt != nil {
			age = now.Sub(*job.CompletedAt)
		} else {
			age = now.Sub(job.CreatedAt)
		}

		if ttl > 0 && age > ttl {
			delete(s.jobs, job.ID)
			s.removeIdempotencyKeyForJob(job.ID)
			count++
		}
	}

	// Refresh terminal jobs list after TTL eviction
	terminalJobs = terminalJobs[:0]
	for _, job := range s.jobs {
		if job.Status == JobStatusCompleted || job.Status == JobStatusFailed || job.Status == JobStatusCancelled {
			terminalJobs = append(terminalJobs, job)
		}
	}

	// 2. History limit eviction (FIFO based on CreatedAt)
	if limit > 0 && len(terminalJobs) > limit {
		// Sort by CreatedAt ascending (oldest first)
		sort.Slice(terminalJobs, func(i, j int) bool {
			return terminalJobs[i].CreatedAt.Before(terminalJobs[j].CreatedAt)
		})

		toEvict := len(terminalJobs) - limit
		for i := 0; i < toEvict; i++ {
			job := terminalJobs[i]
			delete(s.jobs, job.ID)
			s.removeIdempotencyKeyForJob(job.ID)
			count++
		}
	}

	return count, nil
}

func (s *InMemoryJobStore) removeIdempotencyKeyForJob(jobID string) {
	for k, v := range s.idempotencyMap {
		if v == jobID {
			delete(s.idempotencyMap, k)
			// Assume one-to-one mapping for now, or continue if many-to-one
			return
		}
	}
}

// Clone creates a deep copy of the Job
func (j *Job) Clone() *Job {
	if j == nil {
		return nil
	}
	clone := *j
	if j.StartedAt != nil {
		t := *j.StartedAt
		clone.StartedAt = &t
	}
	if j.CompletedAt != nil {
		t := *j.CompletedAt
		clone.CompletedAt = &t
	}
	if j.Report != nil {
		r := *j.Report
		clone.Report = &r
		// Details and other slices in Report are not modified after RunJobPool returns
	}
	if j.Request.Vars != nil {
		vars := make(map[string]string)
		for k, v := range j.Request.Vars {
			vars[k] = v
		}
		clone.Request.Vars = vars
	}
	return &clone
}

func (s *InMemoryJobStore) GetByIdempotencyKey(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.idempotencyMap[key]
	return id, ok
}

func (s *InMemoryJobStore) SetIdempotencyKey(key string, jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idempotencyMap[key] = jobID
}
