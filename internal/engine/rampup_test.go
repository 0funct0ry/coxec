package engine_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0funct0ry/coxec/internal/engine"
)

func TestRunJobPool_RampUp(t *testing.T) {
	concurrency := 5
	rampUp := 400 * time.Millisecond
	// Interval should be 400ms / 4 = 100ms

	tasks := make(chan engine.Task)
	go func() {
		defer close(tasks)
		// Provide enough tasks to keep workers busy
		for i := 0; i < 20; i++ {
			tasks <- engine.Task{Index: i + 1, Command: "sleep 0.2"}
		}
	}()

	var activeCount atomic.Int32
	opts := engine.ExecOptions{
		TotalTasks:  20,
		RampUp:      rampUp,
		ActiveCount: &activeCount,
		Context:     context.Background(),
	}

	start := time.Now()
	
	// Run in a goroutine so we can monitor activeCount
	done := make(chan error, 1)
	go func() {
		done <- engine.RunJobPool(concurrency, tasks, opts)
	}()

	// Monitor active count
	// t=0: 1 worker
	// t=100ms: 2 workers
	// t=200ms: 3 workers
	// t=300ms: 4 workers
	// t=400ms: 5 workers

	time.Sleep(50 * time.Millisecond)
	if c := activeCount.Load(); c != 1 {
		t.Errorf("at 50ms, expected 1 active worker, got %d", c)
	}

	time.Sleep(100 * time.Millisecond) // total 150ms
	if c := activeCount.Load(); c != 2 {
		t.Errorf("at 150ms, expected 2 active workers, got %d", c)
	}

	time.Sleep(100 * time.Millisecond) // total 250ms
	if c := activeCount.Load(); c != 3 {
		t.Errorf("at 250ms, expected 3 active workers, got %d", c)
	}

	time.Sleep(200 * time.Millisecond) // total 450ms
	if c := activeCount.Load(); c != 5 {
		t.Errorf("at 450ms, expected 5 active workers, got %d", c)
	}

	err := <-done
	if err != nil {
		t.Fatalf("RunJobPool failed: %v", err)
	}

	totalDuration := time.Since(start)
	// Each task takes 200ms. 20 tasks / 5 workers = 4 tasks per worker.
	// Last worker starts at 400ms, then does 4 tasks? No, tasks are shared.
	// But it should take at least 400ms because of ramp-up.
	if totalDuration < rampUp {
		t.Errorf("expected total duration >= rampUp, got %v", totalDuration)
	}
}
