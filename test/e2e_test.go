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
			expectedStderr: "No execution source specified. Use one of -e, -f, or -t",
			expectedExit:   64,
		},
		{
			name:         "All commands fail",
			args:         []string{"-e", "exit 42", "-c", "2"},
			expectedExit: 2,
		},
		{
			name:         "Partial command failure",
			args:         []string{"-e", "[[ $COXEC_INDEX -eq 1 ]] && exit 0 || exit 1", "-c", "2", "-n", "2"},
			expectedExit: 1,
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

func TestFileFlag(t *testing.T) {
	// Create temp directory for script
	tmpDir, err := os.MkdirTemp("", "coxec-file-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Simple script
	scriptPath := filepath.Join(tmpDir, "myscript.sh")
	scriptContent := `echo hello from script
echo "COXEC_INDEX=$COXEC_INDEX"
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	tests := []struct {
		name           string
		args           []string
		expectedStdout string
		expectedStderr string
		expectedExit   int
	}{
		{
			name:           "Execute script file with relative path",
			args:           []string{"-f", scriptPath, "-n", "1"},
			expectedStdout: "hello from script\nCOXEC_INDEX=1\n",
			expectedExit:   0,
		},
		{
			name:           "Execute script file with -c and -n",
			args:           []string{"-f", scriptPath, "-c", "4", "-n", "20"},
			expectedStdout: "hello from script", // 20 runs; just verify we get output
			expectedExit:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binPath, tc.args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err := cmd.Run()
			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					t.Fatalf("Unexpected error: %v", err)
				}
			}

			if exitCode != tc.expectedExit {
				t.Errorf("Expected exit %d, got %d. Stderr: %s", tc.expectedExit, exitCode, stderr.String())
			}
			if tc.expectedStdout != "" && !strings.Contains(stdout.String(), tc.expectedStdout) {
				t.Errorf("Expected stdout to contain %q, got: %s", tc.expectedStdout, stdout.String())
			}
			if tc.expectedStderr != "" && !strings.Contains(stderr.String(), tc.expectedStderr) {
				t.Errorf("Expected stderr to contain %q, got %q", tc.expectedStderr, stderr.String())
			}
		})
	}
}

func TestFileFlag_FileNotFound(t *testing.T) {
	cmd := exec.Command(binPath, "-f", "./missing.sh", "-n", "1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()

	got := stderr.String()
	if !strings.Contains(got, "Error: script file not found") {
		t.Errorf("Expected 'Error: script file not found' in stderr, got %q", got)
	}
	if !strings.Contains(got, "missing.sh") {
		t.Errorf("Expected path in error, got %q", got)
	}
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
	}
	if exitCode != 64 {
		t.Errorf("Expected exit 64, got %d", exitCode)
	}
}

func TestFileFlag_NonExecutableFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "coxec-file-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "noexec.sh")
	if err := os.WriteFile(scriptPath, []byte("echo works\n"), 0644); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	cmd := exec.Command(binPath, "-f", scriptPath, "-n", "1")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err = cmd.Run()
	if err != nil {
		t.Fatalf("Expected success with non-executable script (chmod 644), got %v", err)
	}
	if !strings.Contains(stdout.String(), "works") {
		t.Errorf("Expected script output, got %q", stdout.String())
	}
}

func TestFileFlag_ScriptExitsNonZero(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "coxec-file-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "fail.sh")
	if err := os.WriteFile(scriptPath, []byte("exit 42\n"), 0644); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	cmd := exec.Command(binPath, "-f", scriptPath, "-n", "1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		t.Fatal("Expected non-zero exit when script exits 42")
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("Expected ExitError, got %T", err)
	}
	if ee.ExitCode() != 2 {
		t.Errorf("Expected exit 2 (all failed), got %d", ee.ExitCode())
	}
	if !strings.Contains(stderr.String(), "Failed: 1") {
		t.Errorf("Expected summary with Failed: 1, got stderr: %s", stderr.String())
	}
}

func TestExecutionSourceRule_MutualExclusivity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "coxec-file-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	scriptPath := filepath.Join(tmpDir, "s.sh")
	_ = os.WriteFile(scriptPath, []byte("echo x\n"), 0644)

	tests := []struct {
		name         string
		args         []string
		wantStderr   string
		expectedExit int
	}{
		{
			name:         "-e and -f",
			args:         []string{"-e", "echo x", "-f", scriptPath, "-n", "1"},
			wantStderr:   "cannot use both -e / --exec and -f / --file at the same time",
			expectedExit: 64,
		},
		{
			name:         "-e and -t",
			args:         []string{"-e", "echo x", "-t", "/tmp/t.tpl", "-n", "1"},
			wantStderr:   "cannot use both -e / --exec and -t / --template at the same time",
			expectedExit: 64,
		},
		{
			name:         "-f and -t",
			args:         []string{"-f", scriptPath, "-t", "/tmp/t.tpl", "-n", "1"},
			wantStderr:   "cannot use both -f / --file and -t / --template at the same time",
			expectedExit: 64,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binPath, tc.args...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			err := cmd.Run()

			got := stderr.String()
			if !strings.Contains(got, tc.wantStderr) {
				t.Errorf("Expected stderr to contain %q, got %q", tc.wantStderr, got)
			}
			if !strings.Contains(got, "Only one execution source is allowed.") {
				t.Errorf("Expected 'Only one execution source is allowed.' in stderr, got %q", got)
			}

			exitCode := 0
			if err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					exitCode = ee.ExitCode()
				}
			}
			if exitCode != tc.expectedExit {
				t.Errorf("Expected exit %d, got %d", tc.expectedExit, exitCode)
			}
		})
	}
}

func TestHelpContainsExecutionSource(t *testing.T) {
	cmd := exec.Command(binPath, "--help")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("--help should succeed: %v", err)
	}
	got := out.String()
	want := []string{
		"Execution source (exactly one required)",
		"-e, --exec string",
		"Shell command or built-in to execute repeatedly",
		"-f, --file string",
		"Path to shell script file to execute repeatedly",
		"-t, --template string",
		"Path to Go template file defining the execution plan",
	}
	for _, s := range want {
		if !strings.Contains(got, s) {
			t.Errorf("Expected --help to contain %q, got:\n%s", s, got)
		}
	}
}
