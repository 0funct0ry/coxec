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
	s.jobs[job.ID] = job
	return nil
}

func (s *InMemoryJobStore) Get(id string) (*Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	return job, ok
}

func (s *InMemoryJobStore) Update(job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job
	return nil
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
