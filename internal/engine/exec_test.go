package engine_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/0funct0ry/coxec/internal/engine"
	"regexp"
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

func TestRunShellCommand_Silent(t *testing.T) {
	var stdout, stderr bytes.Buffer
	opts := engine.ExecOptions{
		Silent:     true,
		TotalTasks: 1,
		Stdout:     &stdout,
		Stderr:     &stderr,
	}
	task := engine.Task{Index: 1, Command: "echo 'hello world'"}

	err := engine.RunPipeline(task, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if stdout.Len() > 0 {
		t.Errorf("expected no stdout, got %q", stdout.String())
	}
}

func TestRunShellCommand_VerboseSilent(t *testing.T) {
	var stdout, stderr bytes.Buffer
	opts := engine.ExecOptions{
		Verbose:    true,
		Silent:     true,
		TotalTasks: 1,
		Stdout:     &stdout,
		Stderr:     &stderr,
	}
	task := engine.Task{Index: 1, Command: "echo 'hello world'"}

	err := engine.RunPipeline(task, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if stdout.Len() > 0 {
		t.Errorf("expected no stdout, got %q", stdout.String())
	}

	stderrOut := stderr.String()
	if !strings.Contains(stderrOut, "[1/1] echo 'hello world'") {
		t.Errorf("expected verbose metadata in stderr, got %q", stderrOut)
	}
	if strings.Contains(stderrOut, "stdout: hello world") {
		t.Errorf("expected no command output in stderr, got %q", stderrOut)
	}
}

func TestRunShellCommand_TemplateContext(t *testing.T) {
	var stdout bytes.Buffer
	opts := engine.ExecOptions{
		TotalTasks: 1,
		Stdout:     &stdout,
	}
	task := engine.Task{Index: 5, WorkerID: 3, Command: "echo it:{{.Iteration}} wrk:{{.WorkerID}}"}

	err := engine.RunPipeline(task, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := "it:4 wrk:3\n"
	if stdout.String() != expected {
		t.Errorf("expected %q, got %q", expected, stdout.String())
	}
}

func TestRunShellCommand_UserVarsWithCommas(t *testing.T) {
	var stdout bytes.Buffer
	opts := engine.ExecOptions{
		TotalTasks: 1,
		Stdout:     &stdout,
		UserVars: map[string]string{
			"filter": "status=active,priority>=3",
		},
	}
	task := engine.Task{Index: 1, Command: "echo {{.Var \"filter\" | quote}}"}

	err := engine.RunPipeline(task, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := "status=active,priority>=3\n"
	if stdout.String() != expected {
		t.Errorf("expected %q, got %q", expected, stdout.String())
	}
}

func TestRunShellCommand_QuoteInjection(t *testing.T) {
	var stdout bytes.Buffer
	opts := engine.ExecOptions{
		TotalTasks: 1,
		Stdout:     &stdout,
		UserVars: map[string]string{
			"payload": "'; rm -rf /; '",
		},
	}
	task := engine.Task{Index: 1, Command: "echo {{.Var \"payload\" | quote}}"}

	err := engine.RunPipeline(task, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// The output should be the literal string, safely quoted for sh -c
	expected := "'; rm -rf /; '\n"
	if stdout.String() != expected {
		t.Errorf("expected %q, got %q", expected, stdout.String())
	}
}

func TestRunShellCommand_AdvancedContext(t *testing.T) {
	var stdout bytes.Buffer
	opts := engine.ExecOptions{
		TotalTasks: 1,
		Stdout:     &stdout,
	}

	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	uuid := "550e8400-e29b-41d4-a716-446655440000"
	task := engine.Task{
		Index:     1,
		Command:   "echo ts:{{.Timestamp}} unix:{{.TimestampUnix}} milli:{{.TimestampUnixMilli}} nano:{{.TimestampUnixNano}} uuid:{{.UUID}}",
		Timestamp: ts,
		UUID:      uuid,
	}

	err := engine.RunPipeline(task, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	output := stdout.String()
	// RFC3339 for UTC might be Z or +00:00. We now use millisecond precision.
	const rfc3339Milli = "2006-01-02T15:04:05.000Z07:00"
	expectedTS := ts.Format(rfc3339Milli)
	if !strings.Contains(output, "ts:"+expectedTS) {
		t.Errorf("expected timestamp %q in output, got %q", expectedTS, output)
	}

	expectedUnix := "1704110400"
	if !strings.Contains(output, "unix:"+expectedUnix) {
		t.Errorf("expected unix timestamp %q in output, got %q", expectedUnix, output)
	}

	expectedMilli := "1704110400000"
	if !strings.Contains(output, "milli:"+expectedMilli) {
		t.Errorf("expected unix milli timestamp %q in output, got %q", expectedMilli, output)
	}

	expectedNano := "1704110400000000000"
	if !strings.Contains(output, "nano:"+expectedNano) {
		t.Errorf("expected unix nano timestamp %q in output, got %q", expectedNano, output)
	}

	if !strings.Contains(output, "uuid:"+uuid) {
		t.Errorf("expected uuid %q in output, got %q", uuid, output)
	}
}

func TestRunShellCommand_ImplicitGeneration(t *testing.T) {
	var stdout bytes.Buffer
	opts := engine.ExecOptions{
		TotalTasks: 1,
		Stdout:     &stdout,
	}

	task := engine.Task{
		Index:   1,
		Command: "echo ts:{{.Timestamp}} uuid:{{.UUID}}",
	}

	err := engine.RunPipeline(task, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	output := stdout.String()
	// Check for millisecond precision (e.g., .000)
	if !regexp.MustCompile(`ts:\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}`).MatchString(output) {
		t.Errorf("output does not contain timestamp with milliseconds: %q", output)
	}

	// Check for UUID format
	if !regexp.MustCompile(`uuid:[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}`).MatchString(output) {
		t.Errorf("output does not contain valid UUID: %q", output)
	}
}

func TestUUIDFormat(t *testing.T) {
	// This tests the regex for UUID v4 format as generated by our helper (though helper is in cli, we can test it implicitly if we wanted, 
	// but here we just verify the requirement of v4 UUID format)
	uuidRegex := regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
	
	testUUID := "550e8400-e29b-41d4-a716-446655440000"
	if !uuidRegex.MatchString(testUUID) {
		t.Errorf("expected %q to match UUID v4 format", testUUID)
	}
}
func TestRunPipeline_BuiltinVsShell(t *testing.T) {
	registry := engine.NewBuiltinRegistry()
	registry.Register(engine.NewSleepClient()) // Registered as .sleep

	var stdout, stderr bytes.Buffer
	opts := engine.ExecOptions{
		Registry:      registry,
		Stdout:        &stdout,
		Stderr:        &stderr,
		TotalTasks:    1,
		TemplateState: engine.NewTemplateState(),
	}

	// 1. Dotted name should use built-in. .sleep is transparent, so no stdout.
	taskBuiltin := engine.Task{Index: 1, Command: ".sleep 10ms"}
	err := engine.RunPipeline(taskBuiltin, opts)
	if err != nil {
		t.Fatalf("expected no error from .sleep built-in, got %v", err)
	}
	if stdout.Len() > 0 {
		t.Errorf("expected no stdout from .sleep built-in, got %q", stdout.String())
	}

	// 2. Unprefixed 'sleep' should fall through to shell. Shell 'sleep' produces no stdout but takes time.
	// To verify it's shell, we can use a shell-specific command or check if it actually executed.
	// Actually, if it falls through to shell, it will execute `sh -c 'sleep 0.01'`.
	taskShell := engine.Task{Index: 1, Command: "sleep 0.01"}
	start := time.Now()
	err = engine.RunPipeline(taskShell, opts)
	if err != nil {
		t.Fatalf("expected no error from shell sleep fallthrough, got %v", err)
	}
	duration := time.Since(start)
	if duration < 10*time.Millisecond {
		t.Errorf("expected shell sleep to take at least 10ms, took %v", duration)
	}
}
