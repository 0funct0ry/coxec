package engine

import (
	"context"
)

// BuiltinClient defines the interface for native coxec clients
type BuiltinClient interface {
	Name() string
	Execute(ctx context.Context, args []string, data IterationData) (*Result, error)
	Help() string
}

// Result represents the outcome of a pipeline step
type Result struct {
	Stdout        string
	Stderr        string
	ExitCode      int
	Latency       int64 // in nanoseconds
	IsTransparent bool  // If true, Stdout has already been handled or should be ignored for display
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

// Help returns the help string for a built-in client by name
func (r *BuiltinRegistry) Help(name string) (string, bool) {
	if c, ok := r.builtins[name]; ok {
		return c.Help(), true
	}
	return "", false
}

// AllHelp returns a map of all registered built-in client help strings
func (r *BuiltinRegistry) AllHelp() map[string]string {
	help := make(map[string]string)
	for name, c := range r.builtins {
		help[name] = c.Help()
	}
	return help
}
