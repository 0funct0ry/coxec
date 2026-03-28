package cli

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseUserVars(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		want    map[string]string
		wantErr bool
	}{
		{
			name:  "standard key=value",
			input: []string{"env=prod", "limit=500"},
			want:  map[string]string{"env": "prod", "limit": "500"},
		},
		{
			name:  "repeated keys",
			input: []string{"x=1", "x=2"},
			want:  map[string]string{"x": "2"},
		},
		{
			name:  "equals in value",
			input: []string{"filter=status=active"},
			want:  map[string]string{"filter": "status=active"},
		},
		{
			name:  "comma in value",
			input: []string{"filter=status=active,priority>=3"},
			want:  map[string]string{"filter": "status=active,priority>=3"},
		},
		{
			name:  "empty value",
			input: []string{"debug="},
			want:  map[string]string{"debug": ""},
		},
		{
			name:    "missing equals",
			input:   []string{"invalid"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUserVars(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseUserVars() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseUserVars() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTLSFlagValidation(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErr    bool
		errContain string
	}{
		{
			name:    "no tls flags",
			args:    []string{"-s", "--port", "0"},
			wantErr: false,
		},
		{
			name:       "both tls flags",
			args:       []string{"-s", "--tls-cert", "cert.pem", "--tls-key", "key.pem", "--port", "0"},
			wantErr:    true,
			errContain: "tls:", // Fails because files are invalid PEM, but flags are accepted
		},
		{
			name:       "only cert flag",
			args:       []string{"-s", "--tls-cert", "cert.pem", "--port", "0"},
			wantErr:    true,
			errContain: "both --tls-cert and --tls-key must be provided",
		},
		{
			name:       "only key flag",
			args:       []string{"-s", "--tls-key", "key.pem", "--port", "0"},
			wantErr:    true,
			errContain: "both --tls-cert and --tls-key must be provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCmd()
			
			ctx, cancel := context.WithCancel(context.Background())
			// Cancel immediately if not expecting error, or after a short delay
			// if we want to test server startup.
			// Create dummy files for "both tls flags" test case if needed
			if tt.name == "both tls flags" {
				os.WriteFile("cert.pem", []byte("cert"), 0644)
				os.WriteFile("key.pem", []byte("key"), 0644)
				defer os.Remove("cert.pem")
				defer os.Remove("key.pem")
			} else if tt.name == "only cert flag" {
				os.WriteFile("cert.pem", []byte("cert"), 0644)
				defer os.Remove("cert.pem")
			} else if tt.name == "only key flag" {
				os.WriteFile("key.pem", []byte("key"), 0644)
				defer os.Remove("key.pem")
			}

			if !tt.wantErr {
				go func() {
					time.Sleep(50 * time.Millisecond)
					cancel()
				}()
			} else {
				defer cancel()
			}

			cmd.SetArgs(tt.args)
			err := cmd.ExecuteContext(ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("execute error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errContain != "" && !strings.Contains(err.Error(), tt.errContain) {
				t.Errorf("error %v does not contain %q", err, tt.errContain)
			}
		})
	}
}

func TestNamedJobConfigLoading(t *testing.T) {
	configContent := `
server:
  jobs:
    - name: "job1"
      exec: "echo 1"
jobs:
  - name: "job2"
    exec: "echo 2"
named_jobs:
  - name: "job3"
    exec: "echo 3"
`
	os.WriteFile("test-jobs.yaml", []byte(configContent), 0644)
	defer os.Remove("test-jobs.yaml")

	cmd := NewRootCmd()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	cmd.SetArgs([]string{"-s", "--config", "test-jobs.yaml", "--port", "0"})
	
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := cmd.ExecuteContext(ctx)
	if err != nil && err != context.Canceled {
		t.Errorf("expected no error or context.Canceled, got %v", err)
	}
}
