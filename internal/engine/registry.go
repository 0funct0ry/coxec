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

// ExecutionDetail represents the detailed result of a single execution
type ExecutionDetail struct {
	Index    int    `json:"index"`
	Status   string `json:"status"`
	Duration string `json:"duration"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ExecutionReport represents a full summary of a completed Job Pool execution
type ExecutionReport struct {
	TotalExecutions int               `json:"total_executions"`
	SuccessCount    int               `json:"success_count"`
	FailCount       int               `json:"fail_count"`
	TimeoutCount    int               `json:"timeout_count"`
	TotalDuration   string            `json:"total_duration"`
	AverageLatency  string            `json:"average_latency"`
	P50Latency      string            `json:"p50_latency"`
	P90Latency      string            `json:"p90_latency"`
	P95Latency      string            `json:"p95_latency"`
	P99Latency      string            `json:"p99_latency"`
	RatePerSecond   float64           `json:"rate_per_second"`
	HTTPErrors      map[string]int    `json:"http_errors,omitempty"`
	TCPErrors       map[string]int    `json:"tcp_errors,omitempty"`
	TemplateErrors  map[string]int    `json:"template_errors,omitempty"`
	Details         []ExecutionDetail `json:"details,omitempty"`
	Stdout          []string          `json:"stdout,omitempty"`
	Stderr          []string          `json:"stderr,omitempty"`
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
