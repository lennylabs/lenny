// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the once-per-pod intra-pod
// MCP start.
//
// The platform MCP server and the per-connector servers bind the sockets
// the controller renders for the whole pod, so at most one start may arm
// them. The decision is taken inside the same critical section that claims
// the session's slot rather than as a read followed by a bind: two
// concurrent starts that both observed the surface free would hand the
// loser EADDRINUSE, and its rollback would cancel the winner's servers
// through the shared cancel, leaving the pod with a manifest naming a
// socket nothing serves.
//
// Both legs run two starts for different sessions with no ordering between
// them. The second leg uses Resume as the second caller, because Resume
// reaches the arming through its own site and a guard applied to the
// StartSession body alone passes the first leg and fails this one.
//
// Which of the two nonces the live server holds is not asserted. The pod
// has one manifest file and the two writes are unordered, which is the
// collision the proposal records as a limit rather than a behavior.
//
// spec: §4.7 (session start), §15.4.3 (intra-pod MCP surface, nonce
// handshake).
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/mcp"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// raceConnectorID is the one §9.3 connector the pod's sessions resolve, so
// the race also covers the per-connector sockets the same claim arms.
const raceConnectorID = "github"

// staticForwarder resolves one connector and answers the intra-pod tool
// surface. It satisfies both adapter.PlatformToolForwarder and
// adapter.ConnectorToolForwarder.
type staticForwarder struct{}

func (staticForwarder) ListPlatformTools(context.Context, string) ([]mcp.Tool, error) {
	return []mcp.Tool{{Name: "lenny/delegate_task", Description: "delegate a subtask"}}, nil
}

func (staticForwarder) CallPlatformTool(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"content":[]}`), nil
}

func (staticForwarder) ListSessionConnectors(context.Context, string) ([]mcp.ConnectorRef, error) {
	return []mcp.ConnectorRef{{ID: raceConnectorID, DisplayName: "GitHub"}}, nil
}

func (staticForwarder) ListConnectorTools(context.Context, string, string) ([]mcp.Tool, error) {
	return []mcp.Tool{{Name: "list_repos", Description: "list repos"}}, nil
}

func (staticForwarder) CallConnectorTool(context.Context, string, string, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"content":[]}`), nil
}

// mcpRacePod returns an adapter serving the pod-wide intra-pod MCP sockets
// with a runtime that does not park, so nothing but the scheduler orders
// the two starts.
func mcpRacePod(t *testing.T) *adapter.Server {
	t.Helper()
	base := t.TempDir()
	s := adapter.New("test")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.WorkspaceRoot = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.ManifestDir = t.TempDir()
	s.MCPSocket = shortMCPSocket(t)
	s.Runtime = newGatedRuntime()
	s.PlatformForwarder = staticForwarder{}
	s.ConnectorForwarder = staticForwarder{}
	return s
}

// mcpSurfaceListening reports whether a server is bound to the socket and
// refusing an unauthenticated handshake, which is what a live intra-pod
// MCP server does with a nonce it was not armed with.
func mcpSurfaceListening(t *testing.T, socket string) bool {
	t.Helper()
	conn, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"_lennyNonce": "not-the-armed-nonce", "protocolVersion": "2025-03-26"},
	}); err != nil {
		return false
	}
	var resp map[string]json.RawMessage
	if err := dec.Decode(&resp); err != nil {
		// A server that drops an unauthenticated connection without a
		// reply is still a server bound to the socket.
		return true
	}
	if _, isErr := resp["error"]; !isErr {
		t.Error("the intra-pod MCP surface accepted a nonce it was never armed with")
	}
	return true
}

// racedManifest reads the one pod-global manifest both starts wrote.
func racedManifest(t *testing.T, s *adapter.Server) *adapter.Manifest {
	t.Helper()
	var m adapter.Manifest
	b, err := os.ReadFile(filepath.Join(s.ManifestDir, adapter.ManifestFilename))
	if err != nil {
		t.Fatalf("read adapter manifest: %v", err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode adapter manifest: %v", err)
	}
	return &m
}

// spec: 4.7 (session start), 15.4.3 (once-per-pod intra-pod MCP arming)
// diagnosis: two starts admitted onto one pod at once did not agree on
// which of them arms the pod's MCP sockets. A start that returns
// "start platform MCP server" lost a bind race the claim was supposed to
// settle, and its rollback cancels the winner's servers through the shared
// cancel, so the surviving session holds a manifest naming a socket nothing
// serves and every §15.4.3 handshake on that pod fails for the life of the
// pod.
func TestConcurrentStartsArmThePodMCPSurfaceOnce_spec_15_4_3(t *testing.T) {
	for _, second := range []string{"start", "resume"} {
		t.Run(second, func(t *testing.T) {
			s := mcpRacePod(t)
			ctx := context.Background()

			var wg sync.WaitGroup
			errs := make([]error, 2)
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, errs[0] = s.StartSession(ctx, &adapterv1.StartSessionRequest{
					SessionId: &adapterv1.SessionId{Value: "alice"}, Runtime: "echo",
				})
			}()
			go func() {
				defer wg.Done()
				if second == "resume" {
					_, errs[1] = s.Resume(ctx, &adapterv1.ResumeRequest{
						SessionId:    &adapterv1.SessionId{Value: "bob"},
						CheckpointId: "ckpt-1",
					})
					return
				}
				_, errs[1] = s.StartSession(ctx, &adapterv1.StartSessionRequest{
					SessionId: &adapterv1.SessionId{Value: "bob"}, Runtime: "echo",
				})
			}()
			wg.Wait()

			for i, err := range errs {
				if err != nil {
					t.Fatalf("call %d on the shared pod failed: %v", i, err)
				}
			}
			// Both sessions reached the runtime, so both hold a live slot
			// on the pod rather than one having rolled its claim back.
			if got := s.SoleSessionID(); got != "" {
				t.Errorf("SoleSessionID on a pod given two sessions = %q, want empty", got)
			}
			m := racedManifest(t, s)
			if m.SessionID != "alice" && m.SessionID != "bob" {
				t.Errorf("manifest sessionId = %q, want one of the two starting sessions", m.SessionID)
			}
			if m.MCPNonce == "" {
				t.Error("the manifest carries no MCP nonce after both starts returned")
			}
			if m.PlatformMcpServer == nil || m.PlatformMcpServer.Socket == "" {
				t.Fatalf("manifest carries no platform MCP socket: %+v", m.PlatformMcpServer)
			}
			if len(m.ConnectorServers) != 1 || m.ConnectorServers[0].ID != raceConnectorID {
				t.Fatalf("manifest connector servers = %+v, want the one resolved connector", m.ConnectorServers)
			}
			// Neither call cancelled the other's servers: the pod's
			// platform socket and its one connector socket are both still
			// served after both calls returned.
			if !mcpSurfaceListening(t, m.PlatformMcpServer.Socket) {
				t.Error("no server is bound to the pod's platform MCP socket after both starts returned")
			}
			if !mcpSurfaceListening(t, m.ConnectorServers[0].Socket) {
				t.Error("no server is bound to the pod's connector MCP socket after both starts returned")
			}

			t.Cleanup(func() {
				for _, id := range []string{"alice", "bob"} {
					_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
						SessionId: &adapterv1.SessionId{Value: id},
					})
				}
			})
		})
	}
}
