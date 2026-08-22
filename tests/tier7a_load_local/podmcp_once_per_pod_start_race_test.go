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
// Both legs run two starts for different sessions from a rendezvous, so
// the two calls are in the window between the arming read and the socket
// bind at the same instant, and both legs repeat over fresh pods so both
// orderings out of that window are reached. The second leg uses Resume as
// the second caller, because Resume reaches the arming through its own
// site and a guard applied to the StartSession body alone passes the first
// leg and fails this one.
//
// The case carries a stress budget:
//
//	lenny-test stress --test TestConcurrentStartsArmThePodMCPSurfaceOnce_spec_15_4_3 --runs 50 --pkg ./tests/tier7a_load_local/... --tag load_local
//
// The live server keeps the nonce of the start that armed it, whether or
// not the manifest still carries that nonce: the pod holds one manifest
// file, both starts rewrite it, and which document survives is a separate,
// interleaving-dependent property. The case reads the arming from the
// adapter and asserts that it names one of the two starting sessions and
// that its nonce opens both of the pod's intra-pod sockets, which is what
// a loser that cancelled and rebound the winner's servers breaks. The
// sockets also refuse a nonce no start published, and the surviving
// document, whichever of the two rewrites landed last, names one of the
// two starting sessions, carries a nonce, and names those two sockets.
//
// spec: §4.7 (session start), §15.4.3 (intra-pod MCP surface, nonce
// handshake).
package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// racedManifest reads the one pod-global manifest both starts wrote. Each
// start publishes the file as one whole document, so whichever write
// landed last the file decodes as exactly that session's manifest and the
// incumbent's values do not survive. A residue that decodes as neither is
// a publication defect and fails the case here.
func racedManifest(t *testing.T, s *adapter.Server) *adapter.Manifest {
	t.Helper()
	var m adapter.Manifest
	b, err := os.ReadFile(filepath.Join(s.ManifestDir, adapter.ManifestFilename))
	if err != nil {
		t.Fatalf("read adapter manifest: %v", err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("the manifest the two concurrent starts rewrote does not decode as one session's document: %v", err)
	}
	return &m
}

// podConnectorSocket is the intra-pod socket the adapter opens for the
// pod's one resolved connector, derived from the platform socket the same
// way the adapter derives it. The case reads it from here rather than from
// the manifest so the arming assertions hold whichever of the two
// concurrent rewrites the file ends on. spec: §9.3.
func podConnectorSocket(platformSocket string) string {
	return filepath.Join(filepath.Dir(platformSocket), "lenny-connector-"+raceConnectorID+".sock")
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
		for attempt := range raceAttempts {
			t.Run(fmt.Sprintf("%s_attempt_%d", second, attempt), func(t *testing.T) {
				podMCPStartRaceAttempt(t, second)
			})
		}
	}
}

// podMCPStartRaceAttempt builds a fresh pod and drives one start against
// one second caller from a rendezvous, so both calls are between their
// arming read and their socket bind at the same instant. An
// unsynchronized launch lets the second call begin after the first has
// already returned, which is the sequential ordering a merged-start pair
// already covers and which this case exists to go beyond. The case is
// repeated over fresh pods so both orderings out of that window are
// reached.
func podMCPStartRaceAttempt(t *testing.T, second string) {
	t.Helper()
	s := mcpRacePod(t)
	// Registered before the race so every path out of the case,
	// including the early return on an interleaved manifest, closes
	// both sessions' runtimes.
	t.Cleanup(func() {
		for _, id := range []string{"alice", "bob"} {
			_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
				SessionId: &adapterv1.SessionId{Value: id},
			})
		}
	})
	ctx := context.Background()
	rendezvous := newRaceStart(2)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		rendezvous.arrive()
		_, errs[0] = s.StartSession(ctx, &adapterv1.StartSessionRequest{
			SessionId: &adapterv1.SessionId{Value: "alice"}, Runtime: "echo",
		})
	}()
	go func() {
		defer wg.Done()
		rendezvous.arrive()
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
	rendezvous.release(t)
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
	// Neither call cancelled the other's servers: the pod's
	// platform socket and its one connector socket are both still
	// served after both calls returned, and each refuses a nonce no
	// start ever published. Both socket paths are pod-global, so
	// they are known without reading the raced manifest.
	connectorSocket := podConnectorSocket(s.MCPSocket)
	if !mcpSurfaceListening(t, s.MCPSocket) {
		t.Error("no server is bound to the pod's platform MCP socket after both starts returned")
	}
	if !mcpSurfaceListening(t, connectorSocket) {
		t.Error("no server is bound to the pod's connector MCP socket after both starts returned")
	}
	// The live servers keep the nonce of the start that armed them, and
	// that start is one of the two racing calls. A loser that cancelled
	// the winner's servers through the shared cancel and rebound them on
	// its own nonce leaves the arming intact but the winner's manifest
	// naming a nonce nothing answers, so the arming is read from the
	// adapter rather than from the raced file.
	armedSession, armedNonce := s.PodMCPArming()
	if armedSession != "alice" && armedSession != "bob" {
		t.Errorf("the pod's MCP arming names session %q, want one of the two starting sessions", armedSession)
	}
	if armedNonce == "" {
		t.Fatal("the pod's intra-pod MCP surface holds no armed nonce after both starts returned")
	}
	if !nonceAuthenticates(t, s.MCPSocket, armedNonce) {
		t.Error("the pod's platform MCP socket does not answer the nonce its arming start published")
	}
	if !nonceAuthenticates(t, connectorSocket, armedNonce) {
		t.Error("the pod's connector MCP socket does not answer the nonce its arming start published, so the two surfaces were armed by different starts")
	}
	m := racedManifest(t, s)
	// The surviving document is whichever write landed last. It names a
	// starting session, carries that session's nonce, and names the pod's
	// two intra-pod sockets.
	if m.SessionID != "alice" && m.SessionID != "bob" {
		t.Errorf("manifest sessionId = %q, want one of the two starting sessions", m.SessionID)
	}
	if m.MCPNonce == "" {
		t.Error("the manifest carries no MCP nonce after both starts returned")
	}
	if m.PlatformMcpServer == nil || m.PlatformMcpServer.Socket != s.MCPSocket {
		t.Fatalf("manifest platform MCP server = %+v, want the pod's socket %q", m.PlatformMcpServer, s.MCPSocket)
	}
	if len(m.ConnectorServers) != 1 || m.ConnectorServers[0].ID != raceConnectorID || m.ConnectorServers[0].Socket != connectorSocket {
		t.Fatalf("manifest connector servers = %+v, want the one resolved connector on %q", m.ConnectorServers, connectorSocket)
	}
}
