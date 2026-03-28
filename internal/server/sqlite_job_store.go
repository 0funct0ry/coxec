package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/0funct0ry/coxec/internal/engine"
	_ "modernc.org/sqlite"
)

// SQLiteJobStore implements JobStore using a SQLite backend.
type SQLiteJobStore struct {
	db *sql.DB
}

// NewSQLiteJobStore creates a new SQLiteJobStore and initializes the schema.
func NewSQLiteJobStore(dsn string) (*SQLiteJobStore, error) {
	// Ensure the parent directory exists
	dir := filepath.Dir(dsn)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Wait for the connection to be established or time out
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping sqlite database: %w", err)
	}

	s := &SQLiteJobStore{db: db}
	if err := s.initSchema(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *SQLiteJobStore) initSchema() error {
	queries := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			request TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			started_at DATETIME,
			completed_at DATETIME,
			report TEXT,
			error TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS idempotency_keys (
			key TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_created_at ON jobs(created_at)`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("failed to execute schema query: %w", err)
		}
	}
	return nil
}

func (s *SQLiteJobStore) Create(job *Job) error {
	reqJSON, err := json.Marshal(job.Request)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	var reportJSON interface{}
	if job.Report != nil {
		reportJSON, err = json.Marshal(job.Report)
		if err != nil {
			return fmt.Errorf("failed to marshal report: %w", err)
		}
	}

	_, err = s.db.Exec(
		"INSERT INTO jobs (id, status, request, created_at, started_at, completed_at, report, error) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		job.ID, job.Status, string(reqJSON), job.CreatedAt, job.StartedAt, job.CompletedAt, reportJSON, job.Error,
	)
	if err != nil {
		return fmt.Errorf("failed to insert job: %w", err)
	}
	return nil
}

func (s *SQLiteJobStore) Get(id string) (*Job, bool) {
	var job Job
	var reqJSON, reportJSON sql.NullString
	var startedAt, completedAt sql.NullTime

	err := s.db.QueryRow(
		"SELECT id, status, request, created_at, started_at, completed_at, report, error FROM jobs WHERE id = ?",
		id,
	).Scan(&job.ID, &job.Status, &reqJSON, &job.CreatedAt, &startedAt, &completedAt, &reportJSON, &job.Error)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, false
		}
		return nil, false
	}

	if reqJSON.Valid {
		if err := json.Unmarshal([]byte(reqJSON.String), &job.Request); err != nil {
			return nil, false
		}
	}

	if reportJSON.Valid {
		var report engine.ExecutionReport
		if err := json.Unmarshal([]byte(reportJSON.String), &report); err != nil {
			return nil, false
		}
		job.Report = &report
	}

	if startedAt.Valid {
		job.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		job.CompletedAt = &completedAt.Time
	}

	return &job, true
}

func (s *SQLiteJobStore) Update(job *Job) error {
	reqJSON, err := json.Marshal(job.Request)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	var reportJSON interface{}
	if job.Report != nil {
		reportJSON, err = json.Marshal(job.Report)
		if err != nil {
			return fmt.Errorf("failed to marshal report: %w", err)
		}
	}

	_, err = s.db.Exec(
		"UPDATE jobs SET status = ?, request = ?, created_at = ?, started_at = ?, completed_at = ?, report = ?, error = ? WHERE id = ?",
		job.Status, string(reqJSON), job.CreatedAt, job.StartedAt, job.CompletedAt, reportJSON, job.Error, job.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update job: %w", err)
	}
	return nil
}

func (s *SQLiteJobStore) Delete(id string) error {
	_, err := s.db.Exec("DELETE FROM jobs WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete job: %w", err)
	}
	return nil
}

func (s *SQLiteJobStore) List(filter ListFilter) ([]*Job, int, error) {
	now := time.Now()
	var total int

	// Count matching jobs
	countQuery := "SELECT COUNT(*) FROM jobs"
	var countArgs []interface{}
	if filter.TTL > 0 {
		countQuery += " WHERE (status IN ('queued', 'running')) OR (completed_at >= ?) OR (completed_at IS NULL AND created_at >= ?)"
		cutoff := now.Add(-filter.TTL)
		countArgs = append(countArgs, cutoff, cutoff)
	}

	err := s.db.QueryRow(countQuery, countArgs...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count jobs: %w", err)
	}

	// Fetch matching jobs
	selectQuery := "SELECT id, status, request, created_at, started_at, completed_at, report, error FROM jobs"
	var selectArgs []interface{}
	if filter.TTL > 0 {
		selectQuery += " WHERE (status IN ('queued', 'running')) OR (completed_at >= ?) OR (completed_at IS NULL AND created_at >= ?)"
		cutoff := now.Add(-filter.TTL)
		selectArgs = append(selectArgs, cutoff, cutoff)
	}

	selectQuery += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		selectQuery += " LIMIT ?"
		selectArgs = append(selectArgs, filter.Limit)
	}
	if filter.Offset > 0 {
		selectQuery += " OFFSET ?"
		selectArgs = append(selectArgs, filter.Offset)
	}

	rows, err := s.db.Query(selectQuery, selectArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		var job Job
		var reqJSON, reportJSON sql.NullString
		var startedAt, completedAt sql.NullTime

		if err := rows.Scan(&job.ID, &job.Status, &reqJSON, &job.CreatedAt, &startedAt, &completedAt, &reportJSON, &job.Error); err != nil {
			return nil, 0, fmt.Errorf("failed to scan job: %w", err)
		}

		if reqJSON.Valid {
			if err := json.Unmarshal([]byte(reqJSON.String), &job.Request); err != nil {
				return nil, 0, fmt.Errorf("failed to unmarshal request: %w", err)
			}
		}

		if reportJSON.Valid {
			var report engine.ExecutionReport
			if err := json.Unmarshal([]byte(reportJSON.String), &report); err != nil {
				return nil, 0, fmt.Errorf("failed to unmarshal report: %w", err)
			}
			job.Report = &report
		}

		if startedAt.Valid {
			job.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			job.CompletedAt = &completedAt.Time
		}

		jobs = append(jobs, &job)
	}

	return jobs, total, nil
}

func (s *SQLiteJobStore) Prune(limit int, ttl time.Duration) (int, error) {
	now := time.Now()
	totalPruned := 0

	// 1. TTL Pruning
	if ttl > 0 {
		cutoff := now.Add(-ttl)
		res, err := s.db.Exec(
			"DELETE FROM jobs WHERE status IN ('completed', 'failed', 'cancelled') AND completed_at < ?",
			cutoff,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to prune by TTL: %w", err)
		}
		pruned, _ := res.RowsAffected()
		totalPruned += int(pruned)
	}

	// 2. History Limit Pruning
	if limit > 0 {
		// Find terminal jobs count
		var terminalCount int
		err := s.db.QueryRow(
			"SELECT COUNT(*) FROM jobs WHERE status IN ('completed', 'failed', 'cancelled')",
		).Scan(&terminalCount)
		if err != nil {
			return totalPruned, fmt.Errorf("failed to count terminal jobs: %w", err)
		}

		if terminalCount > limit {
			toDelete := terminalCount - limit
			// Delete oldest terminal jobs
			_, err := s.db.Exec(
				`DELETE FROM jobs WHERE id IN (
					SELECT id FROM jobs 
					WHERE status IN ('completed', 'failed', 'cancelled') 
					ORDER BY created_at ASC 
					LIMIT ?
				)`,
				toDelete,
			)
			if err != nil {
				return totalPruned, fmt.Errorf("failed to prune by history limit: %w", err)
			}
			totalPruned += toDelete
		}
	}

	return totalPruned, nil
}

func (s *SQLiteJobStore) GetByIdempotencyKey(key string) (string, bool) {
	var jobID string
	err := s.db.QueryRow("SELECT job_id FROM idempotency_keys WHERE key = ?", key).Scan(&jobID)
	if err != nil {
		return "", false
	}
	return jobID, true
}

func (s *SQLiteJobStore) SetIdempotencyKey(key string, jobID string) {
	_, _ = s.db.Exec("INSERT OR REPLACE INTO idempotency_keys (key, job_id) VALUES (?, ?)", key, jobID)
}

func (s *SQLiteJobStore) Close() error {
	return s.db.Close()
}
