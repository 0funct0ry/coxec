package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// generateUUIDv4 generates a basic v4-compliant UUID string using crypto/rand
func generateUUIDv4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

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
	Index     int
	WorkerID  int
	Command   string
	Timestamp time.Time
	UUID      string
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
	UserVars   map[string]string
	Stderr interface {
		Write([]byte) (int, error)
	}
	Registry      *BuiltinRegistry
	TemplateState *TemplateState
}

// RunPipeline executes a series of pipeline steps
func RunPipeline(task Task, opts ExecOptions) error {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}

	if task.Timestamp.IsZero() {
		task.Timestamp = time.Now()
	}
	if task.UUID == "" {
		task.UUID = generateUUIDv4()
	}

	const rfc3339Milli = "2006-01-02T15:04:05.000Z07:00"

	steps := SplitPipeline(task.Command)
	if len(steps) == 0 {
		return nil // Should be caught by EMPTY_TEMPLATE validation earlier but safe to handle
	}

	var prevResult *Result
	for _, stepTmpl := range steps {
		renderedStep, renderErr := renderTemplate("step", stepTmpl, IterationData{
			Iteration:          task.Index - 1,
			WorkerID:           task.WorkerID,
			Timestamp:          task.Timestamp.Format(rfc3339Milli),
			TimestampUnix:      task.Timestamp.Unix(),
			TimestampUnixMilli: task.Timestamp.UnixMilli(),
			TimestampUnixNano:  task.Timestamp.UnixNano(),
			UUID:               task.UUID,
			UserVars:           opts.UserVars,
			Prev:               prevResult,
		}, opts.TemplateState)

		if renderErr != nil {
			outputMu.Lock()
			if opts.Verbose {
				fmt.Fprintf(opts.Stderr, "[%d/%d] Template error: %v\n\n", task.Index, opts.TotalTasks, renderErr)
			} else if !opts.Silent {
				fmt.Fprintf(opts.Stderr, "Iteration %d: template error: %v\n", task.Index, renderErr)
			}
			outputMu.Unlock()
			return renderErr
		}

		if renderedStep == "" {
			continue
		}

		cmdName, args := ParseCommand(renderedStep)
		var currentResult *Result
		var err error

		if opts.Registry != nil {
			if builtin, ok := opts.Registry.Get(cmdName); ok {
				currentResult, err = builtin.Execute(opts.Context, args, IterationData{
					Iteration: task.Index - 1,
					WorkerID:  task.WorkerID,
					UserVars:  opts.UserVars,
					Prev:      prevResult,
				})
			}
		}

		if currentResult == nil {
			// Fall through to shell
			currentResult, err = runShellStep(renderedStep, opts, task.Index)
		}

		if err != nil {
			return err
		}
		prevResult = currentResult
	}

	return nil
}

func runShellStep(command string, opts ExecOptions, taskIndex int) (*Result, error) {
	cmd := exec.Command("sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), fmt.Sprintf("COXEC_INDEX=%d", taskIndex))

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
	if !opts.Silent {
		if stdoutBuf.Len() > 0 {
			opts.Stdout.Write(stdoutBuf.Bytes())
		}
	}

	if opts.Verbose {
		statusIndicator := "✓ exit 0"
		if exitCode != 0 {
			statusIndicator = fmt.Sprintf("✗ exit %d", exitCode)
		}
		durationStr := duration.Round(time.Millisecond).String()
		fmt.Fprintf(opts.Stderr, "[%d/%d] %s   %s   %s\n",
			taskIndex, opts.TotalTasks, command, durationStr, statusIndicator)
		
		if stdoutBuf.Len() > 0 && !opts.Silent {
			fmt.Fprintf(opts.Stderr, "      stdout: %s", stdoutBuf.String())
			if stdoutBuf.Bytes()[stdoutBuf.Len()-1] != '\n' {
				fmt.Fprintln(opts.Stderr)
			}
		}
		if stderrBuf.Len() > 0 && !opts.Silent {
			fmt.Fprintf(opts.Stderr, "      stderr: %s", stderrBuf.String())
			if stderrBuf.Bytes()[stderrBuf.Len()-1] != '\n' {
				fmt.Fprintln(opts.Stderr)
			}
		}
		fmt.Fprintln(opts.Stderr)
	}
	outputMu.Unlock()

	return &Result{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		ExitCode: exitCode,
		Latency:  duration.Nanoseconds(),
	}, err
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
		workerID := i
		go func(id int) {
			defer wg.Done()
			for task := range tasks {
				if opts.Context != nil && opts.Context.Err() != nil {
					continue
				}

				task.WorkerID = id
				if err := RunPipeline(task, opts); err != nil {
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
		}(workerID)
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
