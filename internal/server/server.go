package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/0funct0ry/coxec/internal/engine"
)

// ServerStatus represents the current lifecycle state of the server.
type ServerStatus string

const (
	StatusStarting     ServerStatus = "starting"
	StatusReady        ServerStatus = "ready"
	StatusShuttingDown ServerStatus = "shutting_down"
)

// Server represents the coxec HTTP server.
type Server struct {
	Addr           string
	Port           int
	Version        string
	StartTime      time.Time
	mu             sync.RWMutex
	Status         ServerStatus
	ActiveJobs     atomic.Int32
	AuthToken      string
	AuthBasic      string
	AuthHmacSecret string
	TLSCert        string
	TLSKey         string
	Registry       *engine.BuiltinRegistry
	DefaultConcurrency int
	DefaultIterations  int
}

// ExecRequest defines the payload for POST /exec
type ExecRequest struct {
	Exec        interface{}       `json:"exec"`
	Concurrency int               `json:"concurrency,omitempty"`
	Iterations  int               `json:"iterations,omitempty"`
	Timeout     string            `json:"timeout,omitempty"`
	Rate        string            `json:"rate,omitempty"`
	Vars        map[string]string `json:"vars,omitempty"`
	Delay       string            `json:"delay,omitempty"`
	Jitter      string            `json:"jitter,omitempty"`
	RampUp      string            `json:"rampup,omitempty"`
	Verbose     bool              `json:"verbose,omitempty"`
}

// ExecResponse defines the response for POST /exec
type ExecResponse struct {
	Status string                  `json:"status"`
	Report *engine.ExecutionReport `json:"report,omitempty"`
	Error  string                  `json:"error,omitempty"`
}

// NewServer creates a new Server instance.
func NewServer(addr string, port int, version string, authToken string, authBasic string, authHmacSecret string, tlsCert string, tlsKey string, registry *engine.BuiltinRegistry, defaultConcurrency, defaultIterations int) *Server {
	return &Server{
		Addr:               addr,
		Port:               port,
		Version:            version,
		AuthToken:          authToken,
		AuthBasic:          authBasic,
		AuthHmacSecret:     authHmacSecret,
		TLSCert:            tlsCert,
		TLSKey:             tlsKey,
		StartTime:          time.Now(),
		Status:             StatusStarting,
		Registry:           registry,
		DefaultConcurrency: defaultConcurrency,
		DefaultIterations:  defaultIterations,
	}
}

// Start starts the HTTP server and waits for it to shut down.
func (s *Server) Start(ctx context.Context) error {
	fullAddr := fmt.Sprintf("%s:%d", s.Addr, s.Port)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.HealthHandler)
	mux.HandleFunc("/exec", s.ExecHandler)

	srv := &http.Server{
		Addr:    fullAddr,
		Handler: mux,
	}

	errChan := make(chan error, 1)
	go func() {
		protocol := "http"
		useTLS := s.TLSCert != "" && s.TLSKey != ""
		if useTLS {
			protocol = "https"
		}
		fmt.Printf("coxec server listening on %s (%s)\n", fullAddr, protocol)
		s.mu.Lock()
		s.Status = StatusReady
		s.mu.Unlock()
		var err error
		if useTLS {
			err = srv.ListenAndServeTLS(s.TLSCert, s.TLSKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	select {
	case <-ctx.Done():
		fmt.Println("\nShutting down server...")
		s.mu.Lock()
		s.Status = StatusShuttingDown
		s.mu.Unlock()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errChan:
		return fmt.Errorf("server failed: %w", err)
	}
}

func (s *Server) HealthHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	currentStatus := s.Status
	s.mu.RUnlock()

	if currentStatus != StatusReady {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": string(currentStatus),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"version":        s.Version,
		"active_jobs":    s.ActiveJobs.Load(),
		"uptime_seconds": int64(time.Since(s.StartTime).Seconds()),
	})
}

func (s *Server) ExecHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "method not allowed"})
		return
	}

	s.mu.RLock()
	currentStatus := s.Status
	s.mu.RUnlock()

	if currentStatus != StatusReady {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "server is not ready"})
		return
	}

	if s.AuthToken != "" {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "unauthorized"})
			return
		}
		
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.AuthToken)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "unauthorized"})
			return
		}
	}

	if s.AuthBasic != "" {
		user, pass, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "unauthorized"})
			return
		}

		actualUserPass := user + ":" + pass
		if subtle.ConstantTimeCompare([]byte(actualUserPass), []byte(s.AuthBasic)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "unauthorized"})
			return
		}
	}

	var req ExecRequest
	contentType := r.Header.Get("Content-Type")

	// Read body for multiple parsing attempts and HMAC validation
	rawBytes, _ := io.ReadAll(r.Body)

	if s.AuthHmacSecret != "" {
		hmacHeader := r.Header.Get("X-Signature")
		if !strings.HasPrefix(hmacHeader, "sha256=") {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "unauthorized"})
			return
		}
		providedSig := strings.TrimPrefix(hmacHeader, "sha256=")
		expectedBytes, err := hex.DecodeString(providedSig)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "unauthorized"})
			return
		}

		mac := hmac.New(sha256.New, []byte(s.AuthHmacSecret))
		mac.Write(rawBytes)
		computedSig := mac.Sum(nil)

		if !hmac.Equal(computedSig, expectedBytes) {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "unauthorized"})
			return
		}
	}

	r.Body = io.NopCloser(bytes.NewBuffer(rawBytes))

	// Pre-process: replace bare (unquoted) template expressions with string placeholders
	// so the JSON is valid even with things like: "score": {{randInt 1 100}}
	bodyBytes, placeholders := makeTemplateExprsJSONSafe(rawBytes)

	// Try JSON regardless of Content-Type (good for DX with curl -d)
	if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&req); err == nil && req.Exec != nil {
		// JSON success
	} else {
		// Reset and try form parsing using the original raw bytes
		r.Body = io.NopCloser(bytes.NewBuffer(rawBytes))
		if err := r.ParseForm(); err == nil {
			req.Exec = r.FormValue("exec")
			if req.Concurrency == 0 {
				if v, err := strconv.Atoi(r.FormValue("concurrency")); err == nil {
					req.Concurrency = v
				}
			}
			if req.Iterations == 0 {
				if v, err := strconv.Atoi(r.FormValue("iterations")); err == nil {
					req.Iterations = v
				}
			}
			if req.Timeout == "" {
				req.Timeout = r.FormValue("timeout")
			}
			if req.Rate == "" {
				req.Rate = r.FormValue("rate")
			}
			if req.Delay == "" {
				req.Delay = r.FormValue("delay")
			}
			if req.Jitter == "" {
				req.Jitter = r.FormValue("jitter")
			}
			if req.RampUp == "" {
				req.RampUp = r.FormValue("rampup")
			}
			if !req.Verbose {
				req.Verbose, _ = strconv.ParseBool(r.FormValue("verbose"))
			}
		}
	}

	execStr, err := s.resolveExec(req.Exec)
	if err != nil || execStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "missing or invalid 'exec' field"})
		return
	}

	// Restore placeholder template expressions in the resolved exec command string.
	// The placeholders were inserted as JSON string values (with surrounding quotes),
	// so we need to replace the quoted form "...__COXEC_TMPL_N__..." back to the bare
	// original expression (without quotes) to preserve the intended JSON type.
	for key, original := range placeholders {
		// Replace the quoted placeholder (as it appears after json.Marshal) with bare expression
		execStr = strings.ReplaceAll(execStr, `"`+key+`"`, original)
		// Also replace unquoted form in case it appears that way (e.g. URL templates)
		execStr = strings.ReplaceAll(execStr, key, original)
	}

	concurrency := req.Concurrency
	if concurrency == 0 {
		concurrency = s.DefaultConcurrency
	}
	if concurrency <= 0 {
		concurrency = 1
	}

	iterations := req.Iterations
	if iterations == 0 {
		iterations = s.DefaultIterations
	}
	if iterations <= 0 {
		iterations = concurrency
	}

	// Validation
	if concurrency > 1000 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "concurrency exceeds maximum allowed (1000)"})
		return
	}
	if iterations > 10000000 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: "iterations exceed maximum allowed (10,000,000)"})
		return
	}
	if iterations < concurrency && iterations > 0 {
		// This is technically allowed but we might want to warn or just let it be.
		// For now, let's just make sure it's at least 1 if concurrency is 1.
	}

	fmt.Printf("[%s] Executing: %s (Concurrency: %d, Iterations: %d)\n", time.Now().Format(time.RFC3339), execStr, concurrency, iterations)

	timeout, _ := time.ParseDuration(req.Timeout)
	delay, _ := time.ParseDuration(req.Delay)
	jitter, _ := time.ParseDuration(req.Jitter)
	rampup, _ := time.ParseDuration(req.RampUp)

	rateLimit := 0.0
	if req.Rate != "" {
		parts := strings.Split(req.Rate, "/")
		val, err := strconv.ParseFloat(parts[0], 64)
		if err == nil {
			if len(parts) == 1 {
				rateLimit = val
			} else {
				switch strings.ToLower(parts[1]) {
				case "s", "sec", "second", "seconds":
					rateLimit = val
				case "m", "min", "minute", "minutes":
					rateLimit = val / 60.0
				case "h", "hr", "hour", "hours":
					rateLimit = val / 3600.0
				}
			}
		}
	}

	s.ActiveJobs.Add(1)
	defer s.ActiveJobs.Add(-1)

	tasks := make(chan engine.Task, iterations)
	opts := engine.ExecOptions{
		Silent:        false,
		Verbose:       req.Verbose,
		Report:        true,
		TotalTasks:    iterations,
		Context:       r.Context(),
		Stdout:        &strings.Builder{}, // Capture but ignore for now as we want structured report
		Stderr:        &strings.Builder{},
		UserVars:      req.Vars,
		Registry:      s.Registry,
		TemplateState: engine.NewTemplateState(),
		Timeout:       timeout,
		Delay:         delay,
		Jitter:        jitter,
		RampUp:        rampup,
		RateLimit:     rateLimit,
	}

	// Simple task generator for the server
	go func() {
		defer close(tasks)
		var lastStart time.Time
		rateInterval := time.Duration(0)
		if rateLimit > 0 {
			rateInterval = time.Duration(float64(time.Second) / rateLimit)
		}

		for i := 0; i < iterations; i++ {
			now := time.Now()
			var waitDuration time.Duration

			if i > 0 {
				if rateLimit > 0 {
					target := lastStart.Add(rateInterval)
					if target.After(now) {
						waitDuration = target.Sub(now)
					}
				}

				if delay > 0 || jitter > 0 {
					d := delay
					if jitter > 0 {
						jf := float64(jitter)
						randomJitter := time.Duration(jf * (2*rand.Float64() - 1))
						d += randomJitter
						if d < 0 {
							d = 0
						}
					}
					if d > waitDuration {
						waitDuration = d
					}
				}

				if waitDuration > 0 {
					select {
					case <-r.Context().Done():
						return
					case <-time.After(waitDuration):
						now = time.Now()
					}
				}
			}

			lastStart = now
			select {
			case <-r.Context().Done():
				return
			case tasks <- engine.Task{Index: i + 1, Command: execStr, Timestamp: time.Now()}:
			}
		}
	}()

	report, err := engine.RunJobPool(concurrency, tasks, opts)
	if err != nil && report == nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(ExecResponse{Status: "error", Error: err.Error()})
		return
	}

	// Response negotiation
	accept := r.Header.Get("Accept")
	useJSON := strings.Contains(accept, "application/json")
	if !useJSON && strings.Contains(contentType, "application/json") {
		// If they sent JSON, they probably want JSON back unless they explicitly asked for something else
		useJSON = true
	}
	if accept == "*/*" || accept == "" {
		// Default to text for curl/browsers unless they specifically asked for JSON or sent JSON
		if !strings.Contains(contentType, "application/json") {
			useJSON = false
		}
	}

	if useJSON {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(ExecResponse{
			Status: "ok",
			Report: report,
		})
		return
	}

	// Default to plain text
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(s.formatReportText(report)))
}

func (s *Server) resolveExec(exec interface{}) (string, error) {
	if exec == nil {
		return "", nil
	}
	switch v := exec.(type) {
	case string:
		return v, nil
	case map[string]interface{}:
		return s.parseStructuredExec(v)
	default:
		return "", fmt.Errorf("invalid 'exec' type: %T", exec)
	}
}

func (s *Server) parseStructuredExec(m map[string]interface{}) (string, error) {
	client, _ := m["client"].(string)
	if client == "" {
		// If no client field, check if it's a single key map like {".http": {...}}
		for k, v := range m {
			if strings.HasPrefix(k, ".") {
				client = k
				if opts, ok := v.(map[string]interface{}); ok {
					m = opts
				}
				break
			}
		}
	}

	if client == "" {
		return "", fmt.Errorf("structured exec missing 'client' field or shorthand key")
	}

	var sb strings.Builder
	sb.WriteString(client)

	// Special handling for common built-ins to maintain expected positional order
	if client == ".http" {
		method, _ := m["method"].(string)
		url, _ := m["url"].(string)
		if method != "" {
			sb.WriteString(" ")
			sb.WriteString(method)
		}
		if url != "" {
			sb.WriteString(" ")
			sb.WriteString(s.quoteArg(url))
		}
	}

	// Handle explicit positional args
	if args, ok := m["args"].([]interface{}); ok {
		for _, arg := range args {
			sb.WriteString(" ")
			sb.WriteString(s.quoteArg(fmt.Sprint(arg)))
		}
	}

	// Handle flags (generic)
	var keys []string
	flags, ok := m["flags"].(map[string]interface{})
	if ok {
		for k := range flags {
			keys = append(keys, k)
		}
		sort.Strings(keys) // Deterministic order
		for _, k := range keys {
			v := flags[k]
			s.appendFlag(&sb, k, v)
		}
	}

	// Handle top-level convenience keys if not already in flags
	// convenienceAliases maps user-facing key names to the actual flag name passed to the client.
	// Supports both singular and plural forms for common flags.
	convenienceAliases := []struct{ key, flag string }{
		{"body", "body"},
		{"header", "header"},
		{"headers", "header"}, // plural alias
		{"output", "output"},
	}
	for _, alias := range convenienceAliases {
		// Skip if already covered by explicit flags block
		if _, inFlags := flags[alias.key]; inFlags {
			continue
		}
		if _, inFlags := flags[alias.flag]; inFlags {
			continue
		}
		if v, ok := m[alias.key]; ok {
			s.appendFlag(&sb, alias.flag, v)
		}
	}

	return sb.String(), nil
}

func (s *Server) appendFlag(sb *strings.Builder, key string, val interface{}) {
	flagName := key
	if len(flagName) == 1 {
		sb.WriteString(" -")
	} else {
		sb.WriteString(" --")
	}
	sb.WriteString(flagName)
	sb.WriteString(" ")

	switch v := val.(type) {
	case map[string]interface{}:
		// JSON body: marshal first, then restore any \" inside {{...}} template expressions
		// that json.Marshal re-escaped. The template engine needs raw " inside {{ }}.
		b, _ := json.Marshal(v)
		sb.WriteString(s.quoteArg(unescapeTemplateExprs(string(b))))
	case []interface{}:
		// Repeatable flags (like headers)
		for i, item := range v {
			if i > 0 {
				if len(flagName) == 1 {
					sb.WriteString(" -")
				} else {
					sb.WriteString(" --")
				}
				sb.WriteString(flagName)
				sb.WriteString(" ")
			}
			// Also unescape template expressions inside header strings
			sb.WriteString(s.quoteArg(unescapeTemplateExprs(fmt.Sprint(item))))
		}
	default:
		sb.WriteString(s.quoteArg(unescapeTemplateExprs(fmt.Sprint(v))))
	}
}

// makeTemplateExprsJSONSafe scans raw JSON bytes and replaces bare (unquoted)
// Go template expressions like {{randInt 1 100}} with placeholder strings so
// the JSON can be parsed normally. Returns the modified bytes and a map of
// placeholder→original expression to restore later.
func makeTemplateExprsJSONSafe(data []byte) ([]byte, map[string]string) {
	placeholders := make(map[string]string)
	var result bytes.Buffer
	n := 0
	inStr := false
	escaped := false

	i := 0
	for i < len(data) {
		b := data[i]

		if escaped {
			result.WriteByte(b)
			escaped = false
			i++
			continue
		}

		if b == '\\' && inStr {
			escaped = true
			result.WriteByte(b)
			i++
			continue
		}

		if b == '"' {
			inStr = !inStr
			result.WriteByte(b)
			i++
			continue
		}

		// When NOT inside a JSON string, look for {{ template expressions
		if !inStr && i+1 < len(data) && b == '{' && data[i+1] == '{' {
			end := bytes.Index(data[i:], []byte("}}"))
			if end == -1 {
				result.Write(data[i:])
				break
			}
			end += i + 2
			expr := string(data[i:end])
			placeholder := fmt.Sprintf("\"__COXEC_TMPL_%d__\"", n)
			placeholders[fmt.Sprintf("__COXEC_TMPL_%d__", n)] = expr
			n++
			result.WriteString(placeholder)
			i = end
			continue
		}

		result.WriteByte(b)
		i++
	}

	return result.Bytes(), placeholders
}

// unescapeTemplateExprs finds all {{...}} template expressions in s and
// replaces \" with " inside them. This is needed because json.Marshal
// escapes the quotes inside template expressions like {{.Var "key"}},
// turning them into {{.Var \"key\"}} which are invalid Go template syntax.
func unescapeTemplateExprs(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '{' && s[i+1] == '{' {
			end := strings.Index(s[i:], "}}")
			if end == -1 {
				result.WriteString(s[i:])
				break
			}
			end += i + 2
			expr := s[i:end]
			result.WriteString(strings.ReplaceAll(expr, `\"`, `"`))
			i = end
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}


func (s *Server) quoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	// If it doesn't contain shell-sensitive chars, no need to quote
	if !strings.ContainsAny(arg, " \t\n\r'\"\\|><&;()$") {
		return arg
	}
	// Wrap in single quotes and escape internal single quotes: ' -> '\''
	escaped := strings.ReplaceAll(arg, "'", "'\\''")
	return "'" + escaped + "'"
}

func (s *Server) formatReportText(report *engine.ExecutionReport) string {
	var sb strings.Builder

	// Add aggregate stdout
	for _, line := range report.Stdout {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	// Add aggregate stderr (includes summary)
	for _, line := range report.Stderr {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	return sb.String()
}
