package engine

import (
	"strings"
)

// SplitPipeline splits a rendered template string into separate pipeline steps.
// It splits by the "|>" operator and trims leading/trailing whitespace from each step.
func SplitPipeline(rendered string) []string {
	if rendered == "" {
		return nil
	}

	rawSteps := strings.Split(rendered, "|>")
	var steps []string
	for _, s := range rawSteps {
		trimmed := strings.TrimSpace(s)
		if trimmed != "" {
			steps = append(steps, trimmed)
		}
	}
	return steps
}

// ParseCommand extracts the first token (command) and the remaining arguments from a string.
func ParseCommand(cmdStr string) (string, []string) {
	fields := strings.Fields(cmdStr)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], fields[1:]
}
