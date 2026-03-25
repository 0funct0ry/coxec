package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// GenerateUUIDv4 generates a basic v4-compliant UUID string using crypto/rand
func GenerateUUIDv4() string {
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
	Silent        bool
	Verbose       bool
	Registry      *BuiltinRegistry
	TotalTasks    int
	Stdout        io.Writer
	Stderr        io.Writer
	Context       context.Context
	UserVars      map[string]string
	TemplateState *TemplateState
	ActiveCount   *atomic.Int32
	Report        bool
	Timeout       time.Duration
	Delay         time.Duration
	Jitter        time.Duration
	RampUp        time.Duration
	RateLimit     float64
	OnResult      func(ExecutionDetail)
}

// RunPipeline executes a series of pipeline steps
func RunPipeline(task Task, opts ExecOptions) (*Result, error) {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}

	if task.Timestamp.IsZero() {
		task.Timestamp = time.Now()
	}
	if task.UUID == "" {
		task.UUID = GenerateUUIDv4()
	}

	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	const rfc3339Milli = "2006-01-02T15:04:05.000Z07:00"

	steps := SplitPipeline(task.Command)
	if len(steps) == 0 {
		return nil, nil // Should be caught by EMPTY_TEMPLATE validation earlier but safe to handle
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
			return nil, renderErr
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
				currentResult, err = builtin.Execute(ctx, args, IterationData{
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
			currentResult, err = runShellStep(ctx, renderedStep, task.Index)
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
			activeInfo := ""
			if opts.ActiveCount != nil {
				activeInfo = fmt.Sprintf(" (active: %d)", opts.ActiveCount.Load())
			}
			fmt.Fprintf(opts.Stderr, "[%d/%d]%s %s   %s   %s\n",
				task.Index, opts.TotalTasks, activeInfo, renderedStep, durationStr, statusIndicator)

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
			if ctx.Err() != nil {
				return prevResult, ctx.Err()
			}
			return prevResult, err
		}
		prevResult = currentResult
	}

	return prevResult, nil
}

func runShellStep(ctx context.Context, command string, taskIndex int) (*Result, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
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
func RunJobPool(concurrency int, tasks <-chan Task, opts ExecOptions) (*ExecutionReport, error) {
	poolStart := time.Now()
	var wg sync.WaitGroup
	var successCount atomic.Int32
	var failCount atomic.Int32
	var timeoutCount int
	var errMu sync.Mutex
	var firstErr error
	var latencies []int64
	var latMu sync.Mutex

	var details []ExecutionDetail
	var detailsMu sync.Mutex

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

	interval := time.Duration(0)
	if opts.RampUp > 0 && concurrency > 1 {
		interval = opts.RampUp / time.Duration(concurrency-1)
	}

	for i := 0; i < concurrency; i++ {
		if i > 0 && interval > 0 {
			select {
			case <-opts.Context.Done():
				return nil, opts.Context.Err()
			case <-time.After(interval):
			}
		}

		wg.Add(1)
		workerID := i
		go func(id int) {
			defer wg.Done()
			if opts.ActiveCount != nil {
				opts.ActiveCount.Add(1)
				defer opts.ActiveCount.Add(-1)
			}
			for task := range tasks {
				if opts.Context != nil && opts.Context.Err() != nil {
					continue
				}

				task.WorkerID = id
				taskStart := time.Now()

				var taskOpts = opts
				var stdout, stderr strings.Builder
				if opts.Verbose {
					taskOpts.Stdout = &stdout
					taskOpts.Stderr = &stderr
					taskOpts.Silent = false // Enable writing so we can capture it
				}

				res, err := RunPipeline(task, taskOpts)
				duration := time.Since(taskStart)

				detail := ExecutionDetail{
					Index:      task.Index,
					WorkerID:   task.WorkerID,
					Status:     "success",
					Duration:   duration.Round(time.Microsecond).String(),
				}

				if err != nil {
					detail.Status = "fail"
					detail.Error = err.Error()

					var he *HTTPError
					if errors.As(err, &he) {
						detail.StatusCode = he.StatusCode
					}
				} else if res != nil && res.Metadata != nil {
					if sc, ok := res.Metadata["status_code"].(int); ok {
						detail.StatusCode = sc
					}
				}

				if opts.OnResult != nil {
					opts.OnResult(detail)
				}

				if opts.Verbose {
					detail.Output = stderr.String()
					if err != nil && stderr.Len() > 0 {
						if detail.Output != "" {
							detail.Output += "\n"
						}
						detail.Output += "Stderr: " + stderr.String()
					}
					detailsMu.Lock()
					details = append(details, detail)
					detailsMu.Unlock()

					// Flush the captured verbose output to the original Stderr so it shows up in CLI/text responses
					outputMu.Lock()
					if opts.Stderr != nil {
						opts.Stderr.Write([]byte(detail.Output))
						if !strings.HasSuffix(detail.Output, "\n") {
							opts.Stderr.Write([]byte("\n"))
						}
					}
					outputMu.Unlock()
				}

				if err != nil {
					failCount.Add(1)
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					if errors.Is(err, context.DeadlineExceeded) {
						timeoutCount++
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
					latMu.Lock()
					latencies = append(latencies, time.Since(taskStart).Nanoseconds())
					latMu.Unlock()
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
			fmt.Fprintf(stderr, "Rate: ~%.1f executions/sec", rate)
			if opts.RateLimit > 0 {
				fmt.Fprintf(stderr, " (target: %.1f/s)", opts.RateLimit)
			}
			fmt.Fprintln(stderr)
		}
		if timeoutCount > 0 {
			fmt.Fprintf(stderr, "Timeouts: %d\n", timeoutCount)
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

	sc := successCount.Load()
	fc := failCount.Load()

	report := &ExecutionReport{
		TotalExecutions: int(totalExecs),
		SuccessCount:    int(sc),
		FailCount:       int(fc),
		TimeoutCount:    timeoutCount,
		TotalDuration:   poolDuration.Round(time.Millisecond).String(),
		RatePerSecond:   rate,
		Details:         details,
	}

	// Capture aggregate output if builders were provided
	if opts.Stdout != nil {
		if sb, ok := opts.Stdout.(*strings.Builder); ok {
			s := sb.String()
			if s != "" {
				report.Stdout = strings.Split(strings.TrimRight(s, "\n"), "\n")
			}
		}
	}
	if opts.Stderr != nil {
		if sb, ok := opts.Stderr.(*strings.Builder); ok {
			s := sb.String()
			if s != "" {
				report.Stderr = strings.Split(strings.TrimRight(s, "\n"), "\n")
			}
		}
	}

	if len(latencies) > 0 {
		var totalLat int64
		for _, l := range latencies {
			totalLat += l
		}
		avgLat := time.Duration(totalLat / int64(len(latencies)))
		report.AverageLatency = avgLat.Round(time.Microsecond).String()

		p50, p90, p95, p99 := calculatePercentiles(latencies)
		report.P50Latency = p50.String()
		report.P90Latency = p90.String()
		report.P95Latency = p95.String()
		report.P99Latency = p99.String()
	}

	if opts.Report {
		if len(httpErrors) > 0 {
			report.HTTPErrors = make(map[string]int)
			for cat, hec := range httpErrors {
				report.HTTPErrors[cat] = hec.count
			}
		}
		if len(tcpErrors) > 0 {
			report.TCPErrors = make(map[string]int)
			for cat, tec := range tcpErrors {
				report.TCPErrors[cat] = tec.count
			}
		}
		if len(templateErrors) > 0 {
			report.TemplateErrors = make(map[string]int)
			for _, tec := range templateErrors {
				report.TemplateErrors[tec.err.Error()] = tec.count
			}
		}

		// Also print percentiles if report is true
		if len(latencies) > 0 {
			fmt.Fprintf(stderr, "Percentiles: p50=%s p90=%s p95=%s p99=%s\n",
				report.P50Latency, report.P90Latency, report.P95Latency, report.P99Latency)
		}
	}

	if opts.Context != nil && opts.Context.Err() != nil {
		if errors.Is(opts.Context.Err(), context.DeadlineExceeded) {
			return report, &ExitError{Code: 124, Err: fmt.Errorf("global timeout reached: %w", opts.Context.Err())}
		}
		return report, &ExitError{Code: 130, Err: opts.Context.Err()}
	}

	if fc > 0 && sc == 0 {
		return report, &ExitError{Code: 2, Err: firstErr}
	} else if fc > 0 && sc > 0 {
		return report, &ExitError{Code: 1, Err: firstErr}
	}

	return report, nil
}

func calculatePercentiles(latencies []int64) (p50, p90, p95, p99 time.Duration) {
	if len(latencies) == 0 {
		return 0, 0, 0, 0
	}
	sorted := make([]int64, len(latencies))
	copy(sorted, latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	getPercentile := func(p float64) time.Duration {
		idx := int(float64(len(sorted)-1) * p / 100.0)
		return time.Duration(sorted[idx])
	}

	return getPercentile(50), getPercentile(90), getPercentile(95), getPercentile(99)
}

