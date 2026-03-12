package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

var outputMu sync.Mutex

// ExitError wraps an error with a specific exit code for the CLI
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("exit status %d", e.Code)
}

// Task defines a single execution target
type Task struct {
	Index   int
	Command string
}

// ExecOptions groups behavior flags for the engine
type ExecOptions struct {
	Verbose    bool
	Silent     bool
	TotalTasks int
	Context    context.Context
}

// RunShellCommand executes a single command in the default shell
// Captures output and handles printing based on ExecOptions
func RunShellCommand(task Task, opts ExecOptions) error {
	var cmd *exec.Cmd
	if opts.Context != nil {
		cmd = exec.CommandContext(opts.Context, "sh", "-c", task.Command)
	} else {
		cmd = exec.Command("sh", "-c", task.Command)
	}

	cmd.Env = append(os.Environ(), fmt.Sprintf("COXEC_INDEX=%d", task.Index))

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	outputMu.Lock()
	defer outputMu.Unlock()

	// 1. Silent flag: only suppress child stdout if silent is true.
	if !opts.Silent {
		if stdoutBuf.Len() > 0 {
			os.Stdout.Write(stdoutBuf.Bytes())
		}
	}

	// 2. Verbose block on stderr
	if opts.Verbose {
		statusIndicator := "✓ exit 0"
		if exitCode != 0 {
			statusIndicator = fmt.Sprintf("✗ exit %d", exitCode)
		}

		durationStr := duration.Round(time.Millisecond).String()
		if duration < time.Millisecond {
			durationStr = duration.Round(time.Microsecond).String()
		}

		fmt.Fprintf(os.Stderr, "[%d/%d] %s   %s   %s\n",
			task.Index, opts.TotalTasks, task.Command, durationStr, statusIndicator)

		if stdoutBuf.Len() > 0 && !opts.Silent {
			fmt.Fprintf(os.Stderr, "      stdout: %s", stdoutBuf.String())
			if stdoutBuf.Bytes()[stdoutBuf.Len()-1] != '\n' {
				fmt.Fprintln(os.Stderr)
			}
		}

		if stderrBuf.Len() > 0 && !opts.Silent {
			fmt.Fprintf(os.Stderr, "      stderr: %s", stderrBuf.String())
			if stderrBuf.Bytes()[stderrBuf.Len()-1] != '\n' {
				fmt.Fprintln(os.Stderr)
			}
		}

		fmt.Fprintln(os.Stderr) // separator for readability
	}

	return err
}

// RunJobPool executes commands from the tasks channel across a pool of worker goroutines
func RunJobPool(concurrency int, tasks <-chan Task, opts ExecOptions) error {
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error

	var successCount atomic.Int32
	var failCount atomic.Int32

	poolStart := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range tasks {
				if opts.Context != nil && opts.Context.Err() != nil {
					continue
				}

				if err := RunShellCommand(task, opts); err != nil {
					failCount.Add(1)
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
				} else {
					successCount.Add(1)
				}
			}
		}()
	}

	wg.Wait()
	poolDuration := time.Since(poolStart)

	totalExecs := successCount.Load() + failCount.Load()
	rate := 0.0
	if poolDuration.Seconds() > 0 && totalExecs > 0 {
		rate = float64(totalExecs) / poolDuration.Seconds()
	}

	outputMu.Lock()
	defer outputMu.Unlock()

	// Print summary to stderr
	if totalExecs > 0 {
		fmt.Fprintf(os.Stderr, "Completed %d executions in %.3fs\n", totalExecs, poolDuration.Seconds())
		fmt.Fprintf(os.Stderr, "Success: %d   Failed: %d\n", successCount.Load(), failCount.Load())
		if rate > 0 {
			fmt.Fprintf(os.Stderr, "Rate: ~%.1f executions/sec\n", rate)
		}
	}

	if opts.Context != nil && opts.Context.Err() != nil {
		return &ExitError{Code: 130, Err: opts.Context.Err()}
	}

	total := successCount.Load() + failCount.Load()
	if total == 0 {
		return nil
	}

	fc := failCount.Load()
	sc := successCount.Load()

	if fc > 0 && sc == 0 {
		return &ExitError{Code: 2, Err: firstErr}
	} else if fc > 0 && sc > 0 {
		return &ExitError{Code: 1, Err: firstErr}
	}

	return nil
}
