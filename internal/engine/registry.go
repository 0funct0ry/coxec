package engine

import (
	"context"
)

// BuiltinClient defines the interface for native coxec clients
type BuiltinClient interface {
	Name() string
	Execute(ctx context.Context, args []string, data IterationData) (*Result, error)
}

// Result represents the outcome of a pipeline step
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Latency  int64 // in nanoseconds
}

// BuiltinRegistry manages registered built-in clients
type BuiltinRegistry struct {
	builtins map[string]BuiltinClient
}

// NewBuiltinRegistry creates a new empty registry
func NewBuiltinRegistry() *BuiltinRegistry {
	return &BuiltinRegistry{
		builtins: make(map[string]BuiltinClient),
	}
}

// Get returns a built-in client by name, if registered
func (r *BuiltinRegistry) Get(name string) (BuiltinClient, bool) {
	c, ok := r.builtins[name]
	return c, ok
}

// Register adds a built-in client to the registry
func (r *BuiltinRegistry) Register(c BuiltinClient) {
	r.builtins[c.Name()] = c
}

// Names returns a list of all registered built-in client names
func (r *BuiltinRegistry) Names() []string {
	names := make([]string, 0, len(r.builtins))
	for name := range r.builtins {
		names = append(names, name)
	}
	return names
}
