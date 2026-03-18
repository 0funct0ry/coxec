package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// TemplateError represents a structured error that occurs during template parsing or execution.
type TemplateError struct {
	Name           string
	Line           int
	Column         int
	OffendingToken string
	Message        string
	Context        string
	Suggestion     string
	OriginalError  error
}

func (e *TemplateError) Error() string {
	name := e.Name
	if name == "" || name == "plan" || name == "step" {
		name = "<inline>"
	}
	if e.Context == "execution" {
		if e.Column > 0 {
			return fmt.Sprintf("%s:%d:%d: %s (at <%s>)", name, e.Line, e.Column, e.Message, e.OffendingToken)
		}
		return fmt.Sprintf("%s:%d: %s (at <%s>)", name, e.Line, e.Message, e.OffendingToken)
	}
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", name, e.Line, e.Message)
	}
	return e.Message
}

// wrapTemplateError converts standard Go template errors into a structured TemplateError.
func wrapTemplateError(err error, tplName string, tplStr string) error {
	if err == nil {
		return nil
	}

	te := &TemplateError{
		Name:          tplName,
		OriginalError: err,
		Message:       err.Error(),
	}

	msg := err.Error()

	// Parse exec error
	execRe := regexp.MustCompile(`template: [^:]+:(\d+):(\d+): executing "[^"]*" at <(.*?)>: (.*)`)
	if matches := execRe.FindStringSubmatch(msg); len(matches) == 5 {
		if l, err := strconv.Atoi(matches[1]); err == nil {
			te.Line = l
		}
		if c, err := strconv.Atoi(matches[2]); err == nil {
			te.Column = c
		}
		te.OffendingToken = matches[3]
		te.Message = strings.TrimSpace(matches[4])
		te.Context = "execution"
	} else {
		execRe2 := regexp.MustCompile(`template: [^:]+:(\d+): executing "[^"]*" at <(.*?)>: (.*)`)
		if matches2 := execRe2.FindStringSubmatch(msg); len(matches2) == 4 {
			if l, err := strconv.Atoi(matches2[1]); err == nil {
				te.Line = l
			}
			te.OffendingToken = matches2[2]
			te.Message = strings.TrimSpace(matches2[3])
			te.Context = "execution"
		} else {
			// Parse parse error
			parseRe := regexp.MustCompile(`template: [^:]+:(\d+): (.*)`)
			if matches := parseRe.FindStringSubmatch(msg); len(matches) == 3 {
				if l, err := strconv.Atoi(matches[1]); err == nil {
					te.Line = l
				}
				te.Message = strings.TrimSpace(matches[2])
				te.Context = "parsing"
			}
		}
	}

	te.Suggestion = generateSuggestion(te.Message)
	return te
}

func generateSuggestion(msg string) string {
	if strings.Contains(msg, "function") && strings.Contains(msg, "not defined") {
		return "check the function name. Available functions include: env, now, add, mod, randInt, uuid, jsonEncode, split, etc."
	}
	if strings.Contains(msg, "map has no entry for key") {
		return "check that the required key is provided via --var flags."
	}
	if strings.Contains(msg, "wrong number of args for Var") || strings.Contains(msg, "wrong number of args for Env") {
		return "methods like .Var and .Env take a string argument. Example: {{.Var \"key\"}}"
	}
	if strings.Contains(msg, "division by zero") || strings.Contains(msg, "modulo by zero") {
		return "ensure the denominator is strictly positive."
	}
	return ""
}

// HTTPError represents a structured error that occurs during HTTP builtin execution.
type HTTPError struct {
	Category string
	Err      error
}

func (e *HTTPError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Category
}

func (e *HTTPError) Unwrap() error {
	return e.Err
}

// NewHTTPError creates a new HTTPError with the given category and wrapped error.
func NewHTTPError(category string, err error) *HTTPError {
	return &HTTPError{
		Category: category,
		Err:      err,
	}
}

// TCPError represents a structured error that occurs during TCP builtin execution.
type TCPError struct {
	Category string
	Err      error
}

func (e *TCPError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Category
}

func (e *TCPError) Unwrap() error {
	return e.Err
}

// NewTCPError creates a new TCPError with the given category and wrapped error.
func NewTCPError(category string, err error) *TCPError {
	return &TCPError{
		Category: category,
		Err:      err,
	}
}
