package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
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
	Stdout     interface {
		Write([]byte) (int, error)
	}
	Stderr interface {
		Write([]byte) (int, error)
	}
}

// RunShellCommand executes a single command in the default shell
// Captures output and handles printing based on ExecOptions
func RunShellCommand(task Task, opts ExecOptions) error {
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	// Render command as a template
	renderedCmd, renderErr := renderTemplate("command", task.Command, IterationData{Iteration: task.Index})
	if renderErr != nil {
		// If template rendering fails, we don't even try to run the command.
		// We use a dummy command execution or just return the error.
		// Returning the error will mark the task as failed in RunJobPool.
		
		// Still need to handle silent/verbose/etc if we want to show the error
		// but RunJobPool will catch it and we can handle it there or if we log it here.
		// Requirement: "When template rendering fails the error message is shown to the user and the execution of that iteration is marked as failed"
		
		outputMu.Lock()
		if opts.Verbose {
			fmt.Fprintf(stderr, "[%d/%d] Template error: %v\n\n", task.Index, opts.TotalTasks, renderErr)
		} else if !opts.Silent {
			fmt.Fprintf(stderr, "Iteration %d: template error: %v\n", task.Index, renderErr)
		}
		outputMu.Unlock()
		return renderErr
	}

	// We do NOT use opts.Context here (which might be a cancellation context from SIGINT)
	// because the requirement is to allow already running executions to finish.
	// We also put the child in its own process group to isolate it from terminal SIGINT.
	cmd := exec.Command("sh", "-c", renderedCmd)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

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
			stdout.Write(stdoutBuf.Bytes())
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

		fmt.Fprintf(stderr, "[%d/%d] %s   %s   %s\n",
			task.Index, opts.TotalTasks, task.Command, durationStr, statusIndicator)

		if stdoutBuf.Len() > 0 && !opts.Silent {
			fmt.Fprintf(stderr, "      stdout: %s", stdoutBuf.String())
			if stdoutBuf.Bytes()[stdoutBuf.Len()-1] != '\n' {
				fmt.Fprintln(stderr)
			}
		}

		if stderrBuf.Len() > 0 && !opts.Silent {
			fmt.Fprintf(stderr, "      stderr: %s", stderrBuf.String())
			if stderrBuf.Bytes()[stderrBuf.Len()-1] != '\n' {
				fmt.Fprintln(stderr)
			}
		}

		fmt.Fprintln(stderr) // separator for readability
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
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	if totalExecs > 0 {
		fmt.Fprintf(stderr, "Completed %d executions in %.3fs\n", totalExecs, poolDuration.Seconds())
		fmt.Fprintf(stderr, "Success: %d   Failed: %d\n", successCount.Load(), failCount.Load())
		if rate > 0 {
			fmt.Fprintf(stderr, "Rate: ~%.1f executions/sec\n", rate)
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
