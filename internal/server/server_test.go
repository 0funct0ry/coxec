package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthCheck(t *testing.T) {
	s := NewServer("127.0.0.1", 8080, "1.0.0")
	
	t.Run("StatusStarting", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		rr := httptest.NewRecorder()
		
		s.healthHandler(rr, req)
		
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
		
		s.healthHandler(rr, req)
		
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
		
		s.healthHandler(rr, req)
		
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
