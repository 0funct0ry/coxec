package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
	Report     bool
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
			return renderErr
		}

		if renderedStep == "" {
			continue
		}

		cmdName, args := ParseCommand(renderedStep)
		var currentResult *Result
		var err error
		var stepDuration time.Duration
		stepStart := time.Now()

		if opts.Registry != nil {
			if builtin, ok := opts.Registry.Get(cmdName); ok {
				currentResult, err = builtin.Execute(opts.Context, args, IterationData{
					Iteration: task.Index - 1,
					WorkerID:  task.WorkerID,
					UserVars:  opts.UserVars,
					Prev:      prevResult,
				})
				stepDuration = time.Since(stepStart)
			}
		}

		if currentResult == nil && err == nil {
			// Fall through to shell
			currentResult, err = runShellStep(renderedStep, task.Index)
			stepDuration = time.Since(stepStart)
		}

		exitCode := 0
		if currentResult != nil {
			exitCode = currentResult.ExitCode
		} else if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}

		outputMu.Lock()
		if !opts.Silent && currentResult != nil && !currentResult.IsTransparent {
			if len(currentResult.Stdout) > 0 {
				opts.Stdout.Write([]byte(currentResult.Stdout))
			}
		}

		if opts.Verbose {
			statusIndicator := "✓ exit 0"
			if exitCode != 0 {
				statusIndicator = fmt.Sprintf("✗ exit %d", exitCode)
			} else if err != nil {
				statusIndicator = "✗ error"
			}
			durationStr := stepDuration.Round(time.Millisecond).String()
			fmt.Fprintf(opts.Stderr, "[%d/%d] %s   %s   %s\n",
				task.Index, opts.TotalTasks, renderedStep, durationStr, statusIndicator)

			if currentResult != nil && !currentResult.IsTransparent {
				if len(currentResult.Stdout) > 0 && !opts.Silent {
					fmt.Fprintf(opts.Stderr, "      stdout: %s", currentResult.Stdout)
					if !strings.HasSuffix(currentResult.Stdout, "\n") {
						fmt.Fprintln(opts.Stderr)
					}
				}
				if len(currentResult.Stderr) > 0 && !opts.Silent {
					fmt.Fprintf(opts.Stderr, "      stderr: %s", currentResult.Stderr)
					if !strings.HasSuffix(currentResult.Stderr, "\n") {
						fmt.Fprintln(opts.Stderr)
					}
				}
			}
			if err != nil && currentResult == nil {
				fmt.Fprintf(opts.Stderr, "      error: %v\n", err)
			}
			fmt.Fprintln(opts.Stderr)
		}
		outputMu.Unlock()

		if err != nil {
			return err
		}
		prevResult = currentResult
	}

	return nil
}

func runShellStep(command string, taskIndex int) (*Result, error) {
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

	type tempErrCount struct {
		err   *TemplateError
		count int
	}
	templateErrors := make(map[string]*tempErrCount)

	type httpErrCount struct {
		err   *HTTPError
		count int
	}
	httpErrors := make(map[string]*httpErrCount)

	type tcpErrCount struct {
		err   *TCPError
		count int
	}
	tcpErrors := make(map[string]*tcpErrCount)

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
					if te, ok := err.(*TemplateError); ok {
						k := te.Error()
						if _, exists := templateErrors[k]; !exists {
							templateErrors[k] = &tempErrCount{err: te, count: 0}
						}
						templateErrors[k].count++
					}
					var he *HTTPError
					if errors.As(err, &he) {
						k := he.Category
						if _, exists := httpErrors[k]; !exists {
							httpErrors[k] = &httpErrCount{err: he, count: 0}
						}
						httpErrors[k].count++
					}
					var tce *TCPError
					if errors.As(err, &tce) {
						k := tce.Category
						if _, exists := tcpErrors[k]; !exists {
							tcpErrors[k] = &tcpErrCount{err: tce, count: 0}
						}
						tcpErrors[k].count++
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

	if len(templateErrors) > 0 {
		fmt.Fprintf(stderr, "\nTemplate Errors:\n")
		for _, tec := range templateErrors {
			fmt.Fprintf(stderr, "  - %s (occurred %d times)\n", tec.err.Error(), tec.count)
			if tec.err.Suggestion != "" {
				fmt.Fprintf(stderr, "    Suggestion: %s\n", tec.err.Suggestion)
			}
		}
	}

	if opts.Report && len(httpErrors) > 0 {
		fmt.Fprintf(stderr, "\nHTTP Errors:\n")
		for cat, hec := range httpErrors {
			fmt.Fprintf(stderr, "  - %s: %d\n", cat, hec.count)
		}
	}

	if opts.Report && len(tcpErrors) > 0 {
		fmt.Fprintf(stderr, "\nTCP Errors:\n")
		for cat, tec := range tcpErrors {
			fmt.Fprintf(stderr, "  - %s: %d\n", cat, tec.count)
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

