package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/0funct0ry/coxec/internal/engine"
)

func TestWebhookDelivery(t *testing.T) {
	// Setup a mock webhook receiver
	var mu sync.Mutex
	receivedPayloads := make([]JobDetailResponse, 0)
	
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload JobDetailResponse
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("failed to decode webhook payload: %v", err)
			return
		}
		mu.Lock()
		receivedPayloads = append(receivedPayloads, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	registry := engine.NewBuiltinRegistry()
	// Disable real execution in this test, just enough to trigger the flow
	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, true, 1*time.Second, 1, nil, false, nil)
	s.Status = StatusReady

	t.Run("SuccessfulDelivery", func(t *testing.T) {
		job := &Job{
			ID:     "webhook-job-1",
			Status: JobStatusCompleted,
			Request: ExecRequest{
				CallbackURL: ts.URL,
			},
		}
		_ = s.JobStore.Create(job)

		s.sendWebhook(job)

		mu.Lock()
		defer mu.Unlock()
		if len(receivedPayloads) != 1 {
			t.Errorf("expected 1 webhook received, got %d", len(receivedPayloads))
		} else if receivedPayloads[0].JobID != "webhook-job-1" {
			t.Errorf("expected job_id webhook-job-1, got %s", receivedPayloads[0].JobID)
		}
	})

	t.Run("DeliveryWithRetries", func(t *testing.T) {
		attempts := 0
		tsRetry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			attempts++
			mu.Unlock()
			if attempts < 2 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer tsRetry.Close()

		sRetry := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, true, 1*time.Second, 2, nil, false, nil)
		
		job := &Job{
			ID:     "retry-job",
			Status: JobStatusCompleted,
			Request: ExecRequest{
				CallbackURL: tsRetry.URL,
			},
		}
		
		sRetry.sendWebhook(job)

		if attempts != 2 {
			t.Errorf("expected 2 attempts, got %d", attempts)
		}
	})
}

func TestValidateCallbackURL(t *testing.T) {
	t.Run("HTTPSOnlyByDefault", func(t *testing.T) {
		s := &Server{}
		if err := s.validateCallbackURL("http://example.com"); err == nil {
			t.Error("expected error for HTTP callback when no allow-list provided")
		}
		if err := s.validateCallbackURL("https://example.com"); err != nil {
			t.Errorf("unexpected error for HTTPS callback: %v", err)
		}
	})
	
	t.Run("AllowInsecureOverride", func(t *testing.T) {
		s := &Server{
			CallbackAllowInsecure: true,
		}
		if err := s.validateCallbackURL("http://example.com"); err != nil {
			t.Errorf("expected HTTP allowed when CallbackAllowInsecure is true, got: %v", err)
		}
	})

	t.Run("AllowListOverridesHTTPS", func(t *testing.T) {
		s := &Server{
			CallbackAllowList: []string{"127.0.0.1/32"},
		}
		if err := s.validateCallbackURL("http://127.0.0.1/callback"); err != nil {
			t.Errorf("expected HTTP allowed if in allow-list, got err: %v", err)
		}
		if err := s.validateCallbackURL("http://10.0.0.1/callback"); err == nil {
			t.Error("expected error for IP not in allow-list")
		}
	})

	t.Run("InvalidCIDRInAllowList", func(t *testing.T) {
		s := &Server{
			CallbackAllowList: []string{"not-a-cidr"},
		}
		if err := s.validateCIDRs(); err == nil {
			t.Error("expected error for invalid CIDR")
		}
	})
}

func TestCIDRValidation(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("192.168.1.0/24")
	ipInside := net.ParseIP("192.168.1.5")
	ipOutside := net.ParseIP("192.168.2.1")

	if !cidr.Contains(ipInside) {
		t.Errorf("expected %s to be in %s", ipInside, cidr)
	}
	if cidr.Contains(ipOutside) {
		t.Errorf("expected %s NOT to be in %s", ipOutside, cidr)
	}
}
