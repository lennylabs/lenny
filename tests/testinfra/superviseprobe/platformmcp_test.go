// SPDX-License-Identifier: MIT

package superviseprobe

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
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
// and a wrong nonce, still end the start.
func TestProbeStartsWhenTheNoSessionCatalogDiscoveryIsRefused(t *testing.T) {
	refused := status.Error(codes.FailedPrecondition, "no single calling session")
	t.Run("no-session refusal is not fatal", func(t *testing.T) {
		p := &gatedProvider{listErr: refused}
		tools, err := openPlatformMCP(servePlatformMCP(t, p, "warm-nonce"))
		if err != nil || tools == nil {
			t.Fatalf("openPlatformMCP = %v, %v; want a connection", tools, err)
		}
		defer tools.close()
		if res := callPlatformMCP(tools); !res.OK {
			t.Fatalf("platform tool call on the warm-up connection failed: %s", res.Error)
		}
		if len(p.calls) != 1 {
			t.Fatalf("provider saw %d calls, want 1", len(p.calls))
		}
	})
	t.Run("another discovery failure is fatal", func(t *testing.T) {
		p := &gatedProvider{listErr: errors.New("catalog backend down")}
		if tools, err := openPlatformMCP(servePlatformMCP(t, p, "warm-nonce")); err == nil {
			tools.close()
			t.Fatal("openPlatformMCP succeeded although the catalog discovery failed for another reason")
		}
	})
	t.Run("a wrong nonce is fatal", func(t *testing.T) {
		manifest := servePlatformMCP(t, &gatedProvider{}, "warm-nonce")
		body, _ := os.ReadFile(manifest)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		m["mcpNonce"] = "other-nonce"
		body, _ = json.Marshal(m)
		_ = os.WriteFile(manifest, body, 0o600)
		if tools, err := openPlatformMCP(manifest); err == nil {
			tools.close()
			t.Fatal("openPlatformMCP succeeded with a nonce the server does not validate")
		}
	})
	t.Run("no platform MCP server in the manifest", func(t *testing.T) {
		dir := t.TempDir()
		manifest := filepath.Join(dir, "adapter-manifest.json")
		_ = os.WriteFile(manifest, []byte(`{"mcpNonce":"n"}`), 0o600)
		tools, err := openPlatformMCP(manifest)
		if err != nil || tools != nil {
			t.Fatalf("openPlatformMCP = %v, %v; want no connection and no error", tools, err)
		}
		if res := callPlatformMCP(nil); res.OK {
			t.Fatal("callPlatformMCP with no connection reported OK")
		}
		if tools, err := openPlatformMCP(filepath.Join(dir, "absent.json")); err != nil || tools != nil {
			t.Fatalf("openPlatformMCP on an absent manifest = %v, %v; want no connection and no error", tools, err)
		}
	})
}
