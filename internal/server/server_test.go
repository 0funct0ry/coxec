package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"context"

	"github.com/0funct0ry/coxec/internal/engine"
)

func TestHealthCheck(t *testing.T) {
	s := NewServer("127.0.0.1", 8080, "1.0.0", "", "", "", "", "", engine.NewBuiltinRegistry(), 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour)
	
	t.Run("StatusStarting", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		rr := httptest.NewRecorder()
		
		s.HealthHandler(rr, req)
		
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status 503, got %d", rr.Code)
		}
		
		var resp map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp["status"] != "starting" {
			t.Errorf("expected status 'starting', got '%s'", resp["status"])
		}
	})

	t.Run("StatusReady", func(t *testing.T) {
		s.Status = StatusReady
		s.ActiveJobs.Store(2)
		s.StartTime = time.Now().Add(-3600 * time.Second)
		
		req := httptest.NewRequest("GET", "/health", nil)
		rr := httptest.NewRecorder()
		
		s.HealthHandler(rr, req)
		
		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}
		
		var resp map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		
		if resp["status"] != "ok" {
			t.Errorf("expected status 'ok', got '%s'", resp["status"])
		}
		if resp["version"] != "1.0.0" {
			t.Errorf("expected version '1.0.0', got '%s'", resp["version"])
		}
		if int64(resp["active_jobs"].(float64)) != 2 {
			t.Errorf("expected active_jobs 2, got %v", resp["active_jobs"])
		}
		if int64(resp["uptime_seconds"].(float64)) < 3600 {
			t.Errorf("expected uptime >= 3600, got %v", resp["uptime_seconds"])
		}
	})

	t.Run("StatusShuttingDown", func(t *testing.T) {
		s.Status = StatusShuttingDown
		
		req := httptest.NewRequest("GET", "/health", nil)
		rr := httptest.NewRecorder()
		
		s.HealthHandler(rr, req)
		
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status 503, got %d", rr.Code)
		}
		
		var resp map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp["status"] != "shutting_down" {
			t.Errorf("expected status 'shutting_down', got '%s'", resp["status"])
		}
	})
}

func TestExecHandler(t *testing.T) {
	registry := engine.NewBuiltinRegistry()
	registry.Register(engine.NewSleepClient())
	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 0, 0, 0, true, NewInMemoryJobStore(), 24*time.Hour)
	s.Status = StatusReady

	t.Run("ValidRequest", func(t *testing.T) {
		payload := ExecRequest{
			Exec:        "echo hello",
			Concurrency: 1,
			Iterations:  1,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/exec", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		rr := httptest.NewRecorder()

		s.ExecHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}

		var resp ExecResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Status != "ok" {
			t.Errorf("expected status 'ok', got '%s'", resp.Status)
		}
		if resp.Report.TotalExecutions != 1 {
			t.Errorf("expected 1 execution, got %d", resp.Report.TotalExecutions)
		}
		if len(resp.Report.Stdout) == 0 || !strings.Contains(resp.Report.Stdout[0], "hello") {
			t.Errorf("expected stdout to contain 'hello', got %v", resp.Report.Stdout)
		}
	})

	t.Run("MissingExec", func(t *testing.T) {
		payload := ExecRequest{
			Concurrency: 1,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/exec", bytes.NewBuffer(body))
		rr := httptest.NewRecorder()

		s.ExecHandler(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", rr.Code)
		}
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/exec", strings.NewReader("invalid json"))
		rr := httptest.NewRecorder()

		s.ExecHandler(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", rr.Code)
		}
	})

	t.Run("ServerNotReady", func(t *testing.T) {
		s.Status = StatusStarting
		payload := ExecRequest{Exec: "echo 1"}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/exec", bytes.NewBuffer(body))
		rr := httptest.NewRecorder()

		s.ExecHandler(rr, req)

		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("expected status 503, got %d", rr.Code)
		}
		s.Status = StatusReady // Restore for other tests
	})

	t.Run("VerboseRequest", func(t *testing.T) {
		payload := ExecRequest{
			Exec:        ".sleep 1ms",
			Concurrency: 1,
			Iterations:  2,
			Verbose:     true,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/exec", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		rr := httptest.NewRecorder()

		s.ExecHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}

		var resp ExecResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if len(resp.Report.Details) != 2 {
			t.Errorf("expected 2 execution details, got %d", len(resp.Report.Details))
		}
		for i, detail := range resp.Report.Details {
			if detail.Index != i+1 {
				t.Errorf("detail %d: expected index %d, got %d", i, i+1, detail.Index)
			}
			if detail.Status != "success" {
				t.Errorf("detail %d: expected status success, got %s", i, detail.Status)
			}
			if detail.Duration == "" {
				t.Errorf("detail %d: expected non-empty duration", i)
			}
		}
	})

	t.Run("FormEncodedRequest", func(t *testing.T) {
		form := url.Values{}
		form.Add("exec", "echo hello")
		form.Add("iterations", "1")
		req := httptest.NewRequest("POST", "/exec", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()

		s.ExecHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "hello") {
			t.Errorf("expected body to contain 'hello', got %q", rr.Body.String())
		}
		if rr.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("expected content-type text/plain, got %s", rr.Header().Get("Content-Type"))
		}
	})

	t.Run("PlainTextResponseByDefault", func(t *testing.T) {
		payload := ExecRequest{Exec: "echo hello", Iterations: 1}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/exec", bytes.NewBuffer(body))
		// No Accept header, and no Content-Type on request (Wait, json.Marshal above doesn't set it)
		rr := httptest.NewRecorder()

		s.ExecHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}
		if rr.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("expected content-type text/plain, got %s", rr.Header().Get("Content-Type"))
		}
	})

	t.Run("JSONResponseIfExplicitlyRequested", func(t *testing.T) {
		form := url.Values{}
		form.Add("exec", "echo hello")
		req := httptest.NewRequest("POST", "/exec", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		rr := httptest.NewRecorder()

		s.ExecHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}
		if rr.Header().Get("Content-Type") != "application/json" {
			t.Errorf("expected content-type application/json, got %s", rr.Header().Get("Content-Type"))
		}
	})

	t.Run("StructuredExecRequest", func(t *testing.T) {
		payload := map[string]interface{}{
			"exec": map[string]interface{}{
				"client": ".http",
				"method": "POST",
				"url":    "http://localhost:9090/post",
				"body": map[string]interface{}{
					"id": "123",
				},
			},
			"iterations": 1,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/exec", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		rr := httptest.NewRecorder()

		s.ExecHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}

		var resp ExecResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Status != "ok" {
			t.Errorf("expected status 'ok', got '%s'", resp.Status)
		}
	})

	t.Run("ConcurrencyAndIterationsOverrides", func(t *testing.T) {
		// Server defaults are 1, 1
		payload := ExecRequest{
			Exec:        "echo hello",
			Concurrency: 2,
			Iterations:  4,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/exec", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		rr := httptest.NewRecorder()

		s.ExecHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}

		var resp ExecResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Report.TotalExecutions != 4 {
			t.Errorf("expected 4 executions, got %d", resp.Report.TotalExecutions)
		}
	})

	t.Run("ValidationFailure", func(t *testing.T) {
		tests := []struct {
			name        string
			concurrency int
			iterations  int
			expectedMsg string
		}{
			{"TooManyConcurrent", 1001, 1, "concurrency exceeds maximum allowed (1000)"},
			{"TooManyIterations", 1, 10000001, "iterations exceed maximum allowed (10,000,000)"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				payload := ExecRequest{
					Exec:        "echo hello",
					Concurrency: tt.concurrency,
					Iterations:  tt.iterations,
				}
				body, _ := json.Marshal(payload)
				req := httptest.NewRequest("POST", "/exec", bytes.NewBuffer(body))
				req.Header.Set("Content-Type", "application/json")
				rr := httptest.NewRecorder()

				s.ExecHandler(rr, req)

				if rr.Code != http.StatusBadRequest {
					t.Errorf("expected status 400, got %d", rr.Code)
				}
				var resp ExecResponse
				_ = json.Unmarshal(rr.Body.Bytes(), &resp)
				if !strings.Contains(resp.Error, tt.expectedMsg) {
					t.Errorf("expected error containing %q, got %q", tt.expectedMsg, resp.Error)
				}
			})
		}
	})
}

func TestExecHandlerWithAuth(t *testing.T) {
	registry := engine.NewBuiltinRegistry()
	s := NewServer("127.0.0.1", 8080, "1.0.0", "super-secret", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour)
	s.Status = StatusReady

	validPayload := ExecRequest{
		Exec:        "echo hello",
		Concurrency: 1,
		Iterations:  1,
	}
	bodyBytes, _ := json.Marshal(validPayload)

	makeReq := func(authHeader string) *http.Request {
		req := httptest.NewRequest("POST", "/exec", bytes.NewBuffer(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		return req
	}

	t.Run("MissingToken", func(t *testing.T) {
		req := makeReq("")
		rr := httptest.NewRecorder()
		s.ExecHandler(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
	})

	t.Run("MalformedToken", func(t *testing.T) {
		req := makeReq("super-secret") // missing Bearer prefix
		rr := httptest.NewRecorder()
		s.ExecHandler(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
	})

	t.Run("IncorrectToken", func(t *testing.T) {
		req := makeReq("Bearer wrong-token")
		rr := httptest.NewRecorder()
		s.ExecHandler(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
	})

	t.Run("CorrectToken", func(t *testing.T) {
		req := makeReq("Bearer super-secret")
		rr := httptest.NewRecorder()
		s.ExecHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}
	})
}

func TestExecHandlerWithBasicAuth(t *testing.T) {
	registry := engine.NewBuiltinRegistry()
	s := NewServer("127.0.0.1", 8080, "1.0.0", "", "admin:secret", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour)
	s.Status = StatusReady

	validPayload := ExecRequest{
		Exec:        "echo hello",
		Concurrency: 1,
		Iterations:  1,
	}
	bodyBytes, _ := json.Marshal(validPayload)

	makeReq := func(authHeader string) *http.Request {
		req := httptest.NewRequest("POST", "/exec", bytes.NewBuffer(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		return req
	}

	t.Run("MissingCredentials", func(t *testing.T) {
		req := makeReq("")
		rr := httptest.NewRecorder()
		s.ExecHandler(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
		if rr.Header().Get("WWW-Authenticate") != `Basic realm="Restricted"` {
			t.Errorf("expected WWW-Authenticate header, got %q", rr.Header().Get("WWW-Authenticate"))
		}
	})

	t.Run("MalformedCredentials", func(t *testing.T) {
		req := makeReq("Basic notbase64!!!")
		rr := httptest.NewRecorder()
		s.ExecHandler(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
		if rr.Header().Get("WWW-Authenticate") != `Basic realm="Restricted"` {
			t.Errorf("expected WWW-Authenticate header, got %q", rr.Header().Get("WWW-Authenticate"))
		}
	})

	t.Run("IncorrectCredentials", func(t *testing.T) {
		// wrong:password base64 = d3Jvbmc6cGFzc3dvcmQ=
		req := makeReq("Basic d3Jvbmc6cGFzc3dvcmQ=")
		rr := httptest.NewRecorder()
		s.ExecHandler(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
	})

	t.Run("CorrectCredentials", func(t *testing.T) {
		// admin:secret base64 = YWRtaW46c2VjcmV0
		req := makeReq("Basic YWRtaW46c2VjcmV0")
		rr := httptest.NewRecorder()
		s.ExecHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}
	})
}

func TestExecHandlerWithHmac(t *testing.T) {
	registry := engine.NewBuiltinRegistry()
	s := NewServer("127.0.0.1", 8080, "1.0.0", "", "", "hmac-secret", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour)
	s.Status = StatusReady

	validPayload := ExecRequest{
		Exec:        "echo hello",
		Concurrency: 1,
		Iterations:  1,
	}
	bodyBytes, _ := json.Marshal(validPayload)

	makeReq := func(sig string) *http.Request {
		req := httptest.NewRequest("POST", "/exec", bytes.NewBuffer(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		if sig != "" {
			req.Header.Set("X-Signature", sig)
		}
		return req
	}

	t.Run("MissingSignature", func(t *testing.T) {
		req := makeReq("")
		rr := httptest.NewRecorder()
		s.ExecHandler(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
	})

	t.Run("MalformedSignatureMissingPrefix", func(t *testing.T) {
		req := makeReq("1234abcd")
		rr := httptest.NewRecorder()
		s.ExecHandler(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
	})

	t.Run("MalformedSignatureNotHex", func(t *testing.T) {
		req := makeReq("sha256=nothex")
		rr := httptest.NewRecorder()
		s.ExecHandler(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
	})

	t.Run("IncorrectSignature", func(t *testing.T) {
		req := makeReq("sha256=" + "1234abcd")
		rr := httptest.NewRecorder()
		s.ExecHandler(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rr.Code)
		}
	})

	t.Run("CorrectSignature", func(t *testing.T) {
		mac := hmac.New(sha256.New, []byte("hmac-secret"))
		mac.Write(bodyBytes)
		sig := hex.EncodeToString(mac.Sum(nil))

		req := makeReq("sha256=" + sig)
		rr := httptest.NewRecorder()
		s.ExecHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rr.Code)
		}
	})
}

func TestStartTLS_MissingFiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "nonexistent.cert", "nonexistent.key", engine.NewBuiltinRegistry(), 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour)
	err := s.Start(ctx)

	if err == nil {
		t.Error("expected error when starting server with missing TLS files, got nil")
	}
	if !strings.Contains(err.Error(), "open nonexistent.cert") && !strings.Contains(err.Error(), "no such file") {
		t.Errorf("expected file not found error, got: %v", err)
	}
}

func TestAsyncExecHandler(t *testing.T) {
	registry := engine.NewBuiltinRegistry()
	registry.Register(engine.NewSleepClient())
	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour)
	s.Status = StatusReady

	t.Run("ValidAsyncRequest", func(t *testing.T) {
		payload := ExecRequest{
			Exec:        ".sleep 10ms",
			Concurrency: 1,
			Iterations:  1,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/async/exec", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()

		s.AsyncExecHandler(rr, req)

		if rr.Code != http.StatusAccepted {
			t.Errorf("expected status 202, got %d", rr.Code)
		}

		var resp map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		jobID := resp["job_id"]
		if jobID == "" {
			t.Error("expected job_id in response")
		}

		// Verify job exists in store
		job, ok := s.JobStore.Get(jobID)
		if !ok {
			t.Errorf("expected job %s to exist in store", jobID)
		}
		if job.Status != JobStatusQueued && job.Status != JobStatusRunning {
			t.Errorf("unexpected job status: %s", job.Status)
		}
	})

	t.Run("Idempotency", func(t *testing.T) {
		payload := ExecRequest{Exec: "echo hello"}
		body, _ := json.Marshal(payload)
		
		key := "test-key-123"
		
		req1 := httptest.NewRequest("POST", "/async/exec", bytes.NewBuffer(body))
		req1.Header.Set("Content-Type", "application/json")
		req1.Header.Set("Idempotency-Key", key)
		rr1 := httptest.NewRecorder()
		s.AsyncExecHandler(rr1, req1)
		
		var resp1 map[string]string
		json.Unmarshal(rr1.Body.Bytes(), &resp1)
		id1 := resp1["job_id"]

		req2 := httptest.NewRequest("POST", "/async/exec", bytes.NewBuffer(body))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("Idempotency-Key", key)
		rr2 := httptest.NewRecorder()
		s.AsyncExecHandler(rr2, req2)
		
		var resp2 map[string]string
		json.Unmarshal(rr2.Body.Bytes(), &resp2)
		id2 := resp2["job_id"]

		if id1 != id2 {
			t.Errorf("expected same job_id for same idempotency key, got %s and %s", id1, id2)
		}
		if rr2.Code != http.StatusOK {
			t.Errorf("expected 200 OK for idempotent retry, got %d", rr2.Code)
		}
	})
}

func TestJobLifecycle(t *testing.T) {
	registry := engine.NewBuiltinRegistry()
	registry.Register(engine.NewSleepClient())
	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour)
	s.Status = StatusReady

	t.Run("FullFlow_Completed", func(t *testing.T) {
		payload := ExecRequest{
			Exec:        "echo hello",
			Concurrency: 1,
			Iterations:  1,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/async/exec", bytes.NewBuffer(body))
		rr := httptest.NewRecorder()
		s.AsyncExecHandler(rr, req)

		var resp map[string]string
		json.Unmarshal(rr.Body.Bytes(), &resp)
		jobID := resp["job_id"]

		// Polling for completion
		for i := 0; i < 20; i++ {
			reqGet := httptest.NewRequest("GET", "/jobs/"+jobID, nil)
			rrGet := httptest.NewRecorder()
			s.JobsHandler(rrGet, reqGet)
			
			var job Job
			json.Unmarshal(rrGet.Body.Bytes(), &job)
			if job.Status == JobStatusCompleted {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}

		reqFinal := httptest.NewRequest("GET", "/jobs/"+jobID, nil)
		rrFinal := httptest.NewRecorder()
		s.JobsHandler(rrFinal, reqFinal)
		
		var finalJob Job
		json.Unmarshal(rrFinal.Body.Bytes(), &finalJob)
		if finalJob.Status != JobStatusCompleted {
			t.Errorf("expected status completed, got %s", finalJob.Status)
		}
		if finalJob.Report == nil {
			t.Error("expected report to be populated")
		}
	})

	t.Run("Cancellation", func(t *testing.T) {
		payload := ExecRequest{
			Exec:        ".sleep 1s",
			Concurrency: 1,
			Iterations:  1,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/async/exec", bytes.NewBuffer(body))
		rr := httptest.NewRecorder()
		s.AsyncExecHandler(rr, req)

		var resp map[string]string
		json.Unmarshal(rr.Body.Bytes(), &resp)
		jobID := resp["job_id"]

		// Wait a bit for it to start
		time.Sleep(50 * time.Millisecond)

		// Cancel
		reqDel := httptest.NewRequest("DELETE", "/jobs/"+jobID, nil)
		rrDel := httptest.NewRecorder()
		s.JobsHandler(rrDel, reqDel)

		if rrDel.Code != http.StatusAccepted {
			t.Errorf("expected 202 Accepted, got %d", rrDel.Code)
		}

		// Verify status is cancelled
		reqGet := httptest.NewRequest("GET", "/jobs/"+jobID, nil)
		rrGet := httptest.NewRecorder()
		s.JobsHandler(rrGet, reqGet)
		
		var job Job
		json.Unmarshal(rrGet.Body.Bytes(), &job)
		if job.Status != JobStatusCancelled {
			t.Errorf("expected status cancelled, got %s", job.Status)
		}
	})
}

func TestJobCleanup(t *testing.T) {
	store := NewInMemoryJobStore()
	now := time.Now()
	
	// Completed job, 2 hours old
	completedAt := now.Add(-2 * time.Hour)
	job1 := &Job{
		ID:          "job1",
		Status:      JobStatusCompleted,
		CreatedAt:   now.Add(-3 * time.Hour),
		CompletedAt: &completedAt,
	}
	
	// Completed job, 30 mins old
	completedAt2 := now.Add(-30 * time.Minute)
	job2 := &Job{
		ID:          "job2",
		Status:      JobStatusCompleted,
		CreatedAt:   now.Add(-1 * time.Hour),
		CompletedAt: &completedAt2,
	}
	
	// Running job, 2 hours old (should NOT be cleaned up)
	job3 := &Job{
		ID:        "job3",
		Status:    JobStatusRunning,
		CreatedAt: now.Add(-2 * time.Hour),
	}
	
	store.Create(job1)
	store.Create(job2)
	store.Create(job3)
	
	// Cleanup jobs older than 1 hour
	count, _ := store.Cleanup(1 * time.Hour)
	if count != 1 {
		t.Errorf("expected 1 job cleaned up, got %d", count)
	}
	
	if _, ok := store.Get("job1"); ok {
		t.Error("job1 should have been cleaned up")
	}
	if _, ok := store.Get("job2"); !ok {
		t.Error("job2 should NOT have been cleaned up")
	}
	if _, ok := store.Get("job3"); !ok {
		t.Error("job3 should NOT have been cleaned up")
	}
}
