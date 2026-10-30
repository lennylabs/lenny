// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// warmUpServer is a Server holding f's supervised runtime, with a manifest
// directory and a platform MCP socket configured as on a sidecar pod.
func warmUpServer(t *testing.T, f *supervisedFixture, kind RuntimeKind) *Server {
	t.Helper()
	s := New("test")
	s.ManifestDir = t.TempDir()
	s.WorkspaceBase = t.TempDir()
	s.MCPSocket = mcpSocketPath(t, "p.sock")
	s.PeerAuth = SocketPeerAuth{ExpectedUID: uint32(os.Getuid())}
	s.RuntimeKind = kind
	s.UseSupervisedRuntime(f.rt)
	t.Cleanup(func() {
		s.mu.Lock()
		cancels := s.takePodMCPCancelsLocked()
		s.mu.Unlock()
		runCancels(cancels)
	})
	return s
}

// mcpConn is an initialized client connection to the platform MCP socket.
type mcpConn struct {
	conn net.Conn
	enc  *json.Encoder
	dec  *json.Decoder
}

// dialInitialized dials socket and runs the nonce-authenticated initialize
// handshake, returning nil when the dial or the handshake fails.
func dialInitialized(socket, nonce string) *mcpConn {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	c := &mcpConn{conn: conn, enc: json.NewEncoder(conn), dec: json.NewDecoder(conn)}
	if err := c.enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"_lennyNonce": nonce, "protocolVersion": "2025-03-26"},
	}); err != nil {
		_ = conn.Close()
		return nil
	}
	var resp map[string]json.RawMessage
	if err := c.dec.Decode(&resp); err != nil || resp["result"] == nil {
		_ = conn.Close()
		return nil
	}
	return c
}

// listsTools reports whether the connection still answers tools/list.
func (c *mcpConn) listsTools() bool {
	_ = c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := c.enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}); err != nil {
		return false
	}
	var resp map[string]json.RawMessage
	return c.dec.Decode(&resp) == nil && resp["result"] != nil
}

// spec: §4.7.10 (Runtime supervisor, Launch), §4.7.9 (step 4), §15.4.3
// diagnosis: a Standard-level runtime launched at warm-up on a service-mode or
// SDK-warm pod exits with "dial @lenny-platform-mcp: connection refused",
// because the warm-up launch was sent before the platform MCP listener the
// warm-up manifest advertises was bound; the service pod then never turns
// Ready. Read PrepareWarmUpLaunch and the startPlatformMCP call it makes.
func TestWarmUpLaunchFollowsThePlatformMCPListenerBind_spec_4_7_10(t *testing.T) {
	f := newSupervisedFixture(t, LifetimeKeep, LaunchService, nil)
	s := warmUpServer(t, f, RuntimeKindAgent)

	// The launch hook runs after the launch is decided and before the
	// launch request reaches the supervisor, which is when the process
	// starts and dials.
	var warm *mcpConn
	var warmNonce string
	f.rt.setLaunchHook(func() {
		m, err := ReadManifest(s.ManifestDir)
		if err != nil {
			return
		}
		warmNonce = m.MCPNonce
		warm = dialInitialized(s.MCPSocket, warmNonce)
	})
	if err := f.rt.WarmUp(startCtx(t, 5*time.Second)); err != nil {
		t.Fatalf("WarmUp: %v", err)
	}
	if warmNonce == "" {
		t.Fatal("no warm-up manifest with a nonce existed when the warm-up launch was sent")
	}
	if warm == nil {
		t.Fatal("the platform MCP listener did not accept the warm-up nonce when the warm-up launch was sent")
	}
	defer warm.conn.Close()

	if !s.mcpHandshakeWasSeen() {
		t.Fatal("the warm-up process's handshake was not recorded")
	}
}

// spec: §15.4.3 (Authentication), §4.7.6 (mcpNonce row), §4.7.11 item 7,
// §4.7.10 (Runtime supervisor, Launch), §5.1
// diagnosis: the pod-wide platform MCP server started twice on a supervised
// warm-up pod: the first session claim cancelled the server the warm-up bound
// and armed a second one on the session's nonce, so the warm-up process's
// connection was authenticated against a nonce the armed server no longer
// validated and the §5.1 handshake record was cleared. Read
// claimPodMCPStartLocked's warm-up arm, startClaimedPlatformMCP, and
// markWarmUpPlatformMCPArmed.
func TestFirstClaimAfterWarmUpKeepsTheWarmUpPlatformMCPServer_spec_15_4_3(t *testing.T) {
	f := newSupervisedFixture(t, LifetimeKeep, LaunchService, nil)
	s := warmUpServer(t, f, RuntimeKindAgent)
	var warm *mcpConn
	var warmNonce string
	f.rt.setLaunchHook(func() {
		if m, err := ReadManifest(s.ManifestDir); err == nil {
			warmNonce = m.MCPNonce
			warm = dialInitialized(s.MCPSocket, warmNonce)
		}
	})
	if err := f.rt.WarmUp(startCtx(t, 5*time.Second)); err != nil {
		t.Fatalf("WarmUp: %v", err)
	}
	if warm == nil {
		t.Fatal("the warm-up process could not connect to the platform MCP server")
	}
	defer warm.conn.Close()

	// A release path that finds the runtime idle before any claim must not
	// cancel the warm-up binding.
	s.cancelPodMCPIfRuntimeIdle()
	if _, nonce := s.PodMCPArming(); nonce != warmNonce {
		t.Fatalf("the idle cancellation before any claim disarmed the warm-up server: armed nonce %q, want the warm-up nonce", nonce)
	}

	claim, err := s.claimSessionSlot("alice", slotResolve{allowCreate: true}, false, false)
	if err != nil {
		t.Fatalf("claim alice: %v", err)
	}
	if !claim.startMCP || !claim.platformBound {
		t.Fatalf("first claim after warm-up: startMCP=%v platformBound=%v, want the once-per-pod start with the platform server already bound",
			claim.startMCP, claim.platformBound)
	}
	sessionNonce, err := s.writeSessionManifest(manifestInputs{})
	if err != nil {
		t.Fatalf("write the session manifest: %v", err)
	}
	if err := s.startClaimedPlatformMCP(claim, sessionNonce); err != nil {
		t.Fatalf("startClaimedPlatformMCP: %v", err)
	}
	if id, nonce := s.PodMCPArming(); id != "alice" || nonce != warmNonce {
		t.Fatalf("PodMCPArming = (%q, %q), want (alice, the warm-up nonce)", id, nonce)
	}
	if !s.mcpHandshakeWasSeen() {
		t.Fatal("the first claim cleared the warm-up process's handshake record")
	}
	if !warm.listsTools() {
		t.Fatal("the warm-up process's connection stopped being served after the first claim")
	}
	if c := dialInitialized(s.MCPSocket, warmNonce); c == nil {
		t.Fatal("the running server stopped validating the warm-up nonce after the session's manifest write")
	} else {
		_ = c.conn.Close()
	}
	if c := dialInitialized(s.MCPSocket, sessionNonce); c != nil {
		_ = c.conn.Close()
		t.Fatal("the session's manifest write re-armed the running server on the session nonce")
	}
}

// spec: §4.7.10 (Runtime supervisor, SDK-warm; Runtime supervisor, Launch),
// §4.7.6 (mcpNonce row), §15.4.3 (Authentication), §6.1
// diagnosis: after an idle DemoteSDK ended the SDK-warm process, the platform
// MCP server the warm-up preparation bound kept validating only the warm-up
// nonce, and the next session's claim took that server over as already bound,
// so the process launched at startup step 7 read the final manifest's nonce
// and its platform MCP initialize was refused. Read demoteIdleSDK,
// releaseWarmUpPlatformMCPLocked, and mcpArmingHeldLocked.
func TestIdleDemotionAfterWarmUpRearmsThePlatformMCPServerOnTheSessionNonce_spec_4_7_10(t *testing.T) {
	f := newSupervisedFixture(t, LifetimeKeep, LaunchPreconnectSDKWarm, nil)
	s := warmUpServer(t, f, RuntimeKindAgent)
	if err := s.PreConnect(startCtx(t, 5*time.Second)); err != nil {
		t.Fatalf("PreConnect: %v", err)
	}
	_, warmNonce := s.PodMCPArming()
	if warmNonce == "" {
		t.Fatal("the warm-up preparation armed no platform MCP server")
	}

	if _, err := s.DemoteSDK(startCtx(t, 5*time.Second), &adapterv1.DemoteSDKRequest{}); err != nil {
		t.Fatalf("DemoteSDK: %v", err)
	}
	if _, nonce := s.PodMCPArming(); nonce != "" {
		t.Fatalf("the idle demotion left the warm-up platform MCP server armed on nonce %q", nonce)
	}
	if c := dialInitialized(s.MCPSocket, warmNonce); c != nil {
		_ = c.conn.Close()
		t.Fatal("the platform MCP server still validates the warm-up nonce after the demotion ended the warm-up process")
	}

	claim, err := s.claimSessionSlot("alice", slotResolve{allowCreate: true}, false, false)
	if err != nil {
		t.Fatalf("claim alice: %v", err)
	}
	if !claim.startMCP || claim.platformBound {
		t.Fatalf("first claim after an idle demotion: startMCP=%v platformBound=%v, want the once-per-pod start with no server bound",
			claim.startMCP, claim.platformBound)
	}
	sessionNonce, err := s.writeSessionManifest(manifestInputs{})
	if err != nil {
		t.Fatalf("write the session manifest: %v", err)
	}
	if err := s.startClaimedPlatformMCP(claim, sessionNonce); err != nil {
		t.Fatalf("startClaimedPlatformMCP: %v", err)
	}
	if id, nonce := s.PodMCPArming(); id != "alice" || nonce != sessionNonce {
		t.Fatalf("PodMCPArming = (%q, %q), want (alice, the session manifest's nonce)", id, nonce)
	}
	c := dialInitialized(s.MCPSocket, sessionNonce)
	if c == nil {
		t.Fatal("the platform MCP server refused the nonce in the final manifest the step-7 process reads")
	}
	_ = c.conn.Close()
}

// spec: §4.7.10 (Runtime supervisor, Launch), §5.1
// diagnosis: a type: mcp runtime dials no platform MCP server, so a warm-up
// that binds one for it holds the pod-wide socket for nothing. Read the
// RuntimeKindMCP arm of PrepareWarmUpLaunch.
func TestWarmUpOfAnMCPRuntimeBindsNoPlatformMCPListener_spec_4_7_10(t *testing.T) {
	f := newSupervisedFixture(t, LifetimeKeep, LaunchService, nil)
	s := warmUpServer(t, f, RuntimeKindMCP)
	if err := f.rt.WarmUp(startCtx(t, 5*time.Second)); err != nil {
		t.Fatalf("WarmUp: %v", err)
	}
	if _, err := ReadManifest(s.ManifestDir); err != nil {
		t.Fatalf("no warm-up manifest: %v", err)
	}
	if _, nonce := s.PodMCPArming(); nonce != "" {
		t.Fatalf("a type: mcp warm-up armed the platform MCP surface on nonce %q", nonce)
	}
	if conn, err := net.Dial("unix", s.MCPSocket); err == nil {
		_ = conn.Close()
		t.Fatal("a type: mcp warm-up bound the platform MCP socket")
	}
}

// spec: §4.7.10 (Runtime supervisor, Launch; Runtime supervisor, Liveness)
// diagnosis: a warm-up whose platform MCP listener cannot bind launches a
// process that dies on its first dial; the bind failure must be the
// warm-up-failure cause before any launch. Read PrepareWarmUpLaunch's bind
// error path and WarmUp's preparation check.
func TestWarmUpWhoseMCPListenerCannotBindLaunchesNothing_spec_4_7_10(t *testing.T) {
	f := newSupervisedFixture(t, LifetimeKeep, LaunchService, nil)
	s := warmUpServer(t, f, RuntimeKindAgent)
	squat, err := net.Listen("unix", s.MCPSocket)
	if err != nil {
		t.Fatalf("occupy the platform MCP socket: %v", err)
	}
	defer squat.Close()
	if err := f.rt.WarmUp(startCtx(t, time.Second)); err == nil {
		t.Fatal("WarmUp succeeded with the platform MCP socket occupied")
	}
	if n := f.sup.count(superviseLaunch); n != 0 {
		t.Fatalf("launched %d times before the platform MCP listener was bound", n)
	}
	requireNotLive(t, f.rt, CauseWarmUpFailure)
}

// recordingForwarder records the sessions it forwards under.
type recordingForwarder struct {
	listed []string
	called []string
}

func (f *recordingForwarder) ListPlatformTools(_ context.Context, sessionID string) ([]mcp.Tool, error) {
	f.listed = append(f.listed, sessionID)
	return []mcp.Tool{{Name: "lenny/echo"}}, nil
}

func (f *recordingForwarder) CallPlatformTool(_ context.Context, sessionID, _ string, _ json.RawMessage) (json.RawMessage, error) {
	f.called = append(f.called, sessionID)
	return json.RawMessage(`{}`), nil
}

// spec: §15.4.3 (Authentication), §4.7.11 item 7, §9.1, §28.5.3
// diagnosis: the platform MCP server answered a tools/list from a process
// holding no session with an empty catalog, where the calling-session rule
// requires a refusal unless the shared runtime process has been given exactly
// one session. Read platformToolProvider.List and callingSession.
func TestPlatformToolListingWithNoSessionIsRefused_spec_15_4_3(t *testing.T) {
	s := New("test")
	fwd := &recordingForwarder{}
	p := &platformToolProvider{forwarder: fwd, server: s}
	ctx := context.Background()

	if tools, err := p.List(ctx); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("a listing with no session returned %d tools and err=%v, want FailedPrecondition", len(tools), err)
	}
	if len(fwd.listed) != 0 {
		t.Fatalf("a listing with no session forwarded under %v, want no forward", fwd.listed)
	}
	if _, err := p.Call(ctx, "lenny/echo", json.RawMessage(`{}`)); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("a call with no session returned %v, want FailedPrecondition", err)
	}

	s.mu.Lock()
	s.noteRuntimeStartedLocked("alice")
	s.mu.Unlock()
	if tools, err := p.List(ctx); err != nil || len(tools) != 1 || len(fwd.listed) != 1 || fwd.listed[0] != "alice" {
		t.Fatalf("a listing with one session: tools=%d err=%v forwarded under %v, want the catalog forwarded under alice",
			len(tools), err, fwd.listed)
	}

	s.mu.Lock()
	s.noteRuntimeStartedLocked("bob")
	s.mu.Unlock()
	if _, err := p.List(ctx); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("a listing with two sessions returned %v, want FailedPrecondition", err)
	}
	if _, err := p.Call(ctx, "lenny/echo", json.RawMessage(`{}`)); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("a call with two sessions returned %v, want FailedPrecondition", err)
	}
	if len(fwd.called) != 0 {
		t.Fatalf("calls forwarded under %v, want none", fwd.called)
	}
}
