package engine

import (
	"bytes"
	"strings"
	"testing"
	"text/template"
)

func TestWrapTemplateError(t *testing.T) {
	tests := []struct {
		name       string
		tpl        string
		tplName    string
		expectMsg  string
		expectLine int
		expectCol  int
		expectSug  bool
	}{
		{
			name:       "Parse error",
			tpl:        "{{undef}}",
			tplName:    "test",
			expectMsg:  "function \"undef\" not defined",
			expectLine: 1,
			expectSug:  true,
		},
		{
			name:       "Parse error multi-line",
			tpl:        "\n\n{{undef}}",
			tplName:    "test",
			expectMsg:  "function \"undef\" not defined",
			expectLine: 3,
			expectSug:  true,
		},
		{
			name:       "Execution error - Map key",
			tpl:        "{{.UserVars.missing}}",
			tplName:    "test",
			expectMsg:  "map has no entry for key \"missing\"",
			expectLine: 1,
			expectCol:  11,
			expectSug:  true,
		},
		{
			name:       "Execution error - Div zero",
			tpl:        "{{mod 5 0}}",
			tplName:    "test",
			expectMsg:  "modulo by zero",
			expectLine: 1,
			expectCol:  2,
			expectSug:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			tmpl := template.New(tt.tplName).Funcs(funcMap(IterationData{}, nil)).Option("missingkey=error")
			
			// Try parse
			_, err = tmpl.Parse(tt.tpl)
			if err == nil {
				// Try execute if parse succeeded
				data := IterationData{UserVars: map[string]string{}}
				var buf bytes.Buffer
				err = tmpl.Execute(&buf, data)
			}

			if err == nil {
				t.Fatalf("expected error but got nil")
			}

			wrapped := wrapTemplateError(err, tt.tplName, tt.tpl)
			te, ok := wrapped.(*TemplateError)
			if !ok {
				t.Fatalf("expected *TemplateError, got %T", wrapped)
			}

			if !strings.Contains(te.Message, tt.expectMsg) {
				t.Errorf("expected message to contain %q, got %q", tt.expectMsg, te.Message)
			}
			if te.Line != tt.expectLine {
				t.Errorf("expected line %d, got %d", tt.expectLine, te.Line)
			}
			if tt.expectCol > 0 && te.Column != tt.expectCol {
				t.Errorf("expected column %d, got %d", tt.expectCol, te.Column)
			}
			if tt.expectSug && te.Suggestion == "" {
				t.Errorf("expected suggestion but got empty")
			}
		})
	}
}

func TestRunJobPoolTemplateErrorDedup(t *testing.T) {
	var stderr bytes.Buffer
	opts := ExecOptions{
		Stderr:     &stderr,
		Stdout:     &bytes.Buffer{},
		TotalTasks: 5,
		Registry:   NewBuiltinRegistry(),
	}

	tasks := make(chan Task, 5)
	for i := 0; i < 5; i++ {
		tasks <- Task{Index: i + 1, Command: "{{.UserVars.missing}}"}
	}
	close(tasks)

	err := RunJobPool(2, tasks, opts)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	output := stderr.String()
	if !strings.Contains(output, "Template Errors:") {
		t.Errorf("expected output to contain 'Template Errors:', got %q", output)
	}
	if !strings.Contains(output, "(occurred 5 times)") {
		t.Errorf("expected output to contain '(occurred 5 times)', got %q", output)
	}
	if !strings.Contains(output, "Suggestion: check that the required key is provided via --var flags.") {
		t.Errorf("expected output to contain suggestion, got %q", output)
	}
}
