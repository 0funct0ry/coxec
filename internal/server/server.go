package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/0funct0ry/coxec/internal/config"
	"github.com/0funct0ry/coxec/internal/engine"
)

// ServerStatus represents the current lifecycle state of the server.
type ServerStatus string

const (
	StatusStarting     ServerStatus = "starting"
	StatusReady        ServerStatus = "ready"
	StatusShuttingDown ServerStatus = "shutting_down"
)

// Server represents the coxec HTTP server.
type Server struct {
	Addr           string
	Port           int
	Version        string
	StartTime      time.Time
	mu             sync.RWMutex
	Status         ServerStatus
	ActiveJobs     atomic.Int32
	AuthToken      string
	AuthBasic      string
	AuthHmacSecret string
	TLSCert        string
	TLSKey         string
	Registry       *engine.BuiltinRegistry
	DefaultConcurrency int
	DefaultIterations  int
	MaxConcurrentJobs  int
	EnableSync         bool
	JobStore           JobStore
	JobTTL             time.Duration
	JobHistory         int
	NamedJobs          map[string]config.NamedJobConfig
	jobCancels         map[string]context.CancelFunc
	jobSubscribers     sync.Map // map[string]*sync.Map (subID -> chan interface{})
}

// ExecRequest defines the payload for POST /exec
type ExecRequest struct {
	Exec        interface{}       `json:"exec"`
	Concurrency int               `json:"concurrency,omitempty"`
	Iterations  int               `json:"iterations,omitempty"`
	Timeout     string            `json:"timeout,omitempty"`
	Rate        string            `json:"rate,omitempty"`
	Vars        map[string]string `json:"vars,omitempty"`
	Delay       string            `json:"delay,omitempty"`
	Jitter      string            `json:"jitter,omitempty"`
	RampUp      string            `json:"rampup,omitempty"`
	Verbose     bool              `json:"verbose,omitempty"`
	Label       string            `json:"label,omitempty"`
}

// ExecResponse defines the response for POST /exec
type ExecResponse struct {
	Status string                  `json:"status"`
	Report *engine.ExecutionReport `json:"report,omitempty"`
	Error  string                  `json:"error,omitempty"`
}

// ListJobsResponse is the response body for GET /jobs.
type ListJobsResponse struct {
	Jobs       []*JobSummary `json:"jobs"`
	Total      int           `json:"total"`
	Limit      int           `json:"limit"`
	Offset     int           `json:"offset"`
	ActiveJobs int           `json:"active_jobs"`
}

// JobSummaryStats holds aggregate statistics for a terminal job.
type JobSummaryStats struct {
	SuccessCount   int            `json:"success_count"`
	FailCount      int            `json:"fail_count"`
	TotalDuration  string         `json:"total_duration"`
	AverageLatency string         `json:"average_latency"`
	HTTPErrors     map[string]int `json:"http_errors,omitempty"`
	TCPErrors      map[string]int `json:"tcp_errors,omitempty"`
	TemplateErrors map[string]int `json:"template_errors,omitempty"`
}

// JobDetailResponse is the response body for GET /jobs/:id.
type JobDetailResponse struct {
	JobID               string           `json:"job_id"`
	State               JobStatus        `json:"state"`
	SubmittedAt         time.Time        `json:"submitted_at"`
	StartedAt           *time.Time       `json:"started_at,omitempty"`
	Duration            string           `json:"duration,omitempty"`
	Concurrency         int              `json:"concurrency"`
	IterationsRequested int              `json:"iterations_requested"`
	IterationsCompleted int              `json:"iterations_completed"`
	Label               string           `json:"label,omitempty"`
	Summary             *JobSummaryStats `json:"summary,omitempty"`
	Error               string           `json:"error,omitempty"`
}

// JobReportResponse defines the detailed summary of a terminal job.
type JobReportResponse struct {
	JobID       string `json:"job_id"`
	Status      string `json:"status"` // overall status: success, partial, failed
	Duration    string `json:"duration"`
	Concurrency int    `json:"concurrency"`
	Iterations  struct {
		Requested int `json:"requested"`
		Completed int `json:"completed"`
	} `json:"iterations"`
	Counts struct {
		Success int `json:"success"`
		Failure int `json:"failure"`
		Retry   int `json:"retry"`
	} `json:"counts"`
	Latencies struct {
		Min string `json:"min"`
		P50 string `json:"p50"`
		P75 string `json:"p75"`
		P90 string `json:"p90"`
		P95 string `json:"p95"`
		P99 string `json:"p99"`
		Max string `json:"max"`
	} `json:"latencies"`
	Errors []JobErrorReport `json:"errors,omitempty"`
}

type JobErrorReport struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Count   int    `json:"count"`
}

// NewServer creates a new Server instance.
func NewServer(addr string, port int, version string, authToken string, authBasic string, authHmacSecret string, tlsCert string, tlsKey string, registry *engine.BuiltinRegistry, defaultConcurrency, defaultIterations int, maxConcurrentJobs int, enableSync bool, jobStore JobStore, jobTTL time.Duration, jobHistory int, namedJobs []config.NamedJobConfig) *Server {
	njMap := make(map[string]config.NamedJobConfig)
	for _, nj := range namedJobs {
		njMap[nj.Name] = nj
	}

	return &Server{
		Addr:               addr,
		Port:               port,
		Version:            version,
		AuthToken:          authToken,
		AuthBasic:          authBasic,
		AuthHmacSecret:     authHmacSecret,
		TLSCert:            tlsCert,
		TLSKey:             tlsKey,
		StartTime:          time.Now(),
		Status:             StatusStarting,
		Registry:           registry,
		DefaultConcurrency: defaultConcurrency,
		DefaultIterations:  defaultIterations,
		MaxConcurrentJobs:  maxConcurrentJobs,
		EnableSync:         enableSync,
		JobStore:           jobStore,
		JobTTL:             jobTTL,
		JobHistory:         jobHistory,
		NamedJobs:          njMap,
		jobCancels:         make(map[string]context.CancelFunc),
	}
}

// Start starts the HTTP server and waits for it to shut down.
func (s *Server) Start(ctx context.Context) error {
	fullAddr := fmt.Sprintf("%s:%d", s.Addr, s.Port)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.HealthHandler)
	mux.HandleFunc("/exec", s.ExecHandler)
	mux.HandleFunc("/async/exec", s.AsyncExecHandler)
	mux.HandleFunc("/jobs", s.ListJobsHandler)   // GET /jobs — list all jobs
	mux.HandleFunc("/jobs/", s.handleJobsPath)    // Multiplexer for /jobs/:id and /jobs/:name/run

	srv := &http.Server{
		Addr:    fullAddr,
		Handler: mux,
	}

	errChan := make(chan error, 1)
	go func() {
		protocol := "http"
		useTLS := s.TLSCert != "" && s.TLSKey != ""
		if useTLS {
			protocol = "https"
		}
		fmt.Printf("coxec server listening on %s (%s)\n", fullAddr, protocol)
		s.mu.Lock()
		s.Status = StatusReady
		s.mu.Unlock()
		var err error
		if useTLS {
			err = srv.ListenAndServeTLS(s.TLSCert, s.TLSKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	// Start background cleanup
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				count, _ := s.JobStore.Prune(s.JobHistory, s.JobTTL)
				if count > 0 {
					fmt.Printf("[%s] Pruned %d completed jobs (Limit: %d, TTL: %v)\n", time.Now().Format(time.RFC3339), count, s.JobHistory, s.JobTTL)
				}
			}
		}
	}()

	select {
	case <-ctx.Done():
		fmt.Println("\nShutting down server...")
		s.mu.Lock()
		s.Status = StatusShuttingDown
		s.mu.Unlock()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errChan:
		return fmt.Errorf("server failed: %w", err)
	}
}

func (s *Server) HealthHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	currentStatus := s.Status
	s.mu.RUnlock()

	if currentStatus != StatusReady {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": string(currentStatus),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"version":        s.Version,
		"active_jobs":    s.ActiveJobs.Load(),
		"uptime_seconds": int64(time.Since(s.StartTime).Seconds()),
	})
}

func (s *Server) checkConcurrencyLimit(w http.ResponseWriter) bool {
	if s.MaxConcurrentJobs > 0 && s.ActiveJobs.Load() >= int32(s.MaxConcurrentJobs) {
		w.Header().Set("Retry-After", "60")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "error",
			"error":  "server at maximum concurrent job capacity",
		})
		return false
	}
	return true
}

func (s *Server) ExecHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "method not allowed"})
		return
	}

	s.mu.RLock()
	currentStatus := s.Status
	s.mu.RUnlock()

	if currentStatus != StatusReady {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "server is not ready"})
		return
	}

	if !s.checkConcurrencyLimit(w) {
		return
	}

	req, body, errorRes := s.validateAndParseRequest(w, r)
	if errorRes != nil {
		w.WriteHeader(errorRes.code)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: errorRes.err})
		return
	}

	execStr, concurrency, iterations, _, err := s.prepareExecPlan(req, body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: err.Error()})
		return
	}

	fmt.Printf("[%s] Executing: %s (Concurrency: %d, Iterations: %d)\n", time.Now().Format(time.RFC3339), execStr, concurrency, iterations)

	timeout, _ := time.ParseDuration(req.Timeout)
	delay, _ := time.ParseDuration(req.Delay)
	jitter, _ := time.ParseDuration(req.Jitter)
	rampup, _ := time.ParseDuration(req.RampUp)

	rateLimit := s.parseRateLimit(req.Rate)

	s.ActiveJobs.Add(1)
	defer s.ActiveJobs.Add(-1)

	opts := engine.ExecOptions{
		Silent:        false,
		Verbose:       req.Verbose,
		Report:        true,
		TotalTasks:    iterations,
		Context:       r.Context(),
		Stdout:        &strings.Builder{},
		Stderr:        &strings.Builder{},
		UserVars:      req.Vars,
		Registry:      s.Registry,
		TemplateState: engine.NewTemplateState(),
		Timeout:       timeout,
		Delay:         delay,
		Jitter:        jitter,
		RampUp:        rampup,
		RateLimit:     rateLimit,
	}

	tasks := s.generateTasks(r.Context(), iterations, execStr, rateLimit, delay, jitter)

	report, err := engine.RunJobPool(concurrency, tasks, opts)
	if err != nil && report == nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: err.Error()})
		return
	}

	// Response negotiation
	accept := r.Header.Get("Accept")
	useJSON := strings.Contains(accept, "application/json")
	if !useJSON && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		// If they sent JSON, they probably want JSON back unless they explicitly asked for something else
		useJSON = true
	}
	if accept == "*/*" || accept == "" {
		// Default to text for curl/browsers unless they specifically asked for JSON or sent JSON
		if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			useJSON = false
		}
	}

	if useJSON {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(ExecResponse{
			Status: "ok",
			Report: report,
		})
		return
	}

	// Default to plain text
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(s.formatReportText(report)))
}
func (s *Server) handleJobsPath(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/jobs/")
	if path == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Check if it's a named job trigger: /jobs/:name/run
	if strings.HasSuffix(path, "/run") && r.Method == http.MethodPost {
		name := strings.TrimSuffix(path, "/run")
		s.NamedJobRunHandler(w, r, name)
		return
	}

	// Otherwise, it's /jobs/:id related
	s.JobsHandler(w, r)
}

func (s *Server) NamedJobRunHandler(w http.ResponseWriter, r *http.Request, name string) {
	nj, ok := s.NamedJobs[name]
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("named job not found: %s", name)})
		return
	}

	// Parse overrides from request body
	var override ExecRequest
	var bodyBytes []byte
	if r.Body != nil && r.ContentLength > 0 {
		rawBytes, _ := io.ReadAll(r.Body)
		if authErr := s.checkAuth(w, r, rawBytes); authErr != nil {
			w.WriteHeader(authErr.code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": authErr.err})
			return
		}
		bodyBytes, _ = makeTemplateExprsJSONSafe(rawBytes)
		if err := json.Unmarshal(bodyBytes, &override); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body overrides"})
			return
		}
	} else {
		// Even for empty body, check auth (GET-style check if no body)
		if authErr := s.checkAuth(w, r, nil); authErr != nil {
			w.WriteHeader(authErr.code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": authErr.err})
			return
		}
	}

	// Merge named job definition with overrides
	req := ExecRequest{
		Exec:        nj.Exec,
		Concurrency: nj.Concurrency,
		Iterations:  nj.Iterations,
		Timeout:     nj.Timeout,
		Rate:        nj.Rate,
		Vars:        make(map[string]string),
		Delay:       nj.Delay,
		Jitter:      nj.Jitter,
		RampUp:      nj.RampUp,
		Label:       nj.Label,
	}

	// Copy vars from definition
	for k, v := range nj.Vars {
		req.Vars[k] = v
	}

	// Apply overrides
	if override.Exec != nil {
		req.Exec = override.Exec
	}
	if override.Concurrency > 0 {
		req.Concurrency = override.Concurrency
	}
	if override.Iterations > 0 {
		req.Iterations = override.Iterations
	}
	if override.Timeout != "" {
		req.Timeout = override.Timeout
	}
	if override.Rate != "" {
		req.Rate = override.Rate
	}
	if override.Delay != "" {
		req.Delay = override.Delay
	}
	if override.Jitter != "" {
		req.Jitter = override.Jitter
	}
	if override.RampUp != "" {
		req.RampUp = override.RampUp
	}
	if override.Label != "" {
		req.Label = override.Label
	}
	for k, v := range override.Vars {
		req.Vars[k] = v
	}

	// Forward to AsyncExecHandler logic
	s.runAsyncJob(w, r, req, bodyBytes)
}

func (s *Server) AsyncExecHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "method not allowed"})
		return
	}

	s.mu.RLock()
	currentStatus := s.Status
	s.mu.RUnlock()

	if currentStatus != StatusReady {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "server is not ready"})
		return
	}

	// Check Idempotency-Key
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey != "" {
		if existingID, ok := s.JobStore.GetByIdempotencyKey(idempotencyKey); ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"job_id": existingID,
				"status": "queued",
			})
			return
		}
	}

	req, bodyBytes, errorRes := s.validateAndParseRequest(w, r)
	if errorRes != nil {
		w.WriteHeader(errorRes.code)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: errorRes.err})
		return
	}

	s.runAsyncJob(w, r, req, bodyBytes)
}

func (s *Server) runAsyncJob(w http.ResponseWriter, r *http.Request, req ExecRequest, bodyBytes []byte) {
	if !s.checkConcurrencyLimit(w) {
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	
	execStr, concurrency, iterations, _, err := s.prepareExecPlan(req, bodyBytes)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: err.Error()})
		return
	}

	jobID := engine.GenerateUUIDv4()
	job := &Job{
		ID:        jobID,
		Status:    JobStatusQueued,
		Request:   req,
		CreatedAt: time.Now(),
	}

	if idempotencyKey != "" {
		s.JobStore.SetIdempotencyKey(idempotencyKey, jobID)
	}

	if err := s.JobStore.Create(job); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "failed to create job"})
		return
	}

	// Start background execution
	go func() {
		s.ActiveJobs.Add(1)
		defer s.ActiveJobs.Add(-1)

		timeout, _ := time.ParseDuration(req.Timeout)
		delay, _ := time.ParseDuration(req.Delay)
		jitter, _ := time.ParseDuration(req.Jitter)
		rampup, _ := time.ParseDuration(req.RampUp)
		rateLimit := s.parseRateLimit(req.Rate)

		// Create a background context with the same timeout as the request if provided
		ctx, cancel := context.WithCancel(context.Background())
		if timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, timeout)
		}
		defer cancel()

		s.mu.Lock()
		s.jobCancels[jobID] = cancel
		s.mu.Unlock()

		defer func() {
			s.mu.Lock()
			delete(s.jobCancels, jobID)
			s.mu.Unlock()
		}()

		now := time.Now()
		job.StartedAt = &now
		job.Status = JobStatusRunning
		_ = s.JobStore.Update(job)

		opts := engine.ExecOptions{
			Silent:        true,
			Verbose:       req.Verbose,
			Report:        true,
			TotalTasks:    iterations,
			Context:       ctx,
			Stdout:        &strings.Builder{},
			Stderr:        &strings.Builder{},
			UserVars:      req.Vars,
			Registry:      s.Registry,
			TemplateState: engine.NewTemplateState(),
			Timeout:       timeout,
			Delay:         delay,
			Jitter:        jitter,
			RampUp:        rampup,
			RateLimit:     rateLimit,
			OnResult: func(detail engine.ExecutionDetail) {
				s.broadcast(jobID, map[string]interface{}{
					"type": "result",
					"data": detail,
				})
			},
		}

		tasks := s.generateTasks(ctx, iterations, execStr, rateLimit, delay, jitter)
		report, err := engine.RunJobPool(concurrency, tasks, opts)

		doneAt := time.Now()
		job.CompletedAt = &doneAt
		
		if ctx.Err() == context.Canceled {
			job.Status = JobStatusCancelled
		} else if err != nil {
			job.Status = JobStatusFailed
			job.Error = err.Error()
		} else {
			job.Status = JobStatusCompleted
		}
		job.Report = report
		_ = s.JobStore.Update(job)

		s.broadcast(jobID, map[string]interface{}{
			"type": "done",
			"data": jobToDetail(job),
		})
		s.closeSubscribers(jobID)
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"job_id": jobID,
		"status": "queued",
	})
}

func (s *Server) JobsHandler(w http.ResponseWriter, r *http.Request) {
	// Extract job ID from /jobs/:id
	id := strings.TrimPrefix(r.URL.Path, "/jobs/")
	if id == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		if authErr := s.checkAuth(w, r, nil); authErr != nil {
			w.WriteHeader(authErr.code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": authErr.err})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/report") {
			s.JobReportHandler(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/stream") {
			s.JobStreamHandler(w, r)
			return
		}

		job, ok := s.JobStore.Get(id)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "job not found or expired"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(jobToDetail(job))

	case http.MethodDelete:
		if authErr := s.checkAuth(w, r, nil); authErr != nil {
			w.WriteHeader(authErr.code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": authErr.err})
			return
		}

		job, ok := s.JobStore.Get(id)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "job not found"})
			return
		}

		// Conflict if job is already terminal
		if job.Status == JobStatusCompleted || job.Status == JobStatusFailed || job.Status == JobStatusCancelled {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("cannot cancel job in terminal state: %s", job.Status)})
			return
		}

		s.mu.Lock()
		cancel, running := s.jobCancels[id]
		s.mu.Unlock()

		if running {
			cancel()
			// Update status immediately for better UX, though the background goroutine will also do it
			job.Status = JobStatusCancelled
			now := time.Now()
			job.CompletedAt = &now
			_ = s.JobStore.Update(job)
			w.WriteHeader(http.StatusAccepted)
		} else if job.Status == JobStatusQueued {
			// Job is queued but not yet in jobCancels (about to start)
			job.Status = JobStatusCancelled
			now := time.Now()
			job.CompletedAt = &now
			_ = s.JobStore.Update(job)
			w.WriteHeader(http.StatusAccepted)
		} else {
			// Fallback for any other state
			w.WriteHeader(http.StatusOK)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": string(job.Status)})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// jobToDetail converts a Job into the rich JobDetailResponse for GET /jobs/:id.
func jobToDetail(job *Job) *JobDetailResponse {
	resp := &JobDetailResponse{
		JobID:               job.ID,
		State:               job.Status,
		SubmittedAt:         job.CreatedAt,
		StartedAt:           job.StartedAt,
		Concurrency:         job.Request.Concurrency,
		IterationsRequested: job.Request.Iterations,
		Label:               job.Request.Label,
		Error:               job.Error,
	}

	// Compute wall-clock duration for finished jobs.
	if job.StartedAt != nil && job.CompletedAt != nil {
		resp.Duration = job.CompletedAt.Sub(*job.StartedAt).String()
	}

	// Pull iteration count and summary stats from the execution report.
	if job.Report != nil {
		resp.IterationsCompleted = job.Report.TotalExecutions

		// Attach summary for terminal states.
		if job.Status == JobStatusCompleted || job.Status == JobStatusFailed || job.Status == JobStatusCancelled {
			resp.Summary = &JobSummaryStats{
				SuccessCount:   job.Report.SuccessCount,
				FailCount:      job.Report.FailCount,
				TotalDuration:  job.Report.TotalDuration,
				AverageLatency: job.Report.AverageLatency,
				HTTPErrors:     job.Report.HTTPErrors,
				TCPErrors:      job.Report.TCPErrors,
				TemplateErrors: job.Report.TemplateErrors,
			}
		}
	}

	return resp
}

func (s *Server) subscribe(jobID string) (chan interface{}, func()) {
	ch := make(chan interface{}, 100)

	val, _ := s.jobSubscribers.LoadOrStore(jobID, &sync.Map{})
	subMap := val.(*sync.Map)

	subID := engine.GenerateUUIDv4()
	subMap.Store(subID, ch)

	cleanup := func() {
		subMap.Delete(subID)
	}

	return ch, cleanup
}

func (s *Server) broadcast(jobID string, event interface{}) {
	if val, ok := s.jobSubscribers.Load(jobID); ok {
		subMap := val.(*sync.Map)
		subMap.Range(func(key, value interface{}) bool {
			ch := value.(chan interface{})
			select {
			case ch <- event:
			default:
				// Channel full, slower client might miss an event
			}
			return true
		})
	}
}

func (s *Server) closeSubscribers(jobID string) {
	if val, ok := s.jobSubscribers.LoadAndDelete(jobID); ok {
		subMap := val.(*sync.Map)
		subMap.Range(func(key, value interface{}) bool {
			ch := value.(chan interface{})
			close(ch)
			return true
		})
	}
}

// JobStreamHandler handles GET /jobs/:id/stream — streams execution results via SSE.
func (s *Server) JobStreamHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/jobs/")
	id = strings.TrimSuffix(id, "/stream")

	if authErr := s.checkAuth(w, r, nil); authErr != nil {
		w.WriteHeader(authErr.code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": authErr.err})
		return
	}

	job, ok := s.JobStore.Get(id)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "job not found or expired"})
		return
	}

	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// If job is already terminal, send existing results (if any) and the final report.
	if job.Status == JobStatusCompleted || job.Status == JobStatusFailed || job.Status == JobStatusCancelled {
		if job.Report != nil {
			for _, detail := range job.Report.Details {
				data, _ := json.Marshal(map[string]interface{}{
					"type": "result",
					"data": detail,
				})
				fmt.Fprintf(w, "event: result\ndata: %s\n\n", data)
			}
		}
		
		finalData, _ := json.Marshal(map[string]interface{}{
			"type": "done",
			"data": jobToDetail(job),
		})
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", finalData)
		flusher.Flush()
		return
	}

	// Subscribe to real-time updates
	ch, cleanup := s.subscribe(id)
	defer cleanup()

	// Send a heartbeat to establish connection
	fmt.Fprintf(w, ": heartbeat\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			
			evtMap := event.(map[string]interface{})
			evtType := evtMap["type"].(string)
			data, _ := json.Marshal(evtMap)
			
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evtType, data)
			flusher.Flush()
			
			if evtType == "done" {
				return
			}
		case <-time.After(15 * time.Second):
			// Keep alive
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}
// JobReportHandler handles GET /jobs/:id/report — returns a detailed terminal report.
func (s *Server) JobReportHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/jobs/")
	id = strings.TrimSuffix(id, "/report")

	job, ok := s.JobStore.Get(id)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "job not found or expired"})
		return
	}

	// 425 Too Early if job is still running or queued
	if job.Status == JobStatusQueued || job.Status == JobStatusRunning {
		w.WriteHeader(http.StatusTooEarly)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "job is still in progress",
			"status": string(job.Status),
		})
		return
	}

	if job.Report == nil {
		// Should not happen for terminal jobs unless they failed before starting
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"job_id": job.ID,
			"status": string(job.Status),
			"error":  job.Error,
		})
		return
	}

	resp := JobReportResponse{
		JobID:       job.ID,
		Duration:    job.Report.TotalDuration,
		Concurrency: job.Request.Concurrency,
	}

	resp.Iterations.Requested = job.Request.Iterations
	resp.Iterations.Completed = job.Report.TotalExecutions

	resp.Counts.Success = job.Report.SuccessCount
	resp.Counts.Failure = job.Report.FailCount
	resp.Counts.Retry = 0 // Not implemented yet

	// Overall status
	if resp.Counts.Failure == 0 {
		resp.Status = "success"
	} else if resp.Counts.Success > 0 {
		resp.Status = "partial"
	} else {
		resp.Status = "failed"
	}

	resp.Latencies.Min = job.Report.MinLatency
	resp.Latencies.P50 = job.Report.P50Latency
	resp.Latencies.P75 = job.Report.P75Latency
	resp.Latencies.P90 = job.Report.P90Latency
	resp.Latencies.P95 = job.Report.P95Latency
	resp.Latencies.P99 = job.Report.P99Latency
	resp.Latencies.Max = job.Report.MaxLatency

	// Errors breakdown
	for errType, count := range job.Report.HTTPErrors {
		resp.Errors = append(resp.Errors, JobErrorReport{
			Type:    "HTTP",
			Message: errType,
			Count:   count,
		})
	}
	for errType, count := range job.Report.TCPErrors {
		resp.Errors = append(resp.Errors, JobErrorReport{
			Type:    "TCP",
			Message: errType,
			Count:   count,
		})
	}
	for errType, count := range job.Report.TemplateErrors {
		resp.Errors = append(resp.Errors, JobErrorReport{
			Type:    "Template",
			Message: errType,
			Count:   count,
		})
	}

	// Sort errors by count descending
	sort.Slice(resp.Errors, func(i, j int) bool {
		return resp.Errors[i].Count > resp.Errors[j].Count
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

type errorResponse struct {
	code int
	err  string
}

// checkAuth enforces authentication for the given request.
// body is the already-read request body; pass nil or []byte{} for GET requests.
// It writes the WWW-Authenticate header (Basic auth) directly to w when needed.
func (s *Server) checkAuth(w http.ResponseWriter, r *http.Request, body []byte) *errorResponse {
	if s.AuthToken != "" {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			return &errorResponse{http.StatusUnauthorized, "unauthorized"}
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.AuthToken)) != 1 {
			return &errorResponse{http.StatusUnauthorized, "unauthorized"}
		}
	}

	if s.AuthBasic != "" {
		user, pass, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			return &errorResponse{http.StatusUnauthorized, "unauthorized"}
		}
		actualUserPass := user + ":" + pass
		if subtle.ConstantTimeCompare([]byte(actualUserPass), []byte(s.AuthBasic)) != 1 {
			return &errorResponse{http.StatusUnauthorized, "unauthorized"}
		}
	}

	if s.AuthHmacSecret != "" {
		hmacHeader := r.Header.Get("X-Signature")
		if !strings.HasPrefix(hmacHeader, "sha256=") {
			return &errorResponse{http.StatusUnauthorized, "unauthorized"}
		}
		providedSig := strings.TrimPrefix(hmacHeader, "sha256=")
		expectedBytes, err := hex.DecodeString(providedSig)
		if err != nil {
			return &errorResponse{http.StatusUnauthorized, "unauthorized"}
		}
		mac := hmac.New(sha256.New, []byte(s.AuthHmacSecret))
		mac.Write(body)
		computedSig := mac.Sum(nil)
		if !hmac.Equal(computedSig, expectedBytes) {
			return &errorResponse{http.StatusUnauthorized, "unauthorized"}
		}
	}

	return nil
}

func (s *Server) validateAndParseRequest(w http.ResponseWriter, r *http.Request) (ExecRequest, []byte, *errorResponse) {
	rawBytes, _ := io.ReadAll(r.Body)

	if authErr := s.checkAuth(w, r, rawBytes); authErr != nil {
		return ExecRequest{}, nil, authErr
	}

	var req ExecRequest
	// Pre-process
	bodyBytes, _ := makeTemplateExprsJSONSafe(rawBytes)

	if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&req); err == nil && req.Exec != nil {
		// JSON success
	} else {
		// Form parsing
		r.Body = io.NopCloser(bytes.NewBuffer(rawBytes))
		if err := r.ParseForm(); err == nil {
			req.Exec = r.FormValue("exec")
			if req.Concurrency == 0 {
				if v, err := strconv.Atoi(r.FormValue("concurrency")); err == nil {
					req.Concurrency = v
				}
			}
			if req.Iterations == 0 {
				if v, err := strconv.Atoi(r.FormValue("iterations")); err == nil {
					req.Iterations = v
				}
			}
			req.Timeout = r.FormValue("timeout")
			req.Rate = r.FormValue("rate")
			req.Delay = r.FormValue("delay")
			req.Jitter = r.FormValue("jitter")
			req.RampUp = r.FormValue("rampup")
			req.Verbose, _ = strconv.ParseBool(r.FormValue("verbose"))
		}
	}

	return req, bodyBytes, nil
}

// jobToSummary converts a Job to its lightweight JobSummary representation.
// The name is derived from the exec string (first 60 chars).
func jobToSummary(job *Job) *JobSummary {
	name := fmt.Sprintf("%v", job.Request.Exec)
	if len(name) > 60 {
		name = name[:60]
	}
	return &JobSummary{
		ID:          job.ID,
		Name:        name,
		State:       job.Status,
		SubmittedAt: job.CreatedAt,
		StartedAt:   job.StartedAt,
		FinishedAt:  job.CompletedAt,
	}
}

// ListJobsHandler handles GET /jobs — returns a paginated, sorted list of job summaries.
func (s *Server) ListJobsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
		return
	}

	s.mu.RLock()
	currentStatus := s.Status
	s.mu.RUnlock()
	if currentStatus != StatusReady {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "server is not ready"})
		return
	}

	if authErr := s.checkAuth(w, r, nil); authErr != nil {
		w.WriteHeader(authErr.code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": authErr.err})
		return
	}

	// Parse pagination params; invalid values fall back to defaults.
	const defaultLimit = 50
	const maxLimit = 1000
	limit := defaultLimit
	offset := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
		if limit > maxLimit {
			limit = maxLimit
		}
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v >= 0 {
		offset = v
	}

	filter := ListFilter{
		Limit:  limit,
		Offset: offset,
		TTL:    s.JobTTL,
	}

	jobs, total, err := s.JobStore.List(filter)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to list jobs"})
		return
	}

	summaries := make([]*JobSummary, 0, len(jobs))
	for _, j := range jobs {
		summaries = append(summaries, jobToSummary(j))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ListJobsResponse{
		Jobs:       summaries,
		Total:      total,
		Limit:      limit,
		Offset:     offset,
		ActiveJobs: int(s.ActiveJobs.Load()),
	})
}

func (s *Server) prepareExecPlan(req ExecRequest, bodyBytes []byte) (string, int, int, map[string]string, error) {
	_, placeholders := makeTemplateExprsJSONSafe(bodyBytes)
	execStr, err := s.resolveExec(req.Exec)
	if err != nil || execStr == "" {
		return "", 0, 0, nil, fmt.Errorf("missing or invalid 'exec' field")
	}

	for key, original := range placeholders {
		execStr = strings.ReplaceAll(execStr, `"`+key+`"`, original)
		execStr = strings.ReplaceAll(execStr, key, original)
	}

	concurrency := req.Concurrency
	if concurrency <= 0 {
		concurrency = s.DefaultConcurrency
	}
	if concurrency <= 0 {
		concurrency = 1
	}

	iterations := req.Iterations
	if iterations <= 0 {
		iterations = s.DefaultIterations
	}
	if iterations <= 0 {
		iterations = concurrency
	}

	if concurrency > 1000 {
		return "", 0, 0, nil, fmt.Errorf("concurrency exceeds maximum allowed (1000)")
	}
	if iterations > 10000000 {
		return "", 0, 0, nil, fmt.Errorf("iterations exceed maximum allowed (10,000,000)")
	}

	return execStr, concurrency, iterations, placeholders, nil
}

func (s *Server) parseRateLimit(rate string) float64 {
	if rate == "" {
		return 0
	}
	parts := strings.Split(rate, "/")
	val, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	if len(parts) == 1 {
		return val
	}
	switch strings.ToLower(parts[1]) {
	case "s", "sec", "second", "seconds":
		return val
	case "m", "min", "minute", "minutes":
		return val / 60.0
	case "h", "hr", "hour", "hours":
		return val / 3600.0
	}
	return 0
}

func (s *Server) generateTasks(ctx context.Context, iterations int, execStr string, rateLimit float64, delay, jitter time.Duration) <-chan engine.Task {
	tasks := make(chan engine.Task, iterations)
	go func() {
		defer close(tasks)
		var lastStart time.Time
		rateInterval := time.Duration(0)
		if rateLimit > 0 {
			rateInterval = time.Duration(float64(time.Second) / rateLimit)
		}

		for i := 0; i < iterations; i++ {
			now := time.Now()
			var waitDuration time.Duration

			if i > 0 {
				if rateLimit > 0 {
					target := lastStart.Add(rateInterval)
					if target.After(now) {
						waitDuration = target.Sub(now)
					}
				}

				if delay > 0 || jitter > 0 {
					d := delay
					if jitter > 0 {
						jf := float64(jitter)
						randomJitter := time.Duration(jf * (2*rand.Float64() - 1))
						d += randomJitter
						if d < 0 {
							d = 0
						}
					}
					if d > waitDuration {
						waitDuration = d
					}
				}

				if waitDuration > 0 {
					select {
					case <-ctx.Done():
						return
					case <-time.After(waitDuration):
						now = time.Now()
					}
				}
			}

			lastStart = now
			select {
			case <-ctx.Done():
				return
			case tasks <- engine.Task{Index: i + 1, Command: execStr, Timestamp: time.Now()}:
			}
		}
	}()
	return tasks
}

func (s *Server) resolveExec(exec interface{}) (string, error) {
	if exec == nil {
		return "", nil
	}
	switch v := exec.(type) {
	case string:
		return v, nil
	case map[string]interface{}:
		return s.parseStructuredExec(v)
	default:
		return "", fmt.Errorf("invalid 'exec' type: %T", exec)
	}
}

func (s *Server) parseStructuredExec(m map[string]interface{}) (string, error) {
	client, _ := m["client"].(string)
	if client == "" {
		// If no client field, check if it's a single key map like {".http": {...}}
		for k, v := range m {
			if strings.HasPrefix(k, ".") {
				client = k
				if opts, ok := v.(map[string]interface{}); ok {
					m = opts
				}
				break
			}
		}
	}

	if client == "" {
		return "", fmt.Errorf("structured exec missing 'client' field or shorthand key")
	}

	var sb strings.Builder
	sb.WriteString(client)

	// Special handling for common built-ins to maintain expected positional order
	if client == ".http" {
		method, _ := m["method"].(string)
		url, _ := m["url"].(string)
		if method != "" {
			sb.WriteString(" ")
			sb.WriteString(method)
		}
		if url != "" {
			sb.WriteString(" ")
			sb.WriteString(s.quoteArg(url))
		}
	}

	// Handle explicit positional args
	if args, ok := m["args"].([]interface{}); ok {
		for _, arg := range args {
			sb.WriteString(" ")
			sb.WriteString(s.quoteArg(fmt.Sprint(arg)))
		}
	}

	// Handle flags (generic)
	var keys []string
	flags, ok := m["flags"].(map[string]interface{})
	if ok {
		for k := range flags {
			keys = append(keys, k)
		}
		sort.Strings(keys) // Deterministic order
		for _, k := range keys {
			v := flags[k]
			s.appendFlag(&sb, k, v)
		}
	}

	// Handle top-level convenience keys if not already in flags
	// convenienceAliases maps user-facing key names to the actual flag name passed to the client.
	// Supports both singular and plural forms for common flags.
	convenienceAliases := []struct{ key, flag string }{
		{"body", "body"},
		{"header", "header"},
		{"headers", "header"}, // plural alias
		{"output", "output"},
	}
	for _, alias := range convenienceAliases {
		// Skip if already covered by explicit flags block
		if _, inFlags := flags[alias.key]; inFlags {
			continue
		}
		if _, inFlags := flags[alias.flag]; inFlags {
			continue
		}
		if v, ok := m[alias.key]; ok {
			s.appendFlag(&sb, alias.flag, v)
		}
	}

	return sb.String(), nil
}

func (s *Server) appendFlag(sb *strings.Builder, key string, val interface{}) {
	flagName := key
	if len(flagName) == 1 {
		sb.WriteString(" -")
	} else {
		sb.WriteString(" --")
	}
	sb.WriteString(flagName)
	sb.WriteString(" ")

	switch v := val.(type) {
	case map[string]interface{}:
		// JSON body: marshal first, then restore any \" inside {{...}} template expressions
		// that json.Marshal re-escaped. The template engine needs raw " inside {{ }}.
		b, _ := json.Marshal(v)
		sb.WriteString(s.quoteArg(unescapeTemplateExprs(string(b))))
	case []interface{}:
		// Repeatable flags (like headers)
		for i, item := range v {
			if i > 0 {
				if len(flagName) == 1 {
					sb.WriteString(" -")
				} else {
					sb.WriteString(" --")
				}
				sb.WriteString(flagName)
				sb.WriteString(" ")
			}
			// Also unescape template expressions inside header strings
			sb.WriteString(s.quoteArg(unescapeTemplateExprs(fmt.Sprint(item))))
		}
	default:
		sb.WriteString(s.quoteArg(unescapeTemplateExprs(fmt.Sprint(v))))
	}
}

// makeTemplateExprsJSONSafe scans raw JSON bytes and replaces bare (unquoted)
// Go template expressions like {{randInt 1 100}} with placeholder strings so
// the JSON can be parsed normally. Returns the modified bytes and a map of
// placeholder→original expression to restore later.
func makeTemplateExprsJSONSafe(data []byte) ([]byte, map[string]string) {
	placeholders := make(map[string]string)
	var result bytes.Buffer
	n := 0
	inStr := false
	escaped := false

	i := 0
	for i < len(data) {
		b := data[i]

		if escaped {
			result.WriteByte(b)
			escaped = false
			i++
			continue
		}

		if b == '\\' && inStr {
			escaped = true
			result.WriteByte(b)
			i++
			continue
		}

		if b == '"' {
			inStr = !inStr
			result.WriteByte(b)
			i++
			continue
		}

		// When NOT inside a JSON string, look for {{ template expressions
		if !inStr && i+1 < len(data) && b == '{' && data[i+1] == '{' {
			end := bytes.Index(data[i:], []byte("}}"))
			if end == -1 {
				result.Write(data[i:])
				break
			}
			end += i + 2
			expr := string(data[i:end])
			placeholder := fmt.Sprintf("\"__COXEC_TMPL_%d__\"", n)
			placeholders[fmt.Sprintf("__COXEC_TMPL_%d__", n)] = expr
			n++
			result.WriteString(placeholder)
			i = end
			continue
		}

		result.WriteByte(b)
		i++
	}

	return result.Bytes(), placeholders
}

// unescapeTemplateExprs finds all {{...}} template expressions in s and
// replaces \" with " inside them. This is needed because json.Marshal
// escapes the quotes inside template expressions like {{.Var "key"}},
// turning them into {{.Var \"key\"}} which are invalid Go template syntax.
func unescapeTemplateExprs(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '{' && s[i+1] == '{' {
			end := strings.Index(s[i:], "}}")
			if end == -1 {
				result.WriteString(s[i:])
				break
			}
			end += i + 2
			expr := s[i:end]
			result.WriteString(strings.ReplaceAll(expr, `\"`, `"`))
			i = end
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}


func (s *Server) quoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	// If it doesn't contain shell-sensitive chars, no need to quote
	if !strings.ContainsAny(arg, " \t\n\r'\"\\|><&;()$") {
		return arg
	}
	// Wrap in single quotes and escape internal single quotes: ' -> '\''
	escaped := strings.ReplaceAll(arg, "'", "'\\''")
	return "'" + escaped + "'"
}

func (s *Server) formatReportText(report *engine.ExecutionReport) string {
	var sb strings.Builder

	// Add aggregate stdout
	for _, line := range report.Stdout {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	// Add aggregate stderr (includes summary)
	for _, line := range report.Stderr {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	return sb.String()
}
