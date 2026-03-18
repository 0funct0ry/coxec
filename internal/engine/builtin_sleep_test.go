package engine

import (
	"context"
	"testing"
	"time"
)

func TestSleepClient(t *testing.T) {
	client := NewSleepClient()

	t.Run("Valid Duration", func(t *testing.T) {
		start := time.Now()
		res, err := client.Execute(context.Background(), []string{"100ms"}, IterationData{})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		duration := time.Since(start)

		if duration < 100*time.Millisecond {
			t.Errorf("Expected sleep of at least 100ms, got %v", duration)
		}
		if res.Latency < int64(100*time.Millisecond) {
			t.Errorf("Expected latency of at least 100ms, got %v", res.Latency)
		}
		if !res.IsTransparent {
			t.Error("Expected result to be transparent")
		}
	})

	t.Run("Invalid Duration", func(t *testing.T) {
		_, err := client.Execute(context.Background(), []string{"invalid"}, IterationData{})
		if err == nil {
			t.Error("Expected error for invalid duration, got nil")
		}
	})

	t.Run("Missing Duration", func(t *testing.T) {
		_, err := client.Execute(context.Background(), []string{}, IterationData{})
		if err == nil {
			t.Error("Expected error for missing duration, got nil")
		}
	})

	t.Run("Context Cancellation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		start := time.Now()
		_, err := client.Execute(ctx, []string{"1s"}, IterationData{})
		duration := time.Since(start)

		if err == nil {
			t.Error("Expected error for cancelled context, got nil")
		}
		if duration > 200*time.Millisecond {
			t.Errorf("Expected sleep to be interrupted, got %v", duration)
		}
	})

	t.Run("Prev Passthrough", func(t *testing.T) {
		prev := &Result{Stdout: "hello", Stderr: "world"}
		res, err := client.Execute(context.Background(), []string{"10ms"}, IterationData{Prev: prev})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if res.Stdout != "hello" {
			t.Errorf("Expected Stdout 'hello', got '%s'", res.Stdout)
		}
		if res.Stderr != "world" {
			t.Errorf("Expected Stderr 'world', got '%s'", res.Stderr)
		}
	})
}
