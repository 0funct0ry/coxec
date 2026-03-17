package engine

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
)

func TestIterationData_EnvVar(t *testing.T) {
	// Set up environment variable for testing
	os.Setenv("COXEC_TEST_ENV", "env_value")
	defer os.Unsetenv("COXEC_TEST_ENV")

	tests := []struct {
		name     string
		data     IterationData
		template string
		expected string
	}{
		{
			name: ".Env exists",
			data: IterationData{},
			template: `{{.Env "COXEC_TEST_ENV"}}`,
			expected: "env_value",
		},
		{
			name: ".Env missing",
			data: IterationData{},
			template: `{{.Env "MISSING_ENV"}}`,
			expected: "",
		},
		{
			name: ".Var exists",
			data: IterationData{
				UserVars: map[string]string{"tenant": "acme"},
			},
			template: `{{.Var "tenant"}}`,
			expected: "acme",
		},
		{
			name: ".Var missing, Env fallback exists",
			data: IterationData{},
			template: `{{.Var "COXEC_TEST_ENV"}}`,
			expected: "env_value",
		},
		{
			name: ".Var precedence over Env",
			data: IterationData{
				UserVars: map[string]string{"COXEC_TEST_ENV": "var_value"},
			},
			template: `{{.Var "COXEC_TEST_ENV"}}`,
			expected: "var_value",
		},
		{
			name: ".Var missing, Env missing",
			data: IterationData{},
			template: `{{.Var "MISSING_VAR"}}`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := renderTemplate("test", tt.template, tt.data)
			if err != nil {
				t.Fatalf("renderTemplate failed: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestRandomFunctions(t *testing.T) {
	tests := []struct {
		name     string
		template string
		validate func(t *testing.T, result string)
	}{
		{
			name:     "randInt range",
			template: `{{randInt 10 20}}`,
			validate: func(t *testing.T, result string) {
				var val int
				fmt.Sscanf(result, "%d", &val)
				if val < 10 || val > 20 {
					t.Errorf("randInt result %d out of range [10, 20]", val)
				}
			},
		},
		{
			name:     "randFloat range and precision",
			template: `{{randFloat 1.5 2.5 3}}`,
			validate: func(t *testing.T, result string) {
				var val float64
				fmt.Sscanf(result, "%f", &val)
				if val < 1.5 || val > 2.5 {
					t.Errorf("randFloat result %f out of range [1.5, 2.5]", val)
				}
				parts := strings.Split(result, ".")
				if len(parts) != 2 || len(parts[1]) != 3 {
					t.Errorf("randFloat result %s has wrong precision", result)
				}
			},
		},
		{
			name:     "randString length",
			template: `{{randString 15}}`,
			validate: func(t *testing.T, result string) {
				if len(result) != 15 {
					t.Errorf("randString result length %d, expected 15", len(result))
				}
				for _, r := range result {
					if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
						t.Errorf("randString result %s contains invalid character %c", result, r)
					}
				}
			},
		},
		{
			name:     "randChoice selection",
			template: `{{randChoice "foo" "bar" "baz"}}`,
			validate: func(t *testing.T, result string) {
				choices := map[string]bool{"foo": true, "bar": true, "baz": true}
				if !choices[result] {
					t.Errorf("randChoice result %s not in choices", result)
				}
			},
		},
		{
			name:     "uuid format",
			template: `{{uuid}}`,
			validate: func(t *testing.T, result string) {
				if _, err := uuid.Parse(result); err != nil {
					t.Errorf("uuid result %s is not a valid UUID: %v", result, err)
				}
			},
		},
		{
			name:     "ulid format",
			template: `{{ulid}}`,
			validate: func(t *testing.T, result string) {
				if _, err := ulid.Parse(result); err != nil {
					t.Errorf("ulid result %s is not a valid ULID: %v", result, err)
				}
			},
		},
		{
			name:     "randName plausibility",
			template: `{{randName}}`,
			validate: func(t *testing.T, result string) {
				parts := strings.Split(result, " ")
				if len(parts) != 2 {
					t.Errorf("randName result %s does not look like a full name", result)
				}
			},
		},
		{
			name:     "randEmail format",
			template: `{{randEmail}}`,
			validate: func(t *testing.T, result string) {
				if !strings.Contains(result, "@") || !strings.Contains(result, ".") {
					t.Errorf("randEmail result %s is not a valid email", result)
				}
			},
		},
		{
			name:     "randPhone format",
			template: `{{randPhone}}`,
			validate: func(t *testing.T, result string) {
				if !strings.HasPrefix(result, "+1-") {
					t.Errorf("randPhone result %s has wrong prefix", result)
				}
				parts := strings.Split(result, "-")
				if len(parts) != 4 {
					t.Errorf("randPhone result %s has wrong format", result)
				}
			},
		},
		{
			name:     "different values in one iteration",
			template: `{{randInt 1 1000000}} {{randInt 1 1000000}}`,
			validate: func(t *testing.T, result string) {
				parts := strings.Fields(result)
				if len(parts) != 2 {
					t.Fatalf("expected 2 parts, got %d", len(parts))
				}
				if parts[0] == parts[1] {
					t.Errorf("expected different values, got %s and %s (probabilistically unlikely)", parts[0], parts[1])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := renderTemplate("test", tt.template, IterationData{})
			if err != nil {
				t.Fatalf("renderTemplate failed: %v", err)
			}
			tt.validate(t, result)
		})
	}
}
