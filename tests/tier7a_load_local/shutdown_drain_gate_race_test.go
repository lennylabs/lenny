// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the drain decision the
// adapter's Shutdown handler takes.
//
// The handler deregisters the ending session and decides whether to send
// the CH-RUNTIMEOPS drain signal inside one critical section, on the
// outcome of that section rather than on a registry read taken before it.
// The signal is pod-global and names no session, so it goes out exactly
// once, when the deregistration leaves the pod holding no bound entry, and
// it goes out before the last session's runtime is closed: the last close
// tears the shared runtime down and a terminate frame sent afterwards
// reaches a dead runtime, where drainViaLifecycle swallows the error and
// the regression is silent.
//
// Two properties are driven here that a sequential teardown pair cannot
// reach. Two co-tenants ending at once must produce one signal on either
// interleaving, where a check-then-act evaluation produces none. A session
// ending while another session's workspace preparation and start are in
// flight must produce the signal as soon as no bound entry remains, where
// a count over all registry entries withholds it behind the incoming
// session's registered-but-unbound entry.
//
// Both cases carry a stress budget:
//
//	lenny-test stress --test TestConcurrentShutdownsSendOneDrainSignal_spec_6_4 --runs 50 --pkg ./tests/tier7a_load_local/... --tag load_local
//	lenny-test stress --test TestShutdownDrainRacesAnIncomingSession_spec_6_4 --runs 50 --pkg ./tests/tier7a_load_local/... --tag load_local
//
// spec: §6.4 (every session is bound to a slot on every pod), §5.2 (slot
// registry, release), §15.4.2 (CH-RUNTIMEOPS drain before the hard close).
package tier7a_load_local_test

import (
	"bufio"
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

// drainPeer stands in for the runtime end of CH-RUNTIMEOPS. It completes
// the handshake the adapter opens the connection with and records every
// frame the adapter writes afterwards, so a case can count the terminate
// frames the drain decision produced.
type drainPeer struct {
	frames chan string

	mu      sync.Mutex
	byType  map[string]int
	readErr error
}

// startDrainPeer wires a live RuntimeOps to a connected peer and returns
// both. The peer's reader runs until the connection ends.
func startDrainPeer(t *testing.T) (*adapter.RuntimeOps, *drainPeer) {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-ops-*")
	if err != nil {
		t.Fatalf("temp lifecycle socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	lc, err := adapter.NewRuntimeOps(filepath.Join(dir, "o.sock"))
	if err != nil {
		t.Fatalf("NewRuntimeOps: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runErr := make(chan error, 1)
	go func() { runErr <- lc.Run(ctx) }()
	t.Cleanup(func() {
		_ = lc.Close()
		<-runErr
	})

	conn, err := net.Dial("unix", lc.SocketPath())
	if err != nil {
		t.Fatalf("dial CH-RUNTIMEOPS socket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	p := &drainPeer{frames: make(chan string, 32), byType: map[string]int{}}
	r := bufio.NewReader(conn)
	// The adapter opens with lifecycle_capabilities and waits for the
	// runtime's lifecycle_support before any signal frame flows.
	if _, err := p.readFrame(r); err != nil {
		t.Fatalf("read lifecycle capabilities: %v", err)
	}
	if err := json.NewEncoder(conn).Encode(map[string]any{
		"type":         "lifecycle_support",
		"capabilities": []string{"checkpoint", "interrupt"},
	}); err != nil {
		t.Fatalf("send lifecycle support: %v", err)
	}
	go func() {
		for {
			typ, err := p.readFrame(r)
			if err != nil {
				p.mu.Lock()
				p.readErr = err
				p.mu.Unlock()
				return
			}
			p.mu.Lock()
			p.byType[typ]++
			p.mu.Unlock()
			select {
			case p.frames <- typ:
			default:
			}
		}
	}()
	return lc, p
}

// readFrame reads one JSONL frame and reports its type.
func (p *drainPeer) readFrame(r *bufio.Reader) (string, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return "", err
	}
	var f struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &f); err != nil {
		return "", err
	}
	return f.Type, nil
}

// count reports how many frames of the given type the runtime has seen.
func (p *drainPeer) count(typ string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.byType[typ]
}

// awaitTerminate blocks until a terminate frame arrives or the wait
// elapses, and reports whether one arrived.
func (p *drainPeer) awaitTerminate(wait time.Duration) bool {
	deadline := time.After(wait)
	for {
		if p.count("terminate") > 0 {
			return true
		}
		select {
		case <-p.frames:
		case <-deadline:
			return p.count("terminate") > 0
		}
	}
}

// drainPod returns an adapter whose CH-RUNTIMEOPS is wired to a live peer
// and whose runtime parks on the gates the case arms.
func drainPod(t *testing.T, rt adapter.RuntimeProcess) (*adapter.Server, *drainPeer) {
	t.Helper()
	lc, peer := startDrainPeer(t)
	base := t.TempDir()
	s := adapter.New("test")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.ManifestDir = t.TempDir()
	s.Runtime = rt
	s.Lifecycle = lc
	return s, peer
}

// startDrainSession drives a full StartSession and fails the case when it
// does not return cleanly.
func startDrainSession(t *testing.T, s *adapter.Server, sessionID string) {
	t.Helper()
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID}, Runtime: "echo",
	}); err != nil {
		t.Fatalf("start %s: %v", sessionID, err)
	}
}

// spec: 6.4 (every session is bound to a slot on every pod), 5.2 (slot
// registry release), 15.4.2 (CH-RUNTIMEOPS drain precedes the hard close)
// diagnosis: two co-tenants ending at once produced the wrong number of
// drain signals, or produced one only after the shared runtime was already
// closed. Zero signals means the drain decision was taken on a registry
// read that precedes the deregistration, so each call observed the other
// and both declined; the shared runtime then never receives the §15.4.2
// DRAINING state and is killed without a graceful drain. Two signals means
// the decision does not read the outcome of its own locked step. A signal
// that arrives only after both closes returned means the send was ordered
// after Runtime.Close, where it reaches a dead runtime and the error is
// swallowed.
func TestConcurrentShutdownsSendOneDrainSignal_spec_6_4(t *testing.T) {
	rt := newGatedRuntime()
	s, peer := drainPod(t, rt)

	startDrainSession(t, s, "alice")
	startDrainSession(t, s, "bob")

	// Park both closes so neither teardown can complete before the other's
	// critical section has run. Whichever call deregisters second is the
	// one that must find no bound entry left and send the drain.
	aliceClosing, releaseAlice := rt.gate("alice", true)
	bobClosing, releaseBob := rt.gate("bob", true)

	done := make(chan struct{}, 2)
	for _, sessionID := range []string{"alice", "bob"} {
		go func() {
			defer func() { done <- struct{}{} }()
			_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
				SessionId: &adapterv1.SessionId{Value: sessionID},
				Reason:    "session_complete",
			})
		}()
	}
	waitClosed(t, aliceClosing, "alice's runtime close to begin")
	waitClosed(t, bobClosing, "bob's runtime close to begin")

	// Both teardowns are parked inside Runtime.Close, so a terminate frame
	// seen here was written before the last session's runtime was closed.
	if !peer.awaitTerminate(30 * time.Second) {
		t.Fatal("no CH-RUNTIMEOPS drain signal reached the runtime before the last session's close; " +
			"the drain decision was taken on a read that precedes the deregistration")
	}

	close(releaseAlice)
	close(releaseBob)
	for range 2 {
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for the concurrent Shutdown calls to return")
		}
	}
	// The signal is pod-global and names no session: the co-tenant's
	// teardown must not send a second one.
	if got := peer.count("terminate"); got != 1 {
		t.Errorf("CH-RUNTIMEOPS terminate frames = %d, want 1 across the two concurrent teardowns", got)
	}
}

// spec: 6.4, 5.2, 15.4.2
// diagnosis: a session ended while another session's workspace was being
// prepared and the drain was withheld, or the pod's shared runtime
// connection did not take the disposition the teardown order implies. A
// missing drain means the decision counts every registry entry rather than
// the bound ones, so an incoming session's registered-but-unbound entry
// suppresses the ending session's §15.4.2 drain. A surviving connection on
// the first leg, or a torn-down one on the second, means the shared
// runtime's active-set accounting no longer tracks which sessions the
// close is the last of.
func TestShutdownDrainRacesAnIncomingSession_spec_6_4(t *testing.T) {
	t.Run("teardown_lands_before_the_incoming_start", func(t *testing.T) {
		s, peer, dial := socketDrainPod(t)
		// The pod's one runtime process connects before the first start,
		// which is the accept that start waits on.
		conn := dial()
		defer conn.Close()
		startDrainSession(t, s, "alice")

		// The incoming session's workspace preparation registers its slot
		// without binding it. A drain withheld on that entry is the defect.
		if _, err := s.FinalizeWorkspace(context.Background(), &adapterv1.FinalizeWorkspaceRequest{
			SessionId: &adapterv1.SessionId{Value: "bob"},
		}); err != nil {
			t.Fatalf("prepare bob's workspace: %v", err)
		}
		if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "alice"},
		}); err != nil {
			t.Fatalf("shut alice down: %v", err)
		}
		if got := peer.count("terminate"); got != 1 {
			t.Errorf("CH-RUNTIMEOPS terminate frames = %d, want 1; the incoming session's "+
				"registered-but-unbound entry must not withhold the drain", got)
		}
		// alice was the shared runtime process's only member, so her close
		// took the connection and the process-scoped listener with it and
		// nothing re-binds them. The incoming session's start fails in
		// accept rather than re-establishing the connection.
		if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
			SessionId: &adapterv1.SessionId{Value: "bob"}, Runtime: "echo",
		}); err == nil {
			t.Error("the incoming session's start succeeded after the shared runtime connection was torn down")
		}
	})

	t.Run("incoming_start_lands_before_the_teardown", func(t *testing.T) {
		s, peer, dial := socketDrainPod(t)
		// The pod's one runtime process connects before the first start,
		// which is the accept that start waits on.
		conn := dial()
		defer conn.Close()
		startDrainSession(t, s, "alice")

		startDrainSession(t, s, "bob")
		if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "alice"},
		}); err != nil {
			t.Fatalf("shut alice down: %v", err)
		}
		// bob is still bound, so the pod-global drain is withheld and the
		// shared connection survives alice's close.
		if got := peer.count("terminate"); got != 0 {
			t.Errorf("CH-RUNTIMEOPS terminate frames = %d, want 0 while a co-tenant is still bound", got)
		}
		if _, err := s.SendMessage(context.Background(), &adapterv1.SendMessageRequest{
			SessionId:    &adapterv1.SessionId{Value: "bob"},
			EnvelopeJson: []byte(`{"type":"message"}`),
		}); err != nil {
			t.Errorf("send on the surviving session's stream: %v", err)
		}
	})

	t.Run("unsequenced", func(t *testing.T) {
		s, peer, dial := socketDrainPod(t)
		// The pod's one runtime process connects before the first start,
		// which is the accept that start waits on.
		conn := dial()
		defer conn.Close()
		startDrainSession(t, s, "alice")

		// No ordering between the teardown and the incoming session's
		// preparation and start. Whichever way the scheduler resolves it,
		// the pod-global signal is sent at most once and is sent whenever
		// the teardown left no bound entry behind.
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
				SessionId: &adapterv1.SessionId{Value: "alice"},
			})
		}()
		go func() {
			defer wg.Done()
			_, _ = s.FinalizeWorkspace(context.Background(), &adapterv1.FinalizeWorkspaceRequest{
				SessionId: &adapterv1.SessionId{Value: "bob"},
			})
			_, _ = s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
				SessionId: &adapterv1.SessionId{Value: "bob"}, Runtime: "echo",
			})
		}()
		wg.Wait()
		if got := peer.count("terminate"); got > 1 {
			t.Errorf("CH-RUNTIMEOPS terminate frames = %d, want at most 1", got)
		}
	})
}

// socketDrainPod returns an adapter whose runtime is a real
// SocketRuntimeProcess, together with its CH-RUNTIMEOPS peer and the dial
// func that stands in for the pod's one runtime process connecting. The
// shared connection is what makes the incoming session's disposition
// observable: one process serves every slot, so the last member's close
// takes the connection and the listener down.
func socketDrainPod(t *testing.T) (*adapter.Server, *drainPeer, func() net.Conn) {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-rt-*")
	if err != nil {
		t.Fatalf("temp runtime socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "r.sock")
	rt, err := adapter.NewSocketRuntimeProcess(socket)
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	// A start that has to accept a connection nobody will make is the
	// failure the first leg asserts; bound it well under the case timeout.
	rt.AcceptTimeout = 5 * time.Second
	s, peer := drainPod(t, rt)
	return s, peer, func() net.Conn {
		conn, derr := net.Dial("unix", socket)
		if derr != nil {
			t.Fatalf("dial runtime socket: %v", derr)
		}
		return conn
	}
}
