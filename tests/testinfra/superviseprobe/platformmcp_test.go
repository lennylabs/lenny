// SPDX-License-Identifier: MIT

package superviseprobe

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// gatedProvider refuses tools/list with listErr and records each call.
type gatedProvider struct {
	listErr error
	calls   []string
}

func (p *gatedProvider) List(context.Context) ([]mcp.Tool, error) {
	if p.listErr != nil {
		return nil, p.listErr
	}
	return []mcp.Tool{{Name: "lenny/get_task_tree"}}, nil
}

func (p *gatedProvider) Call(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
	p.calls = append(p.calls, name)
	return json.RawMessage(`{"content":[]}`), nil
}

// servePlatformMCP serves an adapter MCP server backed by p on a fresh
// socket and writes a manifest advertising it, returning the manifest path.
func servePlatformMCP(t *testing.T, p mcp.ToolProvider, nonce string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sp-mcp")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "p.sock")
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := mcp.NewServer()
	srv.Provider = p
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ctx, lis, nonce) }()
	t.Cleanup(func() { cancel(); <-done })
	manifest := filepath.Join(dir, "adapter-manifest.json")
	body, _ := json.Marshal(map[string]any{"mcpNonce": nonce, "platformMcpServer": map[string]any{"socket": socket}})
	if err := os.WriteFile(manifest, body, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return manifest
}

// spec: §15.4.3 (Authentication), §4.7.11 item 7, §4.7.10 (Runtime
// supervisor, Launch)
//
// TestProbeStartsWhenTheNoSessionCatalogDiscoveryIsRefused pins the probe's
// startup against the calling-session rule: a warm-up process holds no
// session, so the adapter refuses its catalog discovery with
// FailedPrecondition, and the probe must keep its authenticated connection
// and serve a later platform tool call on it. Any other discovery failure,
// and a nonce the running server does not validate, leave the process with
// no connection and a platform MCP command that reports why.
func TestProbeStartsWhenTheNoSessionCatalogDiscoveryIsRefused(t *testing.T) {
	refused := status.Error(codes.FailedPrecondition, "no single calling session")
	t.Run("no-session refusal keeps the connection", func(t *testing.T) {
		p := &gatedProvider{listErr: refused}
		tools := openPlatformMCP(servePlatformMCP(t, p, "warm-nonce"))
		if tools.err != nil || tools.conn == nil {
			t.Fatalf("openPlatformMCP = %+v; want a connection", tools)
		}
		defer tools.close()
		if res := callPlatformMCP(tools); !res.OK {
			t.Fatalf("platform tool call on the warm-up connection failed: %s", res.Error)
		}
		if len(p.calls) != 1 {
			t.Fatalf("provider saw %d calls, want 1", len(p.calls))
		}
	})
	t.Run("another discovery failure leaves no connection", func(t *testing.T) {
		p := &gatedProvider{listErr: errors.New("catalog backend down")}
		tools := openPlatformMCP(servePlatformMCP(t, p, "warm-nonce"))
		defer tools.close()
		if tools.conn != nil || tools.err == nil {
			t.Fatal("openPlatformMCP kept a connection although the catalog discovery failed for another reason")
		}
		if res := callPlatformMCP(tools); res.OK || !strings.Contains(res.Error, "catalog backend down") {
			t.Fatalf("callPlatformMCP = %+v; want the discovery failure reported", res)
		}
	})
	t.Run("a nonce the server does not validate leaves no connection", func(t *testing.T) {
		manifest := servePlatformMCP(t, &gatedProvider{}, "warm-nonce")
		body, _ := os.ReadFile(manifest)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		m["mcpNonce"] = "session-nonce"
		body, _ = json.Marshal(m)
		_ = os.WriteFile(manifest, body, 0o600)
		tools := openPlatformMCP(manifest)
		defer tools.close()
		if tools.conn != nil || tools.err == nil {
			t.Fatal("openPlatformMCP authenticated with a nonce the running server does not validate")
		}
		if res := callPlatformMCP(tools); res.OK {
			t.Fatal("callPlatformMCP reported OK with no connection")
		}
	})
	t.Run("no platform MCP server in the manifest", func(t *testing.T) {
		dir := t.TempDir()
		manifest := filepath.Join(dir, "adapter-manifest.json")
		_ = os.WriteFile(manifest, []byte(`{"mcpNonce":"n"}`), 0o600)
		for _, path := range []string{manifest, filepath.Join(dir, "absent.json")} {
			tools := openPlatformMCP(path)
			if tools.conn != nil || !errors.Is(tools.err, errNoPlatformMCP) {
				t.Fatalf("openPlatformMCP(%s) = %+v; want no connection and errNoPlatformMCP", path, tools)
			}
		}
	})
}
