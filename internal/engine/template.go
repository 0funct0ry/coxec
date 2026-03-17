package engine

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"text/template"
)

// IterationData holds the data available to the command templates
type IterationData struct {
	Iteration          int
	WorkerID           int
	Timestamp          string
	TimestampUnix      int64
	TimestampUnixMilli int64
	TimestampUnixNano  int64
	UUID               string
	UserVars           map[string]string
	Prev               *Result // Previous pipeline step result
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

// ValidateTemplate parses a template string to check for syntax errors.
// It returns a descriptive error if parsing fails.
func ValidateTemplate(name, tpl string) error {
	_, err := template.New(name).Funcs(template.FuncMap{
		"quote":      shellQuote,
		"randInt":    randInt,
		"randFloat":  randFloat,
		"randString": randString,
		"randChoice": randChoice,
	}).Parse(tpl)
	return err
}

// renderTemplate parses and executes a Go template string with the provided data
func renderTemplate(name string, tpl string, data IterationData) (string, error) {
	if tpl == "" {
		return "", nil
	}

	t, err := template.New(name).Funcs(template.FuncMap{
		"quote":      shellQuote,
		"randInt":    randInt,
		"randFloat":  randFloat,
		"randString": randString,
		"randChoice": randChoice,
	}).Parse(tpl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// randInt returns a random integer between min and max inclusive.
func randInt(min, max int) int {
	if min > max {
		min, max = max, min
	}
	return rand.IntN(max-min+1) + min
}

// randFloat returns a random float between min and max with the specified precision.
func randFloat(min, max float64, precision int) string {
	if min > max {
		min, max = max, min
	}
	val := rand.Float64()*(max-min) + min
	format := fmt.Sprintf("%%.%df", precision)
	return fmt.Sprintf(format, val)
}

// randString returns a random alphanumeric string of the specified length.
func randString(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rand.IntN(len(charset))]
	}
	return string(b)
}

// randChoice returns a random string from the provided choices.
func randChoice(choices ...string) string {
	if len(choices) == 0 {
		return ""
	}
	return choices[rand.IntN(len(choices))]
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// Wrap in single quotes, and escape existing single quotes
	// ' becomes '\''
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
