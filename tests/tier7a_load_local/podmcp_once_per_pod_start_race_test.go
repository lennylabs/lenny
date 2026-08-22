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
// The live server keeps the nonce of the start that armed it, whether or
// not the manifest still carries that nonce: the pod holds one manifest
// file and the two writes are unordered, so which of the two documents
// survives is a separate, interleaving-dependent property. The case
// therefore collects both starts' published nonces and asserts that
// exactly one of them opens the pod's sockets.
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

// initializeFrame is the §15.4.3 nonce-bearing initialize a conforming
// runtime opens an intra-pod MCP connection with.
func initializeFrame(nonce string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"_lennyNonce": nonce, "protocolVersion": "2025-03-26"},
	}
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
	if err := enc.Encode(initializeFrame("not-the-armed-nonce")); err != nil {
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

// nonceAuthenticates reports whether the server bound to socket completes
// the §15.4.3 handshake for nonce, which is the statement that this is the
// nonce it was armed with.
func nonceAuthenticates(t *testing.T, socket, nonce string) bool {
	t.Helper()
	conn, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := enc.Encode(initializeFrame(nonce)); err != nil {
		return false
	}
	var resp map[string]json.RawMessage
	if err := dec.Decode(&resp); err != nil {
		return false
	}
	_, isErr := resp["error"]
	return !isErr
}

// nonceWatcher reads the pod's one manifest file while the two starts run
// and records every distinct nonce published on it. Each write is
// published by rename, so a reader observes one whole document at a time
// and the losing start's nonce is recoverable even though its document
// does not survive.
type nonceWatcher struct {
	mu   sync.Mutex
	seen map[string]string // nonce -> the sessionId published beside it
	stop chan struct{}
	done chan struct{}
}

// watchPublishedNonces starts a watcher over the manifest in dir.
func watchPublishedNonces(dir string) *nonceWatcher {
	w := &nonceWatcher{
		seen: map[string]string{},
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	path := filepath.Join(dir, adapter.ManifestFilename)
	go func() {
		defer close(w.done)
		for {
			select {
			case <-w.stop:
				return
			default:
			}
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var m adapter.Manifest
			if json.Unmarshal(b, &m) != nil || m.MCPNonce == "" {
				continue
			}
			w.mu.Lock()
			w.seen[m.MCPNonce] = m.SessionID
			w.mu.Unlock()
		}
	}()
	return w
}

// close stops the watcher and returns the nonce-to-session pairs it
// observed.
func (w *nonceWatcher) close() map[string]string {
	close(w.stop)
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]string, len(w.seen))
	for nonce, sessionID := range w.seen {
		out[nonce] = sessionID
	}
	return out
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
			// Both starts publish the pod's one manifest, and only one of
			// the two documents survives. Watching the file through the
			// race recovers the nonce of the start whose document did not.
			watcher := watchPublishedNonces(s.ManifestDir)

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
			published := watcher.close()
			m := racedManifest(t, s)
			if m.SessionID != "alice" && m.SessionID != "bob" {
				t.Errorf("manifest sessionId = %q, want one of the two starting sessions", m.SessionID)
			}
			if m.MCPNonce == "" {
				t.Error("the manifest carries no MCP nonce after both starts returned")
			}
			// The surviving document is one session's whole manifest: its
			// sessionId is paired with the nonce that session published,
			// rather than with its co-tenant's.
			published[m.MCPNonce] = m.SessionID
			for nonce, sessionID := range published {
				if sessionID != "alice" && sessionID != "bob" {
					t.Errorf("a published manifest paired nonce %q with sessionId %q, want one of the two starting sessions", nonce, sessionID)
				}
			}
			if len(published) > 2 {
				t.Errorf("the pod published %d distinct MCP nonces for two starts", len(published))
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
			// The surviving servers keep the nonce of the start that armed
			// them. Exactly one of the two published nonces authenticates
			// on the platform socket and on the connector socket, whether
			// or not it is the nonce the surviving manifest carries: the
			// loser neither re-armed the live server nor re-keyed it
			// through its later manifest write.
			assertOneArmedNonce(t, "platform", m.PlatformMcpServer.Socket, published)
			assertOneArmedNonce(t, "connector", m.ConnectorServers[0].Socket, published)

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

// assertOneArmedNonce checks that exactly one of the nonces the pod
// published opens the server bound to socket, which is the statement that
// the surviving server still answers the nonce its own start armed it
// with. spec: §15.4.3.
func assertOneArmedNonce(t *testing.T, surface, socket string, published map[string]string) {
	t.Helper()
	var armed []string
	for nonce := range published {
		if nonceAuthenticates(t, socket, nonce) {
			armed = append(armed, published[nonce])
		}
	}
	if len(armed) != 1 {
		t.Errorf("%s socket authenticated %d of the %d published nonces (sessions %v), want exactly one",
			surface, len(armed), len(published), armed)
	}
}
