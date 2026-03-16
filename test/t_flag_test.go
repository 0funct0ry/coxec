package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

type StructuredError struct {
	Error struct {
		Code       string `json:"code"`
		Message    string `json:"message"`
		Suggestion string `json:"suggestion"`
	} `json:"error"`
}

func TestTFlag(t *testing.T) {
	// Build binary if not exists
	binary := "../bin/coxec"
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		t.Skip("binary not found at ../bin/coxec, run make build first")
	}

	t.Run("Basic -t execution", func(t *testing.T) {
		tmpl := "echo hello"
		tmpFile, _ := os.CreateTemp("", "test-*.tmpl")
		defer os.Remove(tmpFile.Name())
		tmpFile.WriteString(tmpl)
		tmpFile.Close()

		cmd := exec.Command(binary, "-c", "1", "-n", "1", "-t", tmpFile.Name())
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("expected success, got %v", err)
		}
		if strings.TrimSpace(out.String()) != "hello" {
			t.Errorf("expected 'hello', got %q", out.String())
		}
	})

	t.Run("Multi-step pipeline", func(t *testing.T) {
		tmpl := "echo step1 |> echo step2"
		tmpFile, _ := os.CreateTemp("", "test-*.tmpl")
		defer os.Remove(tmpFile.Name())
		tmpFile.WriteString(tmpl)
		tmpFile.Close()

		cmd := exec.Command(binary, "-c", "1", "-n", "1", "-t", tmpFile.Name())
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("expected success, got %v", err)
		}
		expected := "step1\nstep2"
		if strings.TrimSpace(out.String()) != expected {
			t.Errorf("expected %q, got %q", expected, out.String())
		}
	})

	t.Run("Mutual exclusivity -e and -t", func(t *testing.T) {
		cmd := exec.Command(binary, "-e", "echo 1", "-t", "plan.tmpl", "--json")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var se StructuredError
		if err := json.Unmarshal(stderr.Bytes(), &se); err != nil {
			t.Fatalf("failed to parse structured error: %v\nOutput: %s", err, stderr.String())
		}

		if se.Error.Code != "INVALID_ARGS" {
			t.Errorf("expected INVALID_ARGS, got %s", se.Error.Code)
		}
		if !strings.Contains(se.Error.Message, "exclusive") {
			t.Errorf("unexpected error message: %s", se.Error.Message)
		}
	})

	t.Run("Default plain text error", func(t *testing.T) {
		cmd := exec.Command(binary, "-e", "echo 1", "-t", "plan.tmpl")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		got := stderr.String()
		if !strings.Contains(got, "Error: flags -e and -t are mutually exclusive") {
			t.Errorf("expected plain text error, got %q", got)
		}
		if !strings.Contains(got, "Suggestion:") {
			t.Errorf("expected suggestion in plain text error, got %q", got)
		}
	})

	t.Run("File not found (JSON)", func(t *testing.T) {
		cmd := exec.Command(binary, "-t", "non-existent-template-file.tmpl", "--json")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var se StructuredError
		if err := json.Unmarshal(stderr.Bytes(), &se); err != nil {
			t.Fatalf("failed to parse structured error: %v\nOutput: %s", err, stderr.String())
		}

		if se.Error.Code != "FILE_NOT_FOUND" {
			t.Errorf("expected FILE_NOT_FOUND, got %s", se.Error.Code)
		}
	})

	t.Run("Invalid template syntax (JSON)", func(t *testing.T) {
		tmpl := "{{invalid"
		tmpFile, _ := os.CreateTemp("", "test-*.tmpl")
		defer os.Remove(tmpFile.Name())
		tmpFile.WriteString(tmpl)
		tmpFile.Close()

		cmd := exec.Command(binary, "-t", tmpFile.Name(), "--json")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var se StructuredError
		if err := json.Unmarshal(stderr.Bytes(), &se); err != nil {
			t.Fatalf("failed to parse structured error: %v\nOutput: %s", err, stderr.String())
		}

		if se.Error.Code != "INVALID_TEMPLATE" {
			t.Errorf("expected INVALID_TEMPLATE, got %s", se.Error.Code)
		}
	})
}
