package engine

import (
	"context"
	"fmt"
	"time"
)

// SleepClient is a BuiltinClient that pauses execution.
type SleepClient struct{}

// NewSleepClient creates a new SleepClient.
func NewSleepClient() *SleepClient {
	return &SleepClient{}
}

// Name returns the name of the built-in client.
func (c *SleepClient) Name() string {
	return "sleep"
}

// Execute pauses execution for the duration specified in the first argument.
func (c *SleepClient) Execute(ctx context.Context, args []string, data IterationData) (*Result, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("sleep requires a duration (e.g. 1.5s, 750ms)")
	}

	duration, err := time.ParseDuration(args[0])
	if err != nil {
		return nil, fmt.Errorf("invalid duration format '%s': %w. Use 1s, 500ms, etc.", args[0], err)
	}

	start := time.Now()
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		// Sleep finished
	}

	latency := time.Since(start)

	// To keep .Prev available for the next step, we return the previous result data
	// but mark this result as transparent so it doesn't print anything or affect output.
	res := &Result{
		Stdout:        "",
		Stderr:        "",
		ExitCode:      0,
		Latency:       latency.Nanoseconds(),
		IsTransparent: true,
	}

	if data.Prev != nil {
		res.Stdout = data.Prev.Stdout
		res.Stderr = data.Prev.Stderr
	}

	return res, nil
}
