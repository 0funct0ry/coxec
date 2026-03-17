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
			result, err := renderTemplate("test", tt.template, tt.data, nil)
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
			result, err := renderTemplate("test", tt.template, IterationData{}, nil)
			if err != nil {
				t.Fatalf("renderTemplate failed: %v", err)
			}
			tt.validate(t, result)
		})
	}
}

func TestDataFunctions(t *testing.T) {
	// Create a temporary file for testing fileLine and fileLineAt
	tmpfile, err := os.CreateTemp("", "coxec_test_data.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	content := "line1\nline2\nline3"
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		iteration int
		template  string
		expected  string
	}{
		{
			name:      "seq ascending",
			iteration: 2,
			template:  `{{seq 1 10 2}}`,
			expected:  "5", // 1 + (2 * 2) = 5
		},
		{
			name:      "seq upper bound",
			iteration: 5,
			template:  `{{seq 1 10 3}}`,
			expected:  "10", // 1 + (5 * 3) = 16 -> 10
		},
		{
			name:      "counter increments",
			iteration: 0,
			template:  `{{counter "a"}} {{counter "a"}}`,
			expected:  "1 2",
		},
		{
			name:      "named counters are separate",
			iteration: 0,
			template:  `{{counter "b"}} {{counter "c"}}`,
			expected:  "1 1",
		},
		{
			name:      "fileLineAt valid",
			iteration: 0,
			template:  fmt.Sprintf(`{{fileLineAt %q 2}}`, tmpfile.Name()),
			expected:  "line2",
		},
		{
			name:      "fileLineAt out of range",
			iteration: 0,
			template:  fmt.Sprintf(`{{fileLineAt %q 10}}`, tmpfile.Name()),
			expected:  "",
		},
		{
			name:      "fileLine sequential",
			iteration: 0,
			template:  fmt.Sprintf(`{{fileLine %q}} {{fileLine %q}}`, tmpfile.Name(), tmpfile.Name()),
			expected:  "line1 line2",
		},
		{
			name:      "fileLine wraps around",
			iteration: 0,
			template:  fmt.Sprintf(`{{fileLine %q}} {{fileLine %q}} {{fileLine %q}} {{fileLine %q}}`, tmpfile.Name(), tmpfile.Name(), tmpfile.Name(), tmpfile.Name()),
			expected:  "line1 line2 line3 line1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := NewTemplateState()
			data := IterationData{Iteration: tt.iteration}
			result, err := renderTemplate("test", tt.template, data, state)
			if err != nil {
				t.Fatalf("renderTemplate failed: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestEncodingFunctions(t *testing.T) {
	tests := []struct {
		name     string
		data     IterationData
		template string
		expected string
	}{
		{
			name:     "base64Enc hello",
			template: `{{base64Enc "hello"}}`,
			expected: "aGVsbG8=",
		},
		{
			name:     "base64Dec aGVsbG8=",
			template: `{{base64Dec "aGVsbG8="}}`,
			expected: "hello",
		},
		{
			name:     "base64Dec invalid input returns empty",
			template: `{{base64Dec "!!!invalid"}}`,
			expected: "",
		},
		{
			name:     "sha256 secret",
			template: `{{sha256 "secret"}}`,
			expected: "2bb80d537b1da3e38bd30361aa855686bde0eacd7162fef6a25fe97bf527a25b",
		},
		{
			name:     "hmac sha256",
			template: `{{hmac "sha256" "key" "message"}}`,
			expected: "6e9ef29b75fffc5b7abae527d58fdadb2fe42e7219011976917343065f58ed4a",
		},
		{
			name:     "hmac unsupported algo returns empty",
			template: `{{hmac "md5" "key" "msg"}}`,
			expected: "",
		},
		{
			name:     "urlEncode spaces and special chars",
			template: `{{urlEncode "a b&c"}}`,
			expected: "a+b%26c",
		},
// toJSON string
		{
			name:     "toJSON string",
			template: `{{toJSON "hello"}}`,
			expected: `"hello"`,
		},
		{
			name: "toJSON map via UserVars",
			data: IterationData{
				UserVars: map[string]string{"k": "v"},
			},
			template: `{{toJSON .UserVars}}`,
			expected: `{"k":"v"}`,
		},
		{
			name:     "jsonEncode string value",
			template: `{{jsonEncode "hello world"}}`,
			expected: `"hello world"`,
		},
		{
			name:     "jsonEncode integer",
			template: `{{jsonEncode 42}}`,
			expected: `42`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := renderTemplate("test", tt.template, tt.data, nil)
			if err != nil {
				t.Fatalf("renderTemplate failed: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestUtilityFunctions(t *testing.T) {
	// Setup env var for 'env' test
	os.Setenv("COXEC_UTIL_ENV", "util_val")
	defer os.Unsetenv("COXEC_UTIL_ENV")

	tests := []struct {
		name        string
		template    string
		expected    string
		expectError bool
		validate    func(t *testing.T, result string)
	}{
		{
			name:     "add basic",
			template: `{{add 5 10}}`,
			expected: "15",
		},
		{
			name:     "add negative",
			template: `{{add -5 10}}`,
			expected: "5",
		},
		{
			name:     "mod normal",
			template: `{{mod 17 5}}`,
			expected: "2",
		},
		{
			name:        "mod zero panics",
			template:    `{{mod 17 0}}`,
			expectError: true,
		},
		{
			name:     "regexReplace matching",
			template: `{{regexReplace "[0-9]+" "X" "item-42-price-100"}}`,
			expected: "item-X-price-X",
		},
		{
			name:     "regexReplace no match",
			template: `{{regexReplace "[0-9]+" "X" "item-price"}}`,
			expected: "item-price",
		},
		{
			name:        "regexReplace invalid regex",
			template:    `{{regexReplace "[" "X" "item"}}`,
			expectError: true,
		},
		{
			name:     "env existing",
			template: `{{env "COXEC_UTIL_ENV"}}`,
			expected: "util_val",
		},
		{
			name:     "env missing",
			template: `{{env "COXEC_MISSING_ENV"}}`,
			expected: "",
		},
		{
			name:     "split and index",
			template: `{{$arr := split "id,name,email" ","}}{{index $arr 1}}`,
			expected: "name",
		},
		{
			name:     "now default",
			template: `{{now}}`,
			validate: func(t *testing.T, result string) {
				if len(result) < 10 || !strings.Contains(result, "T") {
					t.Errorf("now result %s does not look like RFC3339", result)
				}
			},
		},
		{
			name:     "now custom format",
			template: `{{now "2006-01-02"}}`,
			validate: func(t *testing.T, result string) {
				if len(result) != 10 {
					t.Errorf("now custom result %s does not look like YYYY-MM-DD", result)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := renderTemplate("test", tt.template, IterationData{}, nil)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got result %q", result)
				}
				return
			}

			if err != nil {
				t.Fatalf("renderTemplate failed: %v", err)
			}

			if tt.validate != nil {
				tt.validate(t, result)
			} else if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
