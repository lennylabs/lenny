// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local ordering coverage for the once-per-pod intra-pod MCP
// arming across a session handoff on one pod.
//
// The arming state has two writers that interleave. A start claims the
// pod-wide platform and connector MCP servers inside its own critical
// section and arms them on the nonce it wrote into the pod manifest. A
// release cancels those servers when the pod's shared runtime process is
// serving no session. The window this case drives is the one in which the
// two cross: the departing session's entry is already out of the slot
// registry, its Runtime.Close has not returned, the successor's start
// claims the pod and arms on a fresh nonce, and only then does the
// departing session's close return and its release evaluate the gate.
//
// The property is that a session whose start returned finds a server
// listening that authenticates the nonce its manifest carries. The
// alternative is silent: the successor holds a manifest naming a socket
// nothing serves, its own claim has already returned, and no later claim
// re-arms for the life of the pod, so every tool call the runtime makes
// fails the §15.4.3 handshake.
//
// spec: §15.4.3 (intra-pod MCP surface, nonce handshake), §5.2 (slot
// registry, one claim per session), §4.7 (Shutdown teardown order).
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
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// gatedRuntime is a RuntimeProcess whose Start and Close park on a gate so
// a case can hold one session's teardown open across another session's
// start. A nil gate does not park.
type gatedRuntime struct {
	mu sync.Mutex
	// startGate parks Start for the named session until the channel is
	// closed; entering Start closes the matching entry of startEntered.
	startGate    map[string]chan struct{}
	startEntered map[string]chan struct{}
	closeGate    map[string]chan struct{}
	closeEntered map[string]chan struct{}
}

func newGatedRuntime() *gatedRuntime {
	return &gatedRuntime{
		startGate:    map[string]chan struct{}{},
		startEntered: map[string]chan struct{}{},
		closeGate:    map[string]chan struct{}{},
		closeEntered: map[string]chan struct{}{},
	}
}

// gate arms a park on one session's Start or Close and returns the channel
// that reports the call has entered and the channel that releases it.
func (g *gatedRuntime) gate(sessionID string, onClose bool) (entered, release chan struct{}) {
	entered, release = make(chan struct{}), make(chan struct{})
	g.mu.Lock()
	defer g.mu.Unlock()
	if onClose {
		g.closeEntered[sessionID], g.closeGate[sessionID] = entered, release
	} else {
		g.startEntered[sessionID], g.startGate[sessionID] = entered, release
	}
	return entered, release
}

func (g *gatedRuntime) park(sessionID string, onClose bool) {
	g.mu.Lock()
	entered, release := g.startEntered[sessionID], g.startGate[sessionID]
	if onClose {
		entered, release = g.closeEntered[sessionID], g.closeGate[sessionID]
	}
	g.mu.Unlock()
	if entered == nil {
		return
	}
	close(entered)
	<-release
}

func (g *gatedRuntime) Start(_ context.Context, sessionID string) error {
	g.park(sessionID, false)
	return nil
}

func (g *gatedRuntime) Close(_ context.Context, sessionID string) error {
	g.park(sessionID, true)
	return nil
}

func (g *gatedRuntime) WriteEnvelope(string, []byte) error { return nil }

func (g *gatedRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (g *gatedRuntime) Interrupt(context.Context, string, bool) error { return nil }

// manifestNonce reads the §15.4.3 nonce from the pod manifest the last
// start wrote.
func manifestNonce(t *testing.T, manifestDir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(manifestDir, adapter.ManifestFilename))
	if err != nil {
		t.Fatalf("read adapter manifest: %v", err)
	}
	var m adapter.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode adapter manifest: %v", err)
	}
	return m.MCPNonce
}

// mcpInitializeAccepted dials the platform MCP socket and reports whether
// the server completes the §15.4.3 initialize handshake for the nonce.
func mcpInitializeAccepted(t *testing.T, socket, nonce string) bool {
	t.Helper()
	conn, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"_lennyNonce": nonce, "protocolVersion": "2025-03-26"},
	}); err != nil {
		return false
	}
	var resp map[string]json.RawMessage
	if err := dec.Decode(&resp); err != nil {
		return false
	}
	_, isErr := resp["error"]
	return !isErr && resp["result"] != nil
}

// waitClosed fails the case when the channel does not close in time,
// rather than hanging the suite on a lost interleaving.
func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// spec: 15.4.3 (once-per-pod MCP arming), 5.2 (slot registry), 4.7
// (Shutdown teardown order)
// diagnosis: the successor session's start returned with a manifest nonce
// no server accepts. The departing session's release cancelled the pod's
// MCP surface after the successor's claim had armed it, and the successor
// cannot re-arm because its own claim has already returned. Every
// §15.4.3 handshake on that pod fails from then on.
func TestPodMCPArmingSurvivesSessionHandoff_spec_15_4_3(t *testing.T) {
	base := t.TempDir()
	manifestDir := t.TempDir()
	rt := newGatedRuntime()
	s := adapter.New("test")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.ManifestDir = manifestDir
	s.MCPSocket = shortMCPSocket(t)
	s.Runtime = rt

	ctx := context.Background()
	if _, err := s.StartSession(ctx, &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: "alice"}, Runtime: "echo",
	}); err != nil {
		t.Fatalf("start alice: %v", err)
	}

	// alice departs: hold the teardown open inside Runtime.Close, after
	// the locked step took her entry out of the slot registry.
	closeEntered, releaseClose := rt.gate("alice", true)
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		_, _ = s.Shutdown(ctx, &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "alice"},
		})
	}()
	waitClosed(t, closeEntered, "alice's runtime close to begin")

	// bob starts in that window and parks inside Runtime.Start, after the
	// claim, the manifest write, and the MCP arming.
	startEntered, releaseStart := rt.gate("bob", false)
	startDone := make(chan struct{})
	var startErr error
	go func() {
		defer close(startDone)
		_, startErr = s.StartSession(ctx, &adapterv1.StartSessionRequest{
			SessionId: &adapterv1.SessionId{Value: "bob"}, Runtime: "echo",
		})
	}()
	waitClosed(t, startEntered, "bob's runtime start to begin")

	// alice's close returns and her release evaluates the pod-surface gate
	// while bob's start has not yet returned.
	close(releaseClose)
	waitClosed(t, shutdownDone, "alice's Shutdown to return")
	close(releaseStart)
	waitClosed(t, startDone, "bob's StartSession to return")
	if startErr != nil {
		t.Fatalf("start bob: %v", startErr)
	}

	nonce := manifestNonce(t, manifestDir)
	if nonce == "" {
		t.Fatal("the pod manifest carries no MCP nonce for the surviving session")
	}
	if !mcpInitializeAccepted(t, s.MCPSocket, nonce) {
		t.Error("the platform MCP server does not authenticate the surviving session's manifest nonce")
	}

	t.Cleanup(func() {
		_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "bob"},
		})
	})
}

// shortMCPSocket returns a platform MCP socket path short enough for the
// sockaddr_un limit.
func shortMCPSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-mcp-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "p.sock")
}
