package lsp

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/jsonrpc"
)

// TestInitializeReportsScanLimits checks that the initialize result carries the
// scan's own limits, with the experimental feature on and off.
func TestInitializeReportsScanLimits(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		opts map[string]any
	}{
		{"feature off", map[string]any{}},
		{"feature on", map[string]any{"experimental": []string{"crd-typed-policies"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			serverIn, clientOut := io.Pipe()
			clientIn, serverOut := io.Pipe()
			ss := newSession(t)
			ss.conn = jsonrpc.NewConn(serverIn, serverOut)
			ctx, cancel := context.WithCancel(context.Background())
			served := make(chan error, 1)
			go func() { served <- ss.serve(ctx, serverIn) }()
			t.Cleanup(func() {
				cancel()
				_ = clientOut.Close()
				_ = serverOut.Close()
				<-served
			})

			c := jsonrpc.NewConn(clientIn, clientOut)
			if err := c.Request("1", "initialize", map[string]any{"initializationOptions": tc.opts}); err != nil {
				t.Fatal(err)
			}
			m := readMessage(t, c)
			var r struct {
				Capabilities struct {
					Experimental struct {
						CelLens struct {
							MaxFiles     int `json:"workspaceScanMaxFiles"`
							MaxFileBytes int `json:"workspaceScanMaxFileBytes"`
						} `json:"celLens"`
					} `json:"experimental"`
				} `json:"capabilities"`
			}
			if err := json.Unmarshal(m.Result, &r); err != nil {
				t.Fatalf("initialize result %s: %v", m.Result, err)
			}
			got := r.Capabilities.Experimental.CelLens
			if got.MaxFiles != maxWorkspaceFiles {
				t.Errorf("initialize workspaceScanMaxFiles = %d, want %d", got.MaxFiles, maxWorkspaceFiles)
			}
			if got.MaxFileBytes != maxWorkspaceFileSize {
				t.Errorf("initialize workspaceScanMaxFileBytes = %d, want %d", got.MaxFileBytes, maxWorkspaceFileSize)
			}
		})
	}
}
