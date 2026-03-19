package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPClient_Execute(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"test": "ok"}`))
	})
	mux.HandleFunc("/post", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("X-Received-ContentType", r.Header.Get("Content-Type"))
		w.Header().Set("X-Custom-Header", r.Header.Get("X-Custom"))
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"created": true}`))
	})
	mux.HandleFunc("/status/404", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Not Found"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx := context.Background()
	client := NewHTTPClient()

	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		errContains string
		checkResult func(t *testing.T, res *Result)
	}{
		{
			name: "Basic GET",
			args: []string{"GET", ts.URL + "/get"},
			checkResult: func(t *testing.T, res *Result) {
				if res.ExitCode != 0 {
					t.Errorf("expected exit code 0, got %d", res.ExitCode)
				}
				if !strings.Contains(res.Stdout, "HTTP 200") {
					t.Errorf("expected stdout to contain HTTP 200, got: %s", res.Stdout)
				}
			},
		},
		{
			name: "Basic POST with body",
			args: []string{"POST", ts.URL + "/post", "--body", `{"data":"test"}`},
			checkResult: func(t *testing.T, res *Result) {
				if res.ExitCode != 0 {
					t.Errorf("expected exit code 0, got %d", res.ExitCode)
				}
				if !strings.Contains(res.Stdout, "HTTP 201") {
					t.Errorf("expected stdout to contain HTTP 201, got: %s", res.Stdout)
				}
			},
		},
		{
			name: "Header and output json",
			args: []string{"POST", ts.URL + "/post", "--header", "X-Custom: my-val", "--output", "json"},
			checkResult: func(t *testing.T, res *Result) {
				var data map[string]interface{}
				if err := json.Unmarshal([]byte(res.Stdout), &data); err != nil {
					t.Fatalf("failed to decode json output: %v", err)
				}
				
				if data["status_code"].(float64) != 201 {
					t.Errorf("expected status 201, got %v", data["status_code"])
				}
				headers, ok := data["response_headers"].(map[string]interface{})
				if !ok {
					t.Fatalf("expected response_headers object in json output")
				}
				if headers["X-Custom-Header"] != "my-val" {
					t.Errorf("expected X-Custom-Header response to echo my-val, got %v", headers["X-Custom-Header"])
				}
			},
		},
		{
			name: "404 is an error",
			args: []string{"GET", ts.URL + "/status/404"},
			wantErr: true,
			errContains: "HTTP 404",
		},
		{
			name: "Unreachable target",
			args: []string{"GET", "http://localhost:1"}, // Assuming nothing listening on port 1
			wantErr: true,
			errContains: "connection refused",
		},
		{
			name: "Invalid method",
			args: []string{"INVALID", ts.URL},
			wantErr: true,
			errContains: "invalid or unsupported HTTP method",
		},
		{
			name: "Missing args",
			args: []string{"GET"},
			wantErr: true,
			errContains: ".http requires at least METHOD and URL",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := client.Execute(ctx, tc.args, IterationData{})
			if (err != nil) != tc.wantErr {
				t.Fatalf("Execute() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr && !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("expected error containing %q, got: %v", tc.errContains, err)
			}
			if !tc.wantErr && tc.checkResult != nil {
				tc.checkResult(t, res)
			}
		})
	}
}

func TestHTTPClient_OutputJSONL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/jsonl", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "hello"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := NewHTTPClient()
	res, err := client.Execute(context.Background(), []string{"GET", ts.URL + "/jsonl", "--output", "jsonl"}, IterationData{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// JSONL should not contain newlines inside the JSON string itself, except possibly the trailing newline
	// The json Encoder indent makes it multiline, so we need to ensure jsonl is compact.
	compactJSON := strings.TrimSpace(res.Stdout)
	if strings.Contains(compactJSON, "\n") {
		t.Errorf("expected compact JSONL output, got multiple lines: %s", compactJSON)
	}
	
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(res.Stdout), &data); err != nil {
		t.Fatalf("failed to decode jsonl output: %v", err)
	}
	if data["status_code"].(float64) != 200 {
		t.Errorf("expected status 200")
	}
}
