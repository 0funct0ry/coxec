package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binPath string

// TestMain compiles the coxec binary for e2e tests
func TestMain(m *testing.M) {
	// Create a temporary directory for the binary
	tmpDir, err := os.MkdirTemp("", "coxec-e2e-*")
	if err != nil {
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	binPath = filepath.Join(tmpDir, "coxec")

	// Compile the binary
	cmd := exec.Command("go", "build", "-o", binPath, "./main.go")
	cmd.Dir = ".." // Run from the root directory
	if out, err := cmd.CombinedOutput(); err != nil {
		println("Failed to compile coxec:", string(out))
		os.Exit(1)
	}

	// Run the tests
	os.Exit(m.Run())
}

func TestSingleShellCommand(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		expectedStdout string
		expectedStderr string
		expectedExit   int
	}{
		{
			name:           "Simple echo",
			args:           []string{"-e", "echo hello world"},
			expectedStdout: "hello world\n",
			expectedExit:   0,
		},
		{
			name:           "Shell math",
			args:           []string{"-e", "echo $((2+3))"},
			expectedStdout: "5\n",
			expectedExit:   0,
		},
		{
			name:           "Missing execution flags",
			args:           []string{}, // no flags
			expectedStderr: "Error: must provide one of -e, -f, or -t",
			expectedExit:   1,
		},
		{
			name:           "Command failure propagates exit code",
			args:           []string{"-e", "exit 42"},
			expectedExit:   42,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binPath, tc.args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err := cmd.Run()

			// Check exit code
			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					t.Fatalf("Unexpected error type: %v", err)
				}
			}

			if exitCode != tc.expectedExit {
				t.Errorf("Expected exit code %d, got %d. Stderr: %s", tc.expectedExit, exitCode, stderr.String())
			}

			// Check stdout
			if tc.expectedStdout != "" {
				if stdout.String() != tc.expectedStdout {
					t.Errorf("Expected stdout %q, got %q", tc.expectedStdout, stdout.String())
				}
			}

			// Check stderr
			if tc.expectedStderr != "" {
				if !strings.Contains(stderr.String(), tc.expectedStderr) {
					t.Errorf("Expected stderr to contain %q, got %q", tc.expectedStderr, stderr.String())
				}
			}
		})
	}
}
