package engine_test

import (
	"testing"
	"time"

	"github.com/0funct0ry/coxec/internal/engine"
)

func TestRunJobPool_Success(t *testing.T) {
	tasks := make(chan engine.Task, 3)
	tasks <- engine.Task{Index: 1, Command: "echo 1"}
	tasks <- engine.Task{Index: 2, Command: "echo 2"}
	tasks <- engine.Task{Index: 3, Command: "echo 3"}
	close(tasks)

	opts := engine.ExecOptions{TotalTasks: 3}
	err := engine.RunJobPool(2, tasks, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestRunJobPool_Concurrency(t *testing.T) {
	tasks := make(chan engine.Task, 5)
	for i := 0; i < 5; i++ {
		tasks <- engine.Task{Index: i + 1, Command: "sleep 1"}
	}
	close(tasks)

	opts := engine.ExecOptions{TotalTasks: 5}
	start := time.Now()
	err := engine.RunJobPool(5, tasks, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	duration := time.Since(start)
	if duration > 2*time.Second {
		t.Errorf("expected to finish in ~1s, took %v", duration)
	}
}

func TestRunJobPool_ErrorPropagated(t *testing.T) {
	tasks := make(chan engine.Task, 1)
	tasks <- engine.Task{Index: 1, Command: "exit 1"}
	close(tasks)

	opts := engine.ExecOptions{TotalTasks: 1}
	err := engine.RunJobPool(1, tasks, opts)
	if err == nil {
		t.Fatalf("expected error from failed command, got nil")
	}

	exitErr, ok := err.(*engine.ExitError)
	if !ok {
		t.Fatalf("expected *engine.ExitError, got %T", err)
	}
	if exitErr.Code != 2 {
		t.Errorf("expected exit code 2 (all failed), got %v", exitErr.Code)
	}
}

func TestRunJobPool_PartialFailure(t *testing.T) {
	tasks := make(chan engine.Task, 2)
	tasks <- engine.Task{Index: 1, Command: "exit 0"}
	tasks <- engine.Task{Index: 2, Command: "exit 1"}
	close(tasks)

	opts := engine.ExecOptions{TotalTasks: 2}
	err := engine.RunJobPool(2, tasks, opts)
	if err == nil {
		t.Fatalf("expected error from failed command, got nil")
	}

	exitErr, ok := err.(*engine.ExitError)
	if !ok {
		t.Fatalf("expected *engine.ExitError, got %T", err)
	}
	if exitErr.Code != 1 {
		t.Errorf("expected exit code 1 (partial failure), got %v", exitErr.Code)
	}
}

func TestRunShellCommand_Env(t *testing.T) {
	tasks := make(chan engine.Task, 1)
	tasks <- engine.Task{Index: 42, Command: "[[ $COXEC_INDEX -eq 42 ]]"}
	close(tasks)

	opts := engine.ExecOptions{TotalTasks: 1}
	err := engine.RunJobPool(1, tasks, opts)
	if err != nil {
		t.Fatalf("expected COXEC_INDEX to be 42, got error: %v", err)
	}
}
