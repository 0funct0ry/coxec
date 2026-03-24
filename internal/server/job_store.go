package server

import (
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

// JobStore defines the interface for storing and retrieving jobs.
type JobStore interface {
	Create(job *Job) error
	Get(id string) (*Job, bool)
	Update(job *Job) error
	Delete(id string) error
	List() ([]*Job, error)
	Cleanup(ttl time.Duration) (int, error)
	
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

func (s *InMemoryJobStore) List() ([]*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	jobs := make([]*Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job.Clone())
	}
	return jobs, nil
}

func (s *InMemoryJobStore) Cleanup(ttl time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	count := 0
	now := time.Now()
	for id, job := range s.jobs {
		// Only clean up finished jobs
		if job.Status == JobStatusCompleted || job.Status == JobStatusFailed || job.Status == JobStatusCancelled {
			if job.CompletedAt != nil && now.Sub(*job.CompletedAt) > ttl {
				delete(s.jobs, id)
				count++
			} else if job.CompletedAt == nil && now.Sub(job.CreatedAt) > ttl {
				// Fallback for cases where CompletedAt is not set but job is in a terminal state
				delete(s.jobs, id)
				count++
			}
		}
	}
	return count, nil
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
