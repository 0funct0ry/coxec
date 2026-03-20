package e2e

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDelayFlag(t *testing.T) {
	// binPath is defined and compiled in e2e_test.go's TestMain

	// Test case: 3 iterations with 200ms delay.
	// Total time should be at least (3-1) * 200ms = 400ms.
	cmd := exec.Command(binPath, "-n", "3", "-c", "2", "--delay", "200ms", "-e", "echo ok")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	
	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("coxec failed: %v, stderr: %s", err, stderr.String())
	}

	if duration < 400*time.Millisecond {
		t.Errorf("expected duration to be at least 400ms, got %v", duration)
	}

	// Also check that it doesn't take TOO long (threshold 1000ms)
	if duration > 1000*time.Millisecond {
		t.Errorf("expected duration to be around 400-600ms, got %v", duration)
	}
}

func TestDelaySpacing(t *testing.T) {
	// Use a template to output Unix timestamps in milliseconds
	cmd := exec.Command(binPath, "-n", "3", "-c", "1", "--delay", "300ms", "-e", "echo {{.TimestampUnixMilli}}")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	
	err := cmd.Run()
	if err != nil {
		t.Fatalf("coxec failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	lines := strings.Split(output, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines of output, got %d. Output: %q", len(lines), output)
	}

	var ts []int64
	for _, line := range lines {
		val, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
		if err != nil {
			t.Fatalf("failed to parse timestamp %q: %v", line, err)
		}
		ts = append(ts, val)
	}
	
	// Check deltas (should be ~300ms)
	delta1 := ts[1] - ts[0]
	delta2 := ts[2] - ts[1]

	// Allow some slack for execution time and scheduling (e.g., 280ms to 500ms)
	if delta1 < 280 || delta1 > 500 {
		t.Errorf("expected delta1 to be ~300ms, got %dms", delta1)
	}
	if delta2 < 280 || delta2 > 500 {
		t.Errorf("expected delta2 to be ~300ms, got %dms", delta2)
	}
}
