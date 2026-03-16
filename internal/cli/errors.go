package cli

import (
	"encoding/json"
	"fmt"
)

// ValidationError represents a CLI validation failure with a specific exit code
type ValidationError struct {
	ExitCode   int    `json:"-"`
	ID         string `json:"code"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion"`
}

func (e *ValidationError) Error() string {
	return e.Message
}

// JSON returns the structured JSON representation of the error
func (e *ValidationError) JSON() string {
	type wrapper struct {
		Error *ValidationError `json:"error"`
	}
	b, _ := json.Marshal(wrapper{Error: e})
	return string(b)
}

func (e *ValidationError) String() string {
	return fmt.Sprintf("Error: %s\nSuggestion: %s", e.Message, e.Suggestion)
}
