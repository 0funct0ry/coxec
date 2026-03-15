package engine

import (
	"os"
	"testing"
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
