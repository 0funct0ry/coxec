package e2e

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestJitterRange(t *testing.T) {
	// Test case: 5 iterations with 500ms delay and 200ms jitter.
	// Expected intervals: [300ms, 700ms]
	// We'll run it enough times to see some variation but not too many to be slow.
	cmd := exec.Command(binPath, "-n", "5", "-c", "1", "--delay", "500ms", "--jitter", "200ms", "-e", "echo {{.TimestampUnixMilli}}")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	
	err := cmd.Run()
	if err != nil {
		t.Fatalf("coxec failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	lines := strings.Split(output, "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines of output, got %d. Output: %q", len(lines), output)
	}

	var ts []int64
	for _, line := range lines {
		val, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
		if err != nil {
			t.Fatalf("failed to parse timestamp %q: %v", line, err)
		}
		ts = append(ts, val)
	}
	
	for i := 1; i < len(ts); i++ {
		delta := ts[i] - ts[i-1]
		// Allow some slack for execution time (e.g., up to 100ms extra)
		// Range: [300ms, 700ms + slack]
		if delta < 280 { // a bit less than 300 to be safe
			t.Errorf("interval %d too short: %dms (expected >= 300ms)", i, delta)
		}
		if delta > 850 { // 700 + 150ms slack
			t.Errorf("interval %d too long: %dms (expected <= ~700ms)", i, delta)
		}
	}
}

func TestJitterNoNegative(t *testing.T) {
	// Test case: delay 10ms, jitter 100ms.
	// Even though 10 - 100 = -90, the applied delay should be >= 0.
	// This should run very fast.
	cmd := exec.Command(binPath, "-n", "10", "-c", "1", "--delay", "10ms", "--jitter", "100ms", "-e", "echo hi")
	err := cmd.Run()
	if err != nil {
		t.Fatalf("coxec failed with extreme jitter: %v", err)
	}
}
