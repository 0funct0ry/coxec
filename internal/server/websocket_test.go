package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/0funct0ry/coxec/internal/engine"
	"github.com/gorilla/websocket"
)

func TestWebsocketHandler(t *testing.T) {
	registry := engine.NewBuiltinRegistry()
	// Using 25 parameters as per the updated NewServer signature
	s := NewServer("127.0.0.1", 0, "1.0.0", "", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 100*time.Millisecond, 2, nil, false, true, 100*time.Millisecond, 2, nil)
	s.Status = StatusReady

	server := httptest.NewServer(http.HandlerFunc(s.WebsocketHandler))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	t.Run("MissingJobID", func(t *testing.T) {
		_, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err == nil {
			t.Fatal("expected error for missing job_id")
		}
	})

	t.Run("JobNotFound", func(t *testing.T) {
		u, _ := url.Parse(wsURL)
		q := u.Query()
		q.Set("job_id", "non-existent")
		u.RawQuery = q.Encode()

		_, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if err == nil {
			t.Fatal("expected error for non-existent job")
		}
	})

	t.Run("FullFlow", func(t *testing.T) {
		job := &Job{
			ID:     "ws-test-job",
			Status: JobStatusRunning,
			Request: ExecRequest{
				Exec: "echo hello",
			},
			CreatedAt: time.Now(),
		}
		s.JobStore.Create(job)
		s.mu.Lock()
		s.jobCancels[job.ID] = func() {}
		s.mu.Unlock()

		u, _ := url.Parse(wsURL)
		q := u.Query()
		q.Set("job_id", job.ID)
		u.RawQuery = q.Encode()

		conn, resp, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer conn.Close()

		if resp.StatusCode != http.StatusSwitchingProtocols {
			t.Errorf("expected 101, got %d", resp.StatusCode)
		}

		// 1. Receive initial snapshot
		var snapshot JobDetailResponse
		if err := conn.ReadJSON(&snapshot); err != nil {
			t.Fatalf("read snapshot error: %v", err)
		}
		if snapshot.JobID != job.ID {
			t.Errorf("expected job_id %s, got %s", job.ID, snapshot.JobID)
		}

		// 2. Stream an update (TaskResult is internal, broadcast uses map)
		update := map[string]interface{}{
			"type": "result",
			"data": engine.ExecutionDetail{
				Index:  1,
				Status: "success",
				Output: "hello\n",
			},
		}
		s.broadcast(job.ID, update)

		var received map[string]interface{}
		if err := conn.ReadJSON(&received); err != nil {
			t.Fatalf("read update error: %v", err)
		}
		if received["type"] != "result" {
			t.Errorf("expected type 'result', got %v", received["type"])
		}

		// 3. Send cancellation
		cancelMsg := map[string]string{"action": "cancel"}
		if err := conn.WriteJSON(cancelMsg); err != nil {
			t.Fatalf("write cancel error: %v", err)
		}

		// Give it a moment to process
		time.Sleep(50 * time.Millisecond)
		updatedJob, _ := s.JobStore.Get(job.ID)
		if updatedJob.Status != JobStatusCancelled {
			t.Errorf("expected job to be cancelled, got %s", updatedJob.Status)
		}

		// 4. Send "done" event and verify auto-close
		doneEvent := map[string]interface{}{
			"type": "done",
			"data": jobToDetail(job),
		}
		s.broadcast(job.ID, doneEvent)

		var receivedDone map[string]interface{}
		if err := conn.ReadJSON(&receivedDone); err != nil {
			t.Fatalf("read done error: %v", err)
		}
		if receivedDone["type"] != "done" {
			t.Errorf("expected type 'done', got %v", receivedDone["type"])
		}

		// Next read should fail (EOF) because server closes on "done"
		_, _, err = conn.ReadMessage()
		if err == nil {
			t.Error("expected connection to be closed after done event")
		}
	})

	t.Run("AuthRequired", func(t *testing.T) {
		sAuth := NewServer("127.0.0.1", 0, "1.0.0", "secret", "", "", "", "", registry, 1, 1, 0, true, NewInMemoryJobStore(), 24*time.Hour, 1000, false, 100*time.Millisecond, 2, nil, false, true, 100*time.Millisecond, 2, nil)
		sAuth.Status = StatusReady
		serverAuth := httptest.NewServer(http.HandlerFunc(sAuth.WebsocketHandler))
		defer serverAuth.Close()

		wsAuthURL := "ws" + strings.TrimPrefix(serverAuth.URL, "http") + "?job_id=any"

		_, resp, err := websocket.DefaultDialer.Dial(wsAuthURL, nil)
		if err == nil {
			t.Fatal("expected error for unauthorized connection")
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", resp.StatusCode)
		}
	})

	t.Run("MaxClients", func(t *testing.T) {
		// Server has limit of 2 (set in initial NewServer call)
		job := &Job{ID: "limit-job", Status: JobStatusRunning, CreatedAt: time.Now()}
		s.JobStore.Create(job)
		u, _ := url.Parse(wsURL)
		q := u.Query()
		q.Set("job_id", job.ID)
		u.RawQuery = q.Encode()

		// Fill up 2 slots
		c1, _, _ := websocket.DefaultDialer.Dial(u.String(), nil)
		defer c1.Close()
		c2, _, _ := websocket.DefaultDialer.Dial(u.String(), nil)
		defer c2.Close()

		// 3rd should fail
		_, resp, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if err == nil {
			t.Fatal("expected error for exceeding max clients")
		}
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("expected 503, got %d", resp.StatusCode)
		}
	})
}
