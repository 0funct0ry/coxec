package engine

import (
	"context"
)

// HTTPClient is a BuiltinClient that handles HTTP requests.
type HTTPClient struct{}

// NewHTTPClient creates a new HTTPClient.
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{}
}

// Name returns the name of the built-in client.
func (c *HTTPClient) Name() string {
	return "http"
}

// Execute performs the HTTP request based on the provided arguments.
func (c *HTTPClient) Execute(ctx context.Context, args []string, data IterationData) (*Result, error) {
	// For now, this is just a scaffold.
	return &Result{
		Stdout:   "HTTP built-in not yet fully implemented",
		Stderr:   "",
		ExitCode: 0,
		Latency:  0,
	}, nil
}
