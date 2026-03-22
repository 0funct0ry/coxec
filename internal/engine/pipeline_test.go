package engine

import (
	"reflect"
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantCmd  string
		wantArgs []string
	}{
		{
			name:     "basic command",
			input:    "echo hello",
			wantCmd:  "echo",
			wantArgs: []string{"hello"},
		},
		{
			name:     "double quotes with space",
			input:    `echo "hello world"`,
			wantCmd:  "echo",
			wantArgs: []string{"hello world"},
		},
		{
			name:     "single quotes with space",
			input:    "echo 'hello world'",
			wantCmd:  "echo",
			wantArgs: []string{"hello world"},
		},
		{
			name:     "nested quotes in double",
			input:    `echo '{"id": "123"}'`,
			wantCmd:  "echo",
			wantArgs: []string{`{"id": "123"}`},
		},
		{
			name:     "nested quotes in single",
			input:    `.http POST http://example.com --body '{"id": "123"}'`,
			wantCmd:  ".http",
			wantArgs: []string{"POST", "http://example.com", "--body", `{"id": "123"}`},
		},
		{
			name:     "escaped space",
			input:    `echo hello\ world`,
			wantCmd:  "echo",
			wantArgs: []string{"hello world"},
		},
		{
			name:     "multiple spaces",
			input:    "  echo   args  ",
			wantCmd:  "echo",
			wantArgs: []string{"args"},
		},
		{
			name:     "unquoted JSON body",
			input:    `.http POST http://example.com --body {"id": 123}`,
			wantCmd:  ".http",
			wantArgs: []string{"POST", "http://example.com", "--body", `{"id": 123}`},
		},
		{
			name:     "JSON body with template expression",
			input:    `.http POST http://example.com --body {"id": {{.Iteration}}}`,
			wantCmd:  ".http",
			wantArgs: []string{"POST", "http://example.com", "--body", `{"id": {{.Iteration}}}`},
		},
		{
			name:     "empty input",
			input:    "",
			wantCmd:  "",
			wantArgs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmd, gotArgs := ParseCommand(tt.input)
			if gotCmd != tt.wantCmd {
				t.Errorf("ParseCommand() gotCmd = %v, want %v", gotCmd, tt.wantCmd)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Errorf("ParseCommand() gotArgs = %v, want %v", gotArgs, tt.wantArgs)
			}
		})
	}
}
