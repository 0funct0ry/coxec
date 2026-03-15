package engine

import (
	"bytes"
	"os"
	"text/template"
)

// IterationData holds the data available to the command templates
type IterationData struct {
	Iteration     int
	WorkerID      int
	Timestamp          string
	TimestampUnix      int64
	TimestampUnixMilli int64
	TimestampUnixNano  int64
	UUID               string
	UserVars           map[string]string
}

// Env returns the value of an environment variable.
// Returns an empty string if the variable is not set.
func (d IterationData) Env(key string) string {
	return os.Getenv(key)
}

// Var returns the value of a user-provided variable from --var.
// If the variable is not found in UserVars, it falls back to environment variables.
// Returns an empty string if the variable is not set.
func (d IterationData) Var(key string) string {
	if val, ok := d.UserVars[key]; ok {
		return val
	}
	return os.Getenv(key)
}

// renderTemplate parses and executes a Go template string with the provided data
func renderTemplate(name string, tpl string, data IterationData) (string, error) {
	if tpl == "" {
		return "", nil
	}

	t, err := template.New(name).Parse(tpl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}
