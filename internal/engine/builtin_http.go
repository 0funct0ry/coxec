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
	return ".http"
}

// Help returns the help string for the http built-in.
func (c *HTTPClient) Help() string {
	return `.http [METHOD] [URL] [flags]

Natively executes HTTP requests without spawning a shell.

Methods:
  GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS

Flags:
  -H, --header stringArray   Custom HTTP headers (e.g. "Authorization: Bearer token")
  -d, --body string          HTTP request body
  -o, --output string        Output format: text, json, jsonl (default "text")

Examples:
  .http GET https://api.example.com/users/1
  .http POST https://api.example.com/data --body '{"score": 100}'
  .http GET https://api.example.com/stats --output json`
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
		return nil, fmt.Errorf(".http requires at least METHOD and URL. Usage: .http [METHOD] [URL] [flags]")
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

	var statusCode int
	success := false
	var errMsg *string
	var respBody []byte
	headerMap := make(map[string]string)

	var httpErr *HTTPError

	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		success = false
		eStr := err.Error()
		errMsg = &eStr
		category := categorizeHTTPError(err)
		httpErr = NewHTTPError(category, 0, err)
	} else {
		defer resp.Body.Close()
		statusCode = resp.StatusCode
		success = statusCode >= 200 && statusCode < 400

		for k, v := range resp.Header {
			headerMap[k] = strings.Join(v, ", ")
		}

		b, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			eStr := fmt.Sprintf("failed to read response body: %v", readErr)
			errMsg = &eStr
			httpErr = NewHTTPError("body_read_error", statusCode, readErr)
		} else {
			respBody = b
		}

		if !success && httpErr == nil {
			var cat string
			if statusCode >= 500 {
				cat = "http_5xx"
			} else if statusCode == 429 {
				cat = "http_429"
			} else {
				cat = "http_4xx"
			}
			httpErr = NewHTTPError(cat, statusCode, fmt.Errorf("HTTP %d", statusCode))
		}
	}

	outFormat := httpOutputFormat(strings.ToLower(output))

	var stdout string
	switch outFormat {
	case OutputJSON, OutputJSONL:
		var previewString string
		if len(respBody) > 0 {
			previewString = string(respBody)
			if len(previewString) > 1024 {
				previewString = previewString[:1024] + "... (truncated)"
			}
		}

		resultObj := map[string]interface{}{
			"iteration":             data.Iteration,
			"worker_id":             data.WorkerID,
			"method":                method,
			"url":                   rawURL,
			"status_code":           statusCode,
			"latency_ms":            float64(latency.Microseconds()) / 1000.0,
			"success":               success,
			"error":                 errMsg,
			"response_headers":      headerMap,
			"response_body_preview": previewString,
		}

		b, encErr := json.Marshal(resultObj)
		if encErr != nil {
			return nil, fmt.Errorf("failed to encode JSON output: %w", encErr)
		}

		if outFormat == OutputJSON {
			var prettyJSON bytes.Buffer
			if identErr := json.Indent(&prettyJSON, b, "", "  "); identErr == nil {
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
		if httpErr != nil && resp == nil {
			stdout = fmt.Sprintf("HTTP ERROR | %dms | %v\n", latency.Milliseconds(), err)
		} else {
			stdout = fmt.Sprintf("HTTP %d | %dms\n", statusCode, latency.Milliseconds())
		}
	}

	res := &Result{
		Stdout:   stdout,
		Stderr:   "",
		ExitCode: 0,
		Latency:  latency.Nanoseconds(),
		Metadata: map[string]interface{}{
			"status_code": statusCode,
		},
	}

	if httpErr != nil {
		res.ExitCode = 1
		return res, httpErr
	}

	return res, nil
}

func categorizeHTTPError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || osIsTimeout(err) {
		return "timeout"
	}
	if strings.Contains(err.Error(), "connection refused") {
		return "connection_refused"
	}
	if strings.Contains(err.Error(), "no such host") {
		return "dns_error"
	}
	return "network_error"
}

func osIsTimeout(err error) bool {
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}
