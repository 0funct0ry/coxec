package engine_test

import (
	"testing"
	"time"

	"github.com/0funct0ry/coxec/internal/engine"
)

func TestRunJobPool_Success(t *testing.T) {
	tasks := make(chan string, 3)
	tasks <- "echo 1"
	tasks <- "echo 2"
	tasks <- "echo 3"
	close(tasks)

	err := engine.RunJobPool(2, tasks)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestRunJobPool_Concurrency(t *testing.T) {
	tasks := make(chan string, 5)
	for i := 0; i < 5; i++ {
		tasks <- "sleep 1"
	}
	close(tasks)

	start := time.Now()
	err := engine.RunJobPool(5, tasks)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	duration := time.Since(start)
	if duration > 2*time.Second {
		t.Errorf("expected to finish in ~1s, took %v", duration)
	}
}

func TestRunJobPool_ErrorPropagated(t *testing.T) {
	tasks := make(chan string, 1)
	tasks <- "exit 1"
	close(tasks)

	err := engine.RunJobPool(1, tasks)
	if err == nil {
		t.Fatalf("expected error from failed command, got nil")
	}
}
