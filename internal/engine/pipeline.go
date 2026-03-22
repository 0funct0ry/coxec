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

// ParseCommand extracts the command name and its arguments from a string, respecting quotes
// and balanced braces (so JSON-like values such as {"id": 1} stay as one token).
func ParseCommand(cmdStr string) (string, []string) {
	var args []string
	var current strings.Builder
	inDoubleQuotes := false
	inSingleQuotes := false
	escaped := false
	braceDepth := 0

	for i := 0; i < len(cmdStr); i++ {
		c := cmdStr[i]

		if escaped {
			current.WriteByte(c)
			escaped = false
			continue
		}

		if c == '\\' && !inSingleQuotes {
			escaped = true
			continue
		}

		if c == '"' && !inSingleQuotes && braceDepth == 0 {
			inDoubleQuotes = !inDoubleQuotes
			continue
		}

		if c == '\'' && !inDoubleQuotes && braceDepth == 0 {
			inSingleQuotes = !inSingleQuotes
			continue
		}

		if !inDoubleQuotes && !inSingleQuotes {
			if c == '{' {
				braceDepth++
				current.WriteByte(c)
				continue
			}
			if c == '}' && braceDepth > 0 {
				braceDepth--
				current.WriteByte(c)
				continue
			}
		} else {
			// Inside quotes, still track braces for the content but don't split
			current.WriteByte(c)
			continue
		}

		if (c == ' ' || c == '\t' || c == '\n' || c == '\r') && !inDoubleQuotes && !inSingleQuotes && braceDepth == 0 {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}

		current.WriteByte(c)
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	if len(args) == 0 {
		return "", nil
	}
	return args[0], args[1:]
}
