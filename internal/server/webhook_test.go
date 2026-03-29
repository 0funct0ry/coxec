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
	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, true, 1*time.Second, 1, nil, nil)
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

		sRetry := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, true, 1*time.Second, 2, nil, nil)
		
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
	s := &Server{
		CallbackAllowList: []string{"127.0.0.0/8", "10.0.0.0/8"},
	}

	tests := []struct {
		url     string
		wantErr bool
	}{
		{"http://127.0.0.1:8080/callback", false},
		{"https://10.0.0.5/webhook", false},
		{"http://google.com/webhook", true}, // Assuming google.com doesn't resolve to 127/8 or 10/8
		{"ftp://127.0.0.1/webhook", true},
		{"not-a-url", true},
	}

	for _, tt := range tests {
		err := s.validateCallbackURL(tt.url)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateCallbackURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
		}
	}
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
