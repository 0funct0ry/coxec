package engine

import (
	"bytes"
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
