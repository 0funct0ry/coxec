package engine

import (
	"context"
)

// TCPClient is a BuiltinClient that handles TCP connections.
type TCPClient struct{}

// NewTCPClient creates a new TCPClient.
func NewTCPClient() *TCPClient {
	return &TCPClient{}
}

// Name returns the name of the built-in client.
func (c *TCPClient) Name() string {
	return "tcp"
}

// Execute performs the TCP connection based on the provided arguments.
func (c *TCPClient) Execute(ctx context.Context, args []string, data IterationData) (*Result, error) {
	// For now, this is just a scaffold.
	return &Result{
		Stdout:   "TCP built-in not yet fully implemented",
		Stderr:   "",
		ExitCode: 0,
		Latency:  0,
	}, nil
}
