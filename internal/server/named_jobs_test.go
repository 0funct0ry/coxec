package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/0funct0ry/coxec/internal/config"
	"github.com/0funct0ry/coxec/internal/engine"
)

func TestNamedJobTriggering(t *testing.T) {
	registry := engine.NewBuiltinRegistry()
	
	namedJobs := []config.NamedJobConfig{
		{
			Name:        "test-echo",
			Exec:        "echo hello-{{.Vars.name}}",
			Concurrency: 1,
			Iterations:  1,
			Vars: map[string]string{
				"name": "world",
			},
		},
		{
			Name:        "test-long",
			Exec:        ".sleep 100ms",
			Concurrency: 1,
			Iterations:  2,
		},
	}

	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, false, false, 0, 0, namedJobs)
	s.Status = StatusReady

	t.Run("TriggerKnownJob", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/jobs/test-echo/run", nil)
		rr := httptest.NewRecorder()
		s.handleJobsPath(rr, req)

		if rr.Code != http.StatusAccepted {
			t.Errorf("expected status 202, got %d", rr.Code)
		}

		var resp map[string]string
		json.Unmarshal(rr.Body.Bytes(), &resp)
		jobID := resp["job_id"]
		if jobID == "" {
			t.Fatal("expected job_id in response")
		}

		// Verify job state
		job, ok := s.JobStore.Get(jobID)
		if !ok {
			t.Errorf("job %s not found in store", jobID)
		}
		if job.Request.Exec != "echo hello-{{.Vars.name}}" {
			t.Errorf("expected exec 'echo hello-{{.Vars.name}}', got '%v'", job.Request.Exec)
		}
	})

	t.Run("TriggerWithOverrides", func(t *testing.T) {
		override := ExecRequest{
			Concurrency: 2,
			Label:       "overridden-job",
			Vars: map[string]string{
				"name": "gemini",
			},
		}
		body, _ := json.Marshal(override)
		req := httptest.NewRequest("POST", "/jobs/test-echo/run", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		s.handleJobsPath(rr, req)

		if rr.Code != http.StatusAccepted {
			t.Errorf("expected status 202, got %d", rr.Code)
		}

		var resp map[string]string
		json.Unmarshal(rr.Body.Bytes(), &resp)
		jobID := resp["job_id"]

		job, _ := s.JobStore.Get(jobID)
		if job.Request.Concurrency != 2 {
			t.Errorf("expected concurrency 2, got %d", job.Request.Concurrency)
		}
		if job.Request.Label != "overridden-job" {
			t.Errorf("expected label 'overridden-job', got '%s'", job.Request.Label)
		}
		if job.Request.Vars["name"] != "gemini" {
			t.Errorf("expected var 'name' to be 'gemini', got '%s'", job.Request.Vars["name"])
		}
	})

	t.Run("TriggerUnknownJob", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/jobs/nonexistent/run", nil)
		rr := httptest.NewRecorder()
		s.handleJobsPath(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", rr.Code)
		}
	})

	t.Run("InvalidMethod", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/jobs/test-echo/run", nil)
		rr := httptest.NewRecorder()
		s.handleJobsPath(rr, req)

		// /jobs/:name/run only supports POST. If it's GET, handleJobsPath passes it to JobsHandler, which will likely 404 since it's not a job ID
		if rr.Code != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", rr.Code)
		}
	})

	t.Run("TriggerIdempotent", func(t *testing.T) {
		key := "named-idempotency-key"
		
		// First request
		req1 := httptest.NewRequest("POST", "/jobs/test-echo/run", nil)
		req1.Header.Set("Idempotency-Key", key)
		rr1 := httptest.NewRecorder()
		s.handleJobsPath(rr1, req1)
		
		if rr1.Code != http.StatusAccepted {
			t.Errorf("expected status 202 for first request, got %d", rr1.Code)
		}
		
		var resp1 map[string]string
		json.Unmarshal(rr1.Body.Bytes(), &resp1)
		id1 := resp1["job_id"]

		// Second request (retry)
		req2 := httptest.NewRequest("POST", "/jobs/test-echo/run", nil)
		req2.Header.Set("Idempotency-Key", key)
		rr2 := httptest.NewRecorder()
		s.handleJobsPath(rr2, req2)
		
		if rr2.Code != http.StatusOK {
			t.Errorf("expected status 200 for idempotent retry, got %d", rr2.Code)
		}
		
		var resp2 map[string]string
		json.Unmarshal(rr2.Body.Bytes(), &resp2)
		id2 := resp2["job_id"]

		if id1 != id2 {
			t.Errorf("expected same job_id for same idempotency key, got %s and %s", id1, id2)
		}
	})
}
