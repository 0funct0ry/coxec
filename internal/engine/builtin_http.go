package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPClient is a BuiltinClient that handles HTTP requests.
type HTTPClient struct {
	client *http.Client
}

// NewHTTPClient creates a new HTTPClient.
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Name returns the name of the built-in client.
func (c *HTTPClient) Name() string {
	return "http"
}

// stringSliceFlag is a custom flag.Value that allows multiple --header flags
type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ", ")
}

func (s *stringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type httpOutputFormat string

const (
	OutputText  httpOutputFormat = "text"
	OutputJSON  httpOutputFormat = "json"
	OutputJSONL httpOutputFormat = "jsonl"
)

// Execute performs the HTTP request based on the provided arguments.
func (c *HTTPClient) Execute(ctx context.Context, args []string, data IterationData) (*Result, error) {
	fs := flag.NewFlagSet("http", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // Suppress flag usage output to stderr

	var headers stringSliceFlag
	fs.Var(&headers, "header", "Custom HTTP headers (can be specified multiple times)")
	fs.Var(&headers, "H", "Short for --header")
	
	var body string
	fs.StringVar(&body, "body", "", "HTTP request body")
	fs.StringVar(&body, "d", "", "Short for --body")

	var output string
	fs.StringVar(&output, "output", string(OutputText), "Output format: text, json, jsonl")
	fs.StringVar(&output, "o", string(OutputText), "Short for --output")

	var positionalArgs []string
	remaining := args
	for len(remaining) > 0 {
		if err := fs.Parse(remaining); err != nil {
			return nil, fmt.Errorf("http built-in argument error: %w", err)
		}
		if fs.NArg() > 0 {
			positionalArgs = append(positionalArgs, fs.Arg(0))
			remaining = fs.Args()[1:]
		} else {
			break
		}
	}
	if len(positionalArgs) < 2 {
		return nil, fmt.Errorf("http requires at least METHOD and URL. Usage: http [METHOD] [URL] [flags]")
	}

	method := strings.ToUpper(positionalArgs[0])
	validMethods := map[string]bool{
		"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true,
	}
	if !validMethods[method] {
		return nil, fmt.Errorf("invalid or unsupported HTTP method: %s", method)
	}

	rawURL := positionalArgs[1]
	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("invalid URL: %s", rawURL)
	}

	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	hasContentType := false
	for _, h := range headers {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			req.Header.Add(key, val)
			if strings.EqualFold(key, "Content-Type") {
				hasContentType = true
			}
		}
	}

	if body != "" && !hasContentType {
		req.Header.Set("Content-Type", "application/json")
	}

	start := time.Now()
	resp, err := c.client.Do(req)
	latency := time.Since(start)

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || osIsTimeout(err) {
			return nil, fmt.Errorf("http request timed out after %v", latency)
		}
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, fmt.Errorf("http target unreachable: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	outFormat := httpOutputFormat(strings.ToLower(output))
	
	var stdout string
	switch outFormat {
	case OutputJSON, OutputJSONL:
		headerMap := make(map[string]string)
		for k, v := range resp.Header {
			headerMap[k] = strings.Join(v, ", ")
		}

		// Try to parse body as JSON if possible to embed it directly, otherwise keep as string
		// For the requirement "body (string or truncated preview)", we just include the string body.
		var jsonBody interface{}
		if len(respBody) > 0 && (strings.HasPrefix(string(respBody), "{") || strings.HasPrefix(string(respBody), "[")) {
			var decoded interface{}
			if err := json.Unmarshal(respBody, &decoded); err == nil {
				jsonBody = decoded
			} else {
				jsonBody = string(respBody)
			}
		} else {
			jsonBody = string(respBody)
		}

		resultObj := map[string]interface{}{
			"status":     resp.StatusCode,
			"latency_ms": float64(latency.Microseconds()) / 1000.0,
			"headers":    headerMap,
			"body":       jsonBody,
		}

		b, err := json.Marshal(resultObj)
		if err != nil {
			return nil, fmt.Errorf("failed to encode JSON output: %w", err)
		}
		
		if outFormat == OutputJSON {
			// Pretty print if json (not jsonl)
			var prettyJSON bytes.Buffer
			if err := json.Indent(&prettyJSON, b, "", "  "); err == nil {
				stdout = prettyJSON.String() + "\n"
			} else {
				stdout = string(b) + "\n"
			}
		} else {
			stdout = string(b) + "\n"
		}

	case OutputText:
		fallthrough
	default:
		stdout = fmt.Sprintf("HTTP %d | %dms\n", resp.StatusCode, latency.Milliseconds())
	}

	return &Result{
		Stdout:   stdout,
		Stderr:   "",
		ExitCode: 0,
		Latency:  latency.Nanoseconds(),
	}, nil
}

func osIsTimeout(err error) bool {
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}
