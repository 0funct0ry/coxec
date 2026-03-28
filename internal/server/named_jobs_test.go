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

	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, namedJobs)
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
}
