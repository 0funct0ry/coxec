package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
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

type tcpOutputFormat string

const (
	TCPOutputText  tcpOutputFormat = "text"
	TCPOutputJSON  tcpOutputFormat = "json"
	TCPOutputJSONL tcpOutputFormat = "jsonl"
)

// Execute performs the TCP connection based on the provided arguments.
func (c *TCPClient) Execute(ctx context.Context, args []string, data IterationData) (*Result, error) {
	fs := flag.NewFlagSet("tcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var body string
	fs.StringVar(&body, "body", "", "Payload to send after connect")
	fs.StringVar(&body, "d", "", "Short for --body")

	var timeoutStr string
	fs.StringVar(&timeoutStr, "timeout", "10s", "Connection timeout")

	var output string
	fs.StringVar(&output, "output", string(TCPOutputText), "Output format: text, json, jsonl")
	fs.StringVar(&output, "o", string(TCPOutputText), "Short for --output")

	var positionalArgs []string
	remaining := args
	for len(remaining) > 0 {
		if err := fs.Parse(remaining); err != nil {
			return nil, fmt.Errorf("tcp built-in argument error: %w", err)
		}
		if fs.NArg() > 0 {
			positionalArgs = append(positionalArgs, fs.Arg(0))
			remaining = fs.Args()[1:]
		} else {
			break
		}
	}

	if len(positionalArgs) < 2 {
		return nil, fmt.Errorf("tcp requires HOST and PORT. Usage: tcp [HOST] [PORT] [flags]")
	}

	host := positionalArgs[0]
	port := positionalArgs[1]
	address := net.JoinHostPort(host, port)

	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return nil, fmt.Errorf("invalid timeout duration: %s", timeoutStr)
	}

	start := time.Now()
	
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	
	var tcpErr *TCPError
	var bytesSent int
	var bytesReceived int
	var responseData []byte
	connected := err == nil
	
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		category := categorizeTCPError(err)
		tcpErr = NewTCPError(category, err)
	} else {
		defer conn.Close()
		
		if body != "" {
			var n int
			n, err = conn.Write([]byte(body))
			bytesSent = n
			if err != nil {
				tcpErr = NewTCPError("write_error", err)
			}
		}
		
		if err == nil {
			// Set a read deadline to avoid hanging indefinitely if server doesn't respond
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			buf := make([]byte, 128)
			var n int
			n, err = conn.Read(buf)
			if err != nil && err != io.EOF {
				// Only treat as error if it's not just EOF after connecting/writing
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// Timeout on read is often expected if just testing connectivity
					// or heartbeat, so we don't necessarily treat it as a hard failure
					// unless we were expecting a response. 
					// But for the sake of results, we'll just record it.
				} else {
					tcpErr = NewTCPError("read_error", err)
				}
			}
			if n > 0 {
				bytesReceived = n
				responseData = buf[:n]
			}
		}
	}

	latency := time.Since(start)
	outFormat := tcpOutputFormat(strings.ToLower(output))

	var stdout string
	switch outFormat {
	case TCPOutputJSON, TCPOutputJSONL:
		resultObj := map[string]interface{}{
			"iteration":      data.Iteration,
			"worker_id":      data.WorkerID,
			"host":           host,
			"port":           port,
			"connected":      connected,
			"bytes_sent":     bytesSent,
			"bytes_received": bytesReceived,
			"latency_ms":     float64(latency.Microseconds()) / 1000.0,
			"response":       string(responseData),
		}
		if tcpErr != nil {
			resultObj["error"] = tcpErr.Error()
		} else {
			resultObj["error"] = nil
		}

		b, _ := json.Marshal(resultObj)
		if outFormat == TCPOutputJSON {
			var prettyJSON bytes.Buffer
			if identErr := json.Indent(&prettyJSON, b, "", "  "); identErr == nil {
				stdout = prettyJSON.String() + "\n"
			} else {
				stdout = string(b) + "\n"
			}
		} else {
			stdout = string(b) + "\n"
		}

	case TCPOutputText:
		fallthrough
	default:
		if tcpErr != nil {
			stdout = fmt.Sprintf("TCP ERROR | %s:%s | %dms | %v\n", host, port, latency.Milliseconds(), tcpErr.Err)
		} else {
			stdout = fmt.Sprintf("TCP OK | %s:%s | %dms\n", host, port, latency.Milliseconds())
		}
	}

	res := &Result{
		Stdout:   stdout,
		Stderr:   "",
		ExitCode: 0,
		Latency:  latency.Nanoseconds(),
	}

	if tcpErr != nil {
		res.ExitCode = 1
		return res, tcpErr
	}

	return res, nil
}

func categorizeTCPError(err error) string {
	if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline exceeded") {
		return "timeout"
	}
	if strings.Contains(err.Error(), "connection refused") {
		return "connection_refused"
	}
	return "network_error"
}
