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
	s := NewServer("127.0.0.1", 8080, "1.0.0", "", "", "", "", "", engine.NewBuiltinRegistry(), 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
	
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
		if resp["job_store"] != "memory" {
			t.Errorf("expected job_store 'memory', got '%s'", resp["job_store"])
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
	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 0, 0, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
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
	s := NewServer("127.0.0.1", 8080, "1.0.0", "super-secret", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
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
	s := NewServer("127.0.0.1", 8080, "1.0.0", "", "admin:secret", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
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
	s := NewServer("127.0.0.1", 8080, "1.0.0", "", "", "hmac-secret", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
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

	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "nonexistent.cert", "nonexistent.key", engine.NewBuiltinRegistry(), 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
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
	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
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
	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
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

			var detail JobDetailResponse
			json.Unmarshal(rrGet.Body.Bytes(), &detail)
			if detail.State == JobStatusCompleted {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}

		reqFinal := httptest.NewRequest("GET", "/jobs/"+jobID, nil)
		rrFinal := httptest.NewRecorder()
		s.JobsHandler(rrFinal, reqFinal)

		var finalDetail JobDetailResponse
		json.Unmarshal(rrFinal.Body.Bytes(), &finalDetail)
		if finalDetail.State != JobStatusCompleted {
			t.Errorf("expected status completed, got %s", finalDetail.State)
		}
		if finalDetail.Summary == nil {
			t.Error("expected summary to be populated for completed job")
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

		var detail JobDetailResponse
		json.Unmarshal(rrGet.Body.Bytes(), &detail)
		if detail.State != JobStatusCancelled {
			t.Errorf("expected status cancelled, got %s", detail.State)
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
	count, _ := store.Prune(0, 1 * time.Hour)
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

func TestListJobsHandler(t *testing.T) {
	newServer := func(authToken string) *Server {
		s := NewServer("127.0.0.1", 0, "1.0.0", authToken, "", "", "", "", engine.NewBuiltinRegistry(), 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
		s.Status = StatusReady
		return s
	}

	makeGetReq := func(query string) *http.Request {
		return httptest.NewRequest(http.MethodGet, "/jobs"+query, nil)
	}

	t.Run("EmptyStore", func(t *testing.T) {
		s := newServer("")
		rr := httptest.NewRecorder()
		s.ListJobsHandler(rr, makeGetReq(""))

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
		var resp ListJobsResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.Jobs) != 0 {
			t.Errorf("expected 0 jobs, got %d", len(resp.Jobs))
		}
		if resp.Total != 0 {
			t.Errorf("expected total=0, got %d", resp.Total)
		}
		if resp.Limit != 50 {
			t.Errorf("expected default limit=50, got %d", resp.Limit)
		}
		if resp.Offset != 0 {
			t.Errorf("expected default offset=0, got %d", resp.Offset)
		}
	})

	t.Run("SortedNewestFirst", func(t *testing.T) {
		s := newServer("")
		now := time.Now()

		jobs := []*Job{
			{ID: "old", Status: JobStatusCompleted, Request: ExecRequest{Exec: "echo old"}, CreatedAt: now.Add(-2 * time.Hour)},
			{ID: "mid", Status: JobStatusCompleted, Request: ExecRequest{Exec: "echo mid"}, CreatedAt: now.Add(-1 * time.Hour)},
			{ID: "new", Status: JobStatusCompleted, Request: ExecRequest{Exec: "echo new"}, CreatedAt: now},
		}
		for _, j := range jobs {
			_ = s.JobStore.Create(j)
		}

		rr := httptest.NewRecorder()
		s.ListJobsHandler(rr, makeGetReq(""))

		var resp ListJobsResponse
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)

		if len(resp.Jobs) != 3 {
			t.Fatalf("expected 3 jobs, got %d", len(resp.Jobs))
		}
		if resp.Jobs[0].ID != "new" {
			t.Errorf("expected newest job first, got %s", resp.Jobs[0].ID)
		}
		if resp.Jobs[1].ID != "mid" {
			t.Errorf("expected mid job second, got %s", resp.Jobs[1].ID)
		}
		if resp.Jobs[2].ID != "old" {
			t.Errorf("expected oldest job last, got %s", resp.Jobs[2].ID)
		}
	})

	t.Run("PaginationLimitOffset", func(t *testing.T) {
		s := newServer("")
		now := time.Now()

		for i := 0; i < 5; i++ {
			j := &Job{
				ID:        string(rune('a' + i)),
				Status:    JobStatusCompleted,
				Request:   ExecRequest{Exec: "echo"},
				CreatedAt: now.Add(time.Duration(i) * time.Minute),
			}
			_ = s.JobStore.Create(j)
		}

		// limit=2, offset=1 → second and third newest
		rr := httptest.NewRecorder()
		s.ListJobsHandler(rr, makeGetReq("?limit=2&offset=1"))

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		var resp ListJobsResponse
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)

		if resp.Total != 5 {
			t.Errorf("expected total=5, got %d", resp.Total)
		}
		if len(resp.Jobs) != 2 {
			t.Errorf("expected 2 jobs in page, got %d", len(resp.Jobs))
		}
		if resp.Limit != 2 {
			t.Errorf("expected limit=2, got %d", resp.Limit)
		}
		if resp.Offset != 1 {
			t.Errorf("expected offset=1, got %d", resp.Offset)
		}
	})

	t.Run("OffsetPastEnd", func(t *testing.T) {
		s := newServer("")
		j := &Job{ID: "x", Status: JobStatusCompleted, Request: ExecRequest{Exec: "echo"}, CreatedAt: time.Now()}
		_ = s.JobStore.Create(j)

		rr := httptest.NewRecorder()
		s.ListJobsHandler(rr, makeGetReq("?offset=100"))

		var resp ListJobsResponse
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)

		if len(resp.Jobs) != 0 {
			t.Errorf("expected 0 jobs when offset past end, got %d", len(resp.Jobs))
		}
		if resp.Total != 1 {
			t.Errorf("expected total=1 even when offset past end, got %d", resp.Total)
		}
	})

	t.Run("TTLFiltering_ExpiredTerminalExcluded_ActiveAlwaysIncluded", func(t *testing.T) {
		// Server with 1-hour TTL
		s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", engine.NewBuiltinRegistry(), 1, 1, 0, true, NewInMemoryJobStore(), 1*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
		s.Status = StatusReady

		now := time.Now()
		expiredAt := now.Add(-2 * time.Hour) // 2 hours ago → beyond 1-hour TTL

		expiredJob := &Job{
			ID:          "expired",
			Status:      JobStatusCompleted,
			Request:     ExecRequest{Exec: "echo expired"},
			CreatedAt:   now.Add(-3 * time.Hour),
			CompletedAt: &expiredAt,
		}
		freshAt := now.Add(-30 * time.Minute)
		freshJob := &Job{
			ID:          "fresh",
			Status:      JobStatusCompleted,
			Request:     ExecRequest{Exec: "echo fresh"},
			CreatedAt:   now.Add(-1 * time.Hour),
			CompletedAt: &freshAt,
		}
		runningJob := &Job{
			ID:        "running",
			Status:    JobStatusRunning,
			Request:   ExecRequest{Exec: "echo running"},
			CreatedAt: now.Add(-2 * time.Hour), // old but active → always included
		}
		queuedJob := &Job{
			ID:        "queued",
			Status:    JobStatusQueued,
			Request:   ExecRequest{Exec: "echo queued"},
			CreatedAt: now.Add(-5 * time.Hour), // very old but active → always included
		}

		for _, j := range []*Job{expiredJob, freshJob, runningJob, queuedJob} {
			_ = s.JobStore.Create(j)
		}

		rr := httptest.NewRecorder()
		s.ListJobsHandler(rr, makeGetReq(""))

		var resp ListJobsResponse
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)

		// Should see: fresh, running, queued (3). NOT expired.
		if resp.Total != 3 {
			t.Errorf("expected 3 visible jobs (TTL filtered expired), got %d", resp.Total)
		}
		idSet := make(map[string]bool)
		for _, j := range resp.Jobs {
			idSet[j.ID] = true
		}
		if idSet["expired"] {
			t.Error("expired job should have been filtered by TTL")
		}
		if !idSet["fresh"] {
			t.Error("fresh completed job should be included (within TTL)")
		}
		if !idSet["running"] {
			t.Error("running job should always be included regardless of age")
		}
		if !idSet["queued"] {
			t.Error("queued job should always be included regardless of age")
		}
	})

	t.Run("JobSummaryFields", func(t *testing.T) {
		s := newServer("")
		now := time.Now()
		startedAt := now.Add(-10 * time.Minute)
		finishedAt := now.Add(-5 * time.Minute)

		j := &Job{
			ID:          "summary-test",
			Status:      JobStatusCompleted,
			Request:     ExecRequest{Exec: "echo hello world"},
			CreatedAt:   now.Add(-15 * time.Minute),
			StartedAt:   &startedAt,
			CompletedAt: &finishedAt,
		}
		_ = s.JobStore.Create(j)

		rr := httptest.NewRecorder()
		s.ListJobsHandler(rr, makeGetReq(""))

		var resp ListJobsResponse
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)

		if len(resp.Jobs) != 1 {
			t.Fatalf("expected 1 job, got %d", len(resp.Jobs))
		}
		sum := resp.Jobs[0]
		if sum.ID != "summary-test" {
			t.Errorf("expected id=summary-test, got %s", sum.ID)
		}
		if sum.Name != "echo hello world" {
			t.Errorf("expected name='echo hello world', got %q", sum.Name)
		}
		if sum.State != JobStatusCompleted {
			t.Errorf("expected state=completed, got %s", sum.State)
		}
		if sum.StartedAt == nil {
			t.Error("expected started_at to be set")
		}
		if sum.FinishedAt == nil {
			t.Error("expected finished_at to be set")
		}
	})

	t.Run("MethodNotAllowed", func(t *testing.T) {
		s := newServer("")
		tests := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
		for _, method := range tests {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(method, "/jobs", nil)
			s.ListJobsHandler(rr, req)
			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("method %s: expected 405, got %d", method, rr.Code)
			}
		}
	})

	t.Run("AuthEnforced", func(t *testing.T) {
		s := newServer("secret-token")

		t.Run("NoToken", func(t *testing.T) {
			rr := httptest.NewRecorder()
			s.ListJobsHandler(rr, makeGetReq(""))
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", rr.Code)
			}
		})

		t.Run("WrongToken", func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := makeGetReq("")
			req.Header.Set("Authorization", "Bearer wrong-token")
			s.ListJobsHandler(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", rr.Code)
			}
		})

		t.Run("CorrectToken", func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := makeGetReq("")
			req.Header.Set("Authorization", "Bearer secret-token")
			s.ListJobsHandler(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", rr.Code)
			}
		})
	})

	t.Run("InvalidQueryParams_FallbackToDefaults", func(t *testing.T) {
		s := newServer("")
		rr := httptest.NewRecorder()
		s.ListJobsHandler(rr, makeGetReq("?limit=notanumber&offset=alsonotanumber"))

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200 even with invalid params, got %d", rr.Code)
		}
		var resp ListJobsResponse
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		if resp.Limit != 50 {
			t.Errorf("expected default limit=50 for invalid param, got %d", resp.Limit)
		}
		if resp.Offset != 0 {
			t.Errorf("expected default offset=0 for invalid param, got %d", resp.Offset)
		}
	})
}

func TestGetJobByID(t *testing.T) {
	newServer := func(authToken string) *Server {
		s := NewServer("127.0.0.1", 0, "1.0.0", authToken, "", "", "", "", engine.NewBuiltinRegistry(), 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
		s.Status = StatusReady
		return s
	}

	makeGetReq := func(id string) *http.Request {
		return httptest.NewRequest(http.MethodGet, "/jobs/"+id, nil)
	}

	t.Run("JobNotFound", func(t *testing.T) {
		s := newServer("")
		rr := httptest.NewRecorder()
		s.JobsHandler(rr, makeGetReq("nonexistent-id"))

		if rr.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rr.Code)
		}
		var resp map[string]string
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		if resp["error"] != "job not found or expired" {
			t.Errorf("expected 'job not found or expired', got %q", resp["error"])
		}
	})

	t.Run("JobQueued", func(t *testing.T) {
		s := newServer("")
		now := time.Now()
		j := &Job{
			ID:        "queued-job",
			Status:    JobStatusQueued,
			Request:   ExecRequest{Exec: "echo hello", Concurrency: 3, Iterations: 10},
			CreatedAt: now,
		}
		_ = s.JobStore.Create(j)

		rr := httptest.NewRecorder()
		s.JobsHandler(rr, makeGetReq("queued-job"))

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		var resp JobDetailResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.JobID != "queued-job" {
			t.Errorf("expected job_id=queued-job, got %s", resp.JobID)
		}
		if resp.State != JobStatusQueued {
			t.Errorf("expected state=queued, got %s", resp.State)
		}
		if resp.Concurrency != 3 {
			t.Errorf("expected concurrency=3, got %d", resp.Concurrency)
		}
		if resp.IterationsRequested != 10 {
			t.Errorf("expected iterations_requested=10, got %d", resp.IterationsRequested)
		}
		if resp.StartedAt != nil {
			t.Error("expected started_at to be nil for queued job")
		}
		if resp.Duration != "" {
			t.Errorf("expected no duration for queued job, got %q", resp.Duration)
		}
		if resp.Summary != nil {
			t.Error("expected no summary for queued job")
		}
	})

	t.Run("JobRunning", func(t *testing.T) {
		s := newServer("")
		now := time.Now()
		startedAt := now.Add(-5 * time.Second)
		j := &Job{
			ID:        "running-job",
			Status:    JobStatusRunning,
			Request:   ExecRequest{Exec: "echo hello", Concurrency: 2, Iterations: 100},
			CreatedAt: now.Add(-10 * time.Second),
			StartedAt: &startedAt,
		}
		_ = s.JobStore.Create(j)

		rr := httptest.NewRecorder()
		s.JobsHandler(rr, makeGetReq("running-job"))

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		var resp JobDetailResponse
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)

		if resp.State != JobStatusRunning {
			t.Errorf("expected state=running, got %s", resp.State)
		}
		if resp.StartedAt == nil {
			t.Error("expected started_at to be set for running job")
		}
		// No CompletedAt → no duration
		if resp.Duration != "" {
			t.Errorf("expected no duration for running job, got %q", resp.Duration)
		}
		if resp.Summary != nil {
			t.Error("expected no summary for still-running job")
		}
	})

	t.Run("JobCompleted_WithSummaryAndDuration", func(t *testing.T) {
		s := newServer("")
		now := time.Now()
		startedAt := now.Add(-30 * time.Second)
		completedAt := now.Add(-1 * time.Second)
		report := &engine.ExecutionReport{
			TotalExecutions: 5,
			SuccessCount:    4,
			FailCount:       1,
			TotalDuration:   "29s",
			AverageLatency:  "5.8s",
		}
		j := &Job{
			ID:          "completed-job",
			Status:      JobStatusCompleted,
			Request:     ExecRequest{Exec: "echo hi", Concurrency: 1, Iterations: 5, Label: "my-batch"},
			CreatedAt:   now.Add(-35 * time.Second),
			StartedAt:   &startedAt,
			CompletedAt: &completedAt,
			Report:      report,
		}
		_ = s.JobStore.Create(j)

		rr := httptest.NewRecorder()
		s.JobsHandler(rr, makeGetReq("completed-job"))

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		var resp JobDetailResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.State != JobStatusCompleted {
			t.Errorf("expected state=completed, got %s", resp.State)
		}
		if resp.Duration == "" {
			t.Error("expected duration to be set for completed job")
		}
		if resp.Label != "my-batch" {
			t.Errorf("expected label=my-batch, got %q", resp.Label)
		}
		if resp.IterationsCompleted != 5 {
			t.Errorf("expected iterations_completed=5, got %d", resp.IterationsCompleted)
		}
		if resp.Summary == nil {
			t.Fatal("expected summary to be set for completed job")
		}
		if resp.Summary.SuccessCount != 4 {
			t.Errorf("expected summary.success_count=4, got %d", resp.Summary.SuccessCount)
		}
		if resp.Summary.FailCount != 1 {
			t.Errorf("expected summary.fail_count=1, got %d", resp.Summary.FailCount)
		}
		if resp.Summary.TotalDuration != "29s" {
			t.Errorf("expected summary.total_duration=29s, got %q", resp.Summary.TotalDuration)
		}
	})

	t.Run("JobFailed_WithErrorAndSummary", func(t *testing.T) {
		s := newServer("")
		now := time.Now()
		startedAt := now.Add(-10 * time.Second)
		completedAt := now.Add(-2 * time.Second)
		report := &engine.ExecutionReport{
			TotalExecutions: 3,
			SuccessCount:    1,
			FailCount:       2,
		}
		j := &Job{
			ID:          "failed-job",
			Status:      JobStatusFailed,
			Request:     ExecRequest{Exec: "badcmd", Concurrency: 1, Iterations: 3},
			CreatedAt:   now.Add(-15 * time.Second),
			StartedAt:   &startedAt,
			CompletedAt: &completedAt,
			Report:      report,
			Error:       "exit status 1",
		}
		_ = s.JobStore.Create(j)

		rr := httptest.NewRecorder()
		s.JobsHandler(rr, makeGetReq("failed-job"))

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		var resp JobDetailResponse
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)

		if resp.State != JobStatusFailed {
			t.Errorf("expected state=failed, got %s", resp.State)
		}
		if resp.Error != "exit status 1" {
			t.Errorf("expected error='exit status 1', got %q", resp.Error)
		}
		if resp.Summary == nil {
			t.Fatal("expected summary for failed job")
		}
		if resp.Summary.FailCount != 2 {
			t.Errorf("expected summary.fail_count=2, got %d", resp.Summary.FailCount)
		}
	})

	t.Run("JobCancelled_WithSummary", func(t *testing.T) {
		s := newServer("")
		now := time.Now()
		startedAt := now.Add(-5 * time.Second)
		completedAt := now.Add(-1 * time.Second)
		report := &engine.ExecutionReport{
			TotalExecutions: 2,
			SuccessCount:    2,
			FailCount:       0,
		}
		j := &Job{
			ID:          "cancelled-job",
			Status:      JobStatusCancelled,
			Request:     ExecRequest{Exec: "echo hi", Concurrency: 1, Iterations: 10},
			CreatedAt:   now.Add(-8 * time.Second),
			StartedAt:   &startedAt,
			CompletedAt: &completedAt,
			Report:      report,
		}
		_ = s.JobStore.Create(j)

		rr := httptest.NewRecorder()
		s.JobsHandler(rr, makeGetReq("cancelled-job"))

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		var resp JobDetailResponse
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)

		if resp.State != JobStatusCancelled {
			t.Errorf("expected state=cancelled, got %s", resp.State)
		}
		if resp.Duration == "" {
			t.Error("expected duration to be set for cancelled job (has both started_at and completed_at)")
		}
		if resp.Summary == nil {
			t.Fatal("expected summary for cancelled job")
		}
		if resp.Summary.SuccessCount != 2 {
			t.Errorf("expected summary.success_count=2, got %d", resp.Summary.SuccessCount)
		}
	})

	t.Run("MethodNotAllowed", func(t *testing.T) {
		s := newServer("")
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(method, "/jobs/some-id", nil)
			s.JobsHandler(rr, req)
			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("method %s: expected 405, got %d", method, rr.Code)
			}
		}
	})

	t.Run("AuthEnforced", func(t *testing.T) {
		s := newServer("secret-token")

		j := &Job{
			ID:        "auth-test-job",
			Status:    JobStatusQueued,
			Request:   ExecRequest{Exec: "echo hi"},
			CreatedAt: time.Now(),
		}
		_ = s.JobStore.Create(j)

		t.Run("NoToken", func(t *testing.T) {
			rr := httptest.NewRecorder()
			s.JobsHandler(rr, makeGetReq("auth-test-job"))
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", rr.Code)
			}
		})

		t.Run("WrongToken", func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := makeGetReq("auth-test-job")
			req.Header.Set("Authorization", "Bearer wrong-token")
			s.JobsHandler(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", rr.Code)
			}
		})

		t.Run("CorrectToken", func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := makeGetReq("auth-test-job")
			req.Header.Set("Authorization", "Bearer secret-token")
			s.JobsHandler(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", rr.Code)
			}
		})
	})

	t.Run("ResponseIsConsistentJSON", func(t *testing.T) {
		s := newServer("")
		now := time.Now()
		j := &Job{
			ID:        "json-check",
			Status:    JobStatusQueued,
			Request:   ExecRequest{Exec: "echo hi", Concurrency: 1, Iterations: 1},
			CreatedAt: now,
		}
		_ = s.JobStore.Create(j)

		rr := httptest.NewRecorder()
		s.JobsHandler(rr, makeGetReq("json-check"))

		if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}
		if !json.Valid(rr.Body.Bytes()) {
			t.Errorf("response body is not valid JSON: %s", rr.Body.String())
		}
	})
}

func TestJobCancellation(t *testing.T) {
	newServer := func(authToken string) *Server {
		s := NewServer("127.0.0.1", 0, "1.0.0", authToken, "", "", "", "", engine.NewBuiltinRegistry(), 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
		s.Status = StatusReady
		return s
	}

	makeDelReq := func(id string) *http.Request {
		return httptest.NewRequest(http.MethodDelete, "/jobs/"+id, nil)
	}

	t.Run("AuthRequired", func(t *testing.T) {
		s := newServer("secret")
		rr := httptest.NewRecorder()
		s.JobsHandler(rr, makeDelReq("any-id"))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		s := newServer("")
		rr := httptest.NewRecorder()
		s.JobsHandler(rr, makeDelReq("nonexistent"))
		if rr.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rr.Code)
		}
	})

	t.Run("Conflict_TerminalState", func(t *testing.T) {
		s := newServer("")
		terminalStates := []JobStatus{JobStatusCompleted, JobStatusFailed, JobStatusCancelled}
		for _, state := range terminalStates {
			t.Run(string(state), func(t *testing.T) {
				j := &Job{ID: "term-" + string(state), Status: state, Request: ExecRequest{Exec: "echo"}}
				_ = s.JobStore.Create(j)
				rr := httptest.NewRecorder()
				s.JobsHandler(rr, makeDelReq(j.ID))
				if rr.Code != http.StatusConflict {
					t.Errorf("expected 409 for state %s, got %d", state, rr.Code)
				}
			})
		}
	})

	t.Run("CancelQueued", func(t *testing.T) {
		s := newServer("")
		j := &Job{ID: "queued-job", Status: JobStatusQueued, Request: ExecRequest{Exec: "echo"}}
		_ = s.JobStore.Create(j)
		
		rr := httptest.NewRecorder()
		s.JobsHandler(rr, makeDelReq(j.ID))
		
		if rr.Code != http.StatusAccepted {
			t.Errorf("expected 202, got %d", rr.Code)
		}
		
		job, _ := s.JobStore.Get(j.ID)
		if job.Status != JobStatusCancelled {
			t.Errorf("expected cancelled status, got %s", job.Status)
		}
		if job.CompletedAt == nil {
			t.Error("expected CompletedAt to be set")
		}
	})

    t.Run("CancelRunning", func(t *testing.T) {
        registry := engine.NewBuiltinRegistry()
        registry.Register(engine.NewSleepClient())
        s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
        s.Status = StatusReady

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

        // Wait for it to start
        time.Sleep(100 * time.Millisecond)

        // Cancel
        reqDel := httptest.NewRequest("DELETE", "/jobs/"+jobID, nil)
        rrDel := httptest.NewRecorder()
        s.JobsHandler(rrDel, reqDel)

        if rrDel.Code != http.StatusAccepted {
            t.Errorf("expected 202 Accepted, got %d", rrDel.Code)
        }

        // Verify status is cancelled
        job, _ := s.JobStore.Get(jobID)
        if job.Status != JobStatusCancelled {
            t.Errorf("expected status cancelled, got %s", job.Status)
        }
		if job.CompletedAt == nil {
			t.Error("expected CompletedAt to be set")
		}
	})
}

func TestJobReportHandler(t *testing.T) {
	newServer := func() *Server {
		s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", engine.NewBuiltinRegistry(), 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
		s.Status = StatusReady
		return s
	}

	t.Run("ReportNotFound", func(t *testing.T) {
		s := newServer()
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/jobs/nonexistent/report", nil)
		s.JobsHandler(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rr.Code)
		}
	})

	t.Run("ReportTooEarly", func(t *testing.T) {
		s := newServer()
		j := &Job{
			ID:        "running-job",
			Status:    JobStatusRunning,
			Request:   ExecRequest{Exec: "echo hi"},
			CreatedAt: time.Now(),
		}
		_ = s.JobStore.Create(j)

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/jobs/running-job/report", nil)
		s.JobsHandler(rr, req)

		if rr.Code != http.StatusTooEarly {
			t.Errorf("expected 425, got %d", rr.Code)
		}
	})

	t.Run("ReportCompleted", func(t *testing.T) {
		s := newServer()
		report := &engine.ExecutionReport{
			TotalExecutions: 10,
			SuccessCount:    8,
			FailCount:       2,
			TotalDuration:   "5s",
			MinLatency:      "100ms",
			P50Latency:      "200ms",
			P75Latency:      "250ms",
			P90Latency:      "300ms",
			P95Latency:      "400ms",
			P99Latency:      "500ms",
			MaxLatency:      "600ms",
			HTTPErrors:     map[string]int{"500 Internal Server Error": 2},
		}
		j := &Job{
			ID:     "done-job",
			Status: JobStatusCompleted,
			Request: ExecRequest{
				Exec:        "echo hi",
				Concurrency: 2,
				Iterations:  10,
			},
			CreatedAt: time.Now(),
			Report:    report,
		}
		_ = s.JobStore.Create(j)

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/jobs/done-job/report", nil)
		s.JobsHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}

		var resp JobReportResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if resp.JobID != "done-job" {
			t.Errorf("expected job_id=done-job, got %s", resp.JobID)
		}
		if resp.Status != "partial" {
			t.Errorf("expected status=partial, got %s", resp.Status)
		}
		if resp.Counts.Success != 8 {
			t.Errorf("expected success=8, got %d", resp.Counts.Success)
		}
		if resp.Counts.Failure != 2 {
			t.Errorf("expected failure=2, got %d", resp.Counts.Failure)
		}
		if resp.Latencies.P99 != "500ms" {
			t.Errorf("expected p99=500ms, got %s", resp.Latencies.P99)
		}
		if len(resp.Errors) != 1 {
			t.Errorf("expected 1 error entry, got %d", len(resp.Errors))
		}
		if resp.Errors[0].Count != 2 {
			t.Errorf("expected error count 2, got %d", resp.Errors[0].Count)
		}
	})
}

func TestJobStreamHandler(t *testing.T) {
	registry := engine.NewBuiltinRegistry()
	registry.Register(engine.NewSleepClient())
	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
	s.Status = StatusReady

	t.Run("StreamExecutionResults", func(t *testing.T) {
		payload := ExecRequest{
			Exec:        ".sleep 10ms",
			Concurrency: 1,
			Iterations:  3,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/async/exec", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		s.AsyncExecHandler(rr, req)

		var resp map[string]string
		json.Unmarshal(rr.Body.Bytes(), &resp)
		jobID := resp["job_id"]

		// Connect to stream
		reqStream := httptest.NewRequest("GET", "/jobs/"+jobID+"/stream", nil)
		rrStream := httptest.NewRecorder()

		// Use a context with timeout for the stream request
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		reqStream = reqStream.WithContext(ctx)

		// Run JobStreamHandler in a goroutine because it blocks
		done := make(chan bool)
		go func() {
			s.JobStreamHandler(rrStream, reqStream)
			done <- true
		}()

		select {
		case <-done:
			// Handler finished (either job done or context cancelled)
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for JobStreamHandler to finish")
		}

		output := rrStream.Body.String()
		
		// Verify SSE structure
		if !strings.Contains(output, "event: result") {
			t.Errorf("expected 'event: result' in output, got %q", output)
		}
		if !strings.Contains(output, "event: done") {
			t.Errorf("expected 'event: done' in output, got %q", output)
		}
		
		// Count result events
		resultsCount := strings.Count(output, "event: result")
		if resultsCount != 3 {
			t.Errorf("expected 3 result events, got %d", resultsCount)
		}

		// Verify data contains expected fields
		if !strings.Contains(output, `"worker_id":0`) {
			t.Errorf("expected worker_id:0 in output, got %q", output)
		}
	})

	t.Run("AuthEnforced", func(t *testing.T) {
		sAuth := NewServer("127.0.0.1", 0, "1.0.0", "secret-token", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
		sAuth.Status = StatusReady

		req := httptest.NewRequest("GET", "/jobs/some-id/stream", nil)
		rr := httptest.NewRecorder()

		sAuth.JobStreamHandler(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized, got %d", rr.Code)
		}
	})
}

func TestMaxConcurrentJobs(t *testing.T) {
	registry := engine.NewBuiltinRegistry()
	registry.Register(engine.NewSleepClient())
	// Set limit to 1
	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 0, 0, 1, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 10*time.Second, 3, nil, nil)
	s.Status = StatusReady

	t.Run("EnforceOnSync", func(t *testing.T) {
		// Mock an active job
		s.ActiveJobs.Store(1)
		defer s.ActiveJobs.Store(0)

		payload := ExecRequest{Exec: "echo 1"}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/exec", bytes.NewBuffer(body))
		rr := httptest.NewRecorder()

		s.ExecHandler(rr, req)

		if rr.Code != http.StatusTooManyRequests {
			t.Errorf("expected status 429, got %d", rr.Code)
		}
		if rr.Header().Get("Retry-After") != "60" {
			t.Errorf("expected Retry-After: 60, got %q", rr.Header().Get("Retry-After"))
		}
		var resp map[string]string
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
		if !strings.Contains(resp["error"], "maximum concurrent job capacity") {
			t.Errorf("unexpected error message: %s", resp["error"])
		}
	})

	t.Run("EnforceOnAsync", func(t *testing.T) {
		// Mock an active job
		s.ActiveJobs.Store(1)
		defer s.ActiveJobs.Store(0)

		payload := ExecRequest{Exec: "echo 1"}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/async/exec", bytes.NewBuffer(body))
		rr := httptest.NewRecorder()

		s.AsyncExecHandler(rr, req)

		if rr.Code != http.StatusTooManyRequests {
			t.Errorf("expected status 429, got %d", rr.Code)
		}
		if rr.Header().Get("Retry-After") != "60" {
			t.Errorf("expected Retry-After: 60, got %q", rr.Header().Get("Retry-After"))
		}
	})

	t.Run("RealConcurrentUsage", func(t *testing.T) {
		s.ActiveJobs.Store(0)
		
		// 1. Submit a long-running async job
		payload := ExecRequest{
			Exec:       ".sleep 200ms",
			Iterations: 1,
		}
		body, _ := json.Marshal(payload)
		req1 := httptest.NewRequest("POST", "/async/exec", bytes.NewBuffer(body))
		rr1 := httptest.NewRecorder()
		s.AsyncExecHandler(rr1, req1)

		if rr1.Code != http.StatusAccepted {
			t.Fatalf("first job should be accepted, got %d", rr1.Code)
		}

		// Wait for the goroutine to start and increment ActiveJobs
		timeout := time.After(1 * time.Second)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		
		found := false
		for !found {
			select {
			case <-timeout:
				t.Fatal("timed out waiting for job to start")
			case <-ticker.C:
				if s.ActiveJobs.Load() == 1 {
					found = true
				}
			}
		}

		// 2. Submit another job immediately, should be rejected
		req2 := httptest.NewRequest("POST", "/async/exec", bytes.NewBuffer(body))
		rr2 := httptest.NewRecorder()
		s.AsyncExecHandler(rr2, req2)

		if rr2.Code != http.StatusTooManyRequests {
			t.Errorf("second job should be rejected with 429, got %d", rr2.Code)
		}

		// 3. Wait for first job to finish
		found = false
		for !found {
			select {
			case <-timeout:
				t.Fatal("timed out waiting for job to finish")
			case <-ticker.C:
				if s.ActiveJobs.Load() == 0 {
					found = true
				}
			}
		}

		// 4. Submit again, should be accepted now
		req3 := httptest.NewRequest("POST", "/async/exec", bytes.NewBuffer(body))
		rr3 := httptest.NewRecorder()
		s.AsyncExecHandler(rr3, req3)

		if rr3.Code != http.StatusAccepted {
			t.Errorf("job should be accepted after limit cleared, got %d", rr3.Code)
		}
	})

	t.Run("VisibleInJobList", func(t *testing.T) {
		s.ActiveJobs.Store(2)
		defer s.ActiveJobs.Store(0)

		req := httptest.NewRequest("GET", "/jobs", nil)
		rr := httptest.NewRecorder()

		s.ListJobsHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}

		var resp ListJobsResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if resp.ActiveJobs != 2 {
			t.Errorf("expected active_jobs 2 in list response, got %d", resp.ActiveJobs)
		}
	})
}
