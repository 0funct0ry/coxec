package engine

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestTCPClient_Execute(t *testing.T) {
	// Start a local echo server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	host, port, _ := net.SplitHostPort(addr)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				n, err := c.Read(buf)
				if err == nil && n > 0 {
					c.Write(buf[:n])
				}
			}(conn)
		}
	}()

	client := NewTCPClient()
	ctx := context.Background()
	data := IterationData{Iteration: 0, WorkerID: 0}

	tests := []struct {
		name           string
		args           []string
		expectedStatus string
		expectErr      bool
	}{
		{
			name:           "Successful connection and echo",
			args:           []string{host, port, "--body", "hello"},
			expectedStatus: "TCP OK",
			expectErr:      false,
		},
		{
			name:           "Connection refused",
			args:           []string{"127.0.0.1", "65535"}, // Likely unused port
			expectedStatus: "TCP ERROR",
			expectErr:      true,
		},
		{
			name:           "Invalid host/port",
			args:           []string{"invalid-host", "abc"},
			expectedStatus: "",
			expectErr:      true,
		},
		{
			name:           "JSON output",
			args:           []string{host, port, "--body", "ping", "--output", "json"},
			expectedStatus: "\"connected\": true",
			expectErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := client.Execute(ctx, tt.args, data)
			if (err != nil) != tt.expectErr {
				t.Errorf("Execute() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if tt.expectedStatus != "" && !strings.Contains(res.Stdout, tt.expectedStatus) {
				t.Errorf("Execute() stdout = %v, expected to contain %v", res.Stdout, tt.expectedStatus)
			}
		})
	}
}

func TestTCPClient_Timeout(t *testing.T) {
	// A port that doesn't answer (using a non-routable IP in a test might be slow, 
	// but we'll use a local port that we just don't accept on)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	// Note: we DON'T Accept() here to simulate a port that's open but not responding (though Connect logic might still succeed)
	// Actually, Connect logic usually completes once the kernel handshakes. 
	// To test timeout, we'd need a dropped packet or a very slow handshake.
	// We'll just test the timeout flag parsing and that it's used.
	
	addr := ln.Addr().String()
	host, port, _ := net.SplitHostPort(addr)
	defer ln.Close()

	client := NewTCPClient()
	ctx := context.Background()
	data := IterationData{Iteration: 0, WorkerID: 0}

	// Test invalid timeout
	_, err = client.Execute(ctx, []string{host, port, "--timeout", "invalid"}, data)
	if err == nil || !strings.Contains(err.Error(), "invalid timeout") {
		t.Errorf("Expected error for invalid timeout, got %v", err)
	}
}
