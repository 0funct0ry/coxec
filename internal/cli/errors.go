package cli

// ValidationError represents a CLI validation failure with a specific exit code
type ValidationError struct {
	Code int
	Msg  string
}

func (e *ValidationError) Error() string {
	return e.Msg
}
