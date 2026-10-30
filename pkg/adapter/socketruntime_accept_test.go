// SPDX-License-Identifier: MIT

package adapter_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// newAcceptTestRuntime binds a SocketRuntimeProcess that admits the test
// process's own UID and registers its pod-scope teardown.
func newAcceptTestRuntime(t *testing.T) *adapter.SocketRuntimeProcess {
	t.Helper()
	sp, err := newSocketRuntime(t, runtimeSocketAddr(t),
		adapter.SocketPeerAuth{ExpectedUID: uint32(os.Getuid())})
	if err != nil {
		t.Fatalf("NewSocketRuntimeProcess: %v", err)
	}
	t.Cleanup(func() { _ = sp.CloseListener() })
	return sp
}

// waitInstalled polls until the accept loop has installed a connection, which
// ServesNextSession reports, or fails the test.
func waitInstalled(t *testing.T, sp *adapter.SocketRuntimeProcess) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !sp.ServesNextSession() {
		if time.Now().After(deadline) {
			t.Fatal("the accept loop did not install the runtime's late connection")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// requireCarriesEnvelope writes one envelope for sessionID and checks that it
// reaches rt, the runtime side of the installed connection.
func requireCarriesEnvelope(t *testing.T, sp *adapter.SocketRuntimeProcess, rt net.Conn, sessionID string) {
	t.Helper()
	if err := sp.WriteEnvelope(sessionID, []byte(`{"type":"message","sessionId":"`+sessionID+`"}`)); err != nil {
		t.Fatalf("WriteEnvelope(%s): %v", sessionID, err)
	}
	_ = rt.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(rt).ReadString('\n')
	if err != nil || !strings.Contains(line, `"sessionId":"`+sessionID+`"`) {
		t.Fatalf("runtime read = (%q, %v), want the %s envelope", line, err, sessionID)
	}
}

// spec: 4.7.10 (Runtime process lifetime), 28.5.3 (CH-MSGSOCK)
func TestSocketRuntimeLateDialAfterTimedOutStartServesTheNextStart_spec_4_7_10(t *testing.T) {
	sp := newAcceptTestRuntime(t)
	sp.AcceptTimeout = 50 * time.Millisecond
	err := sp.Start(context.Background(), "s1")
	if err == nil || !strings.Contains(err.Error(), "did not connect") {
		t.Fatalf("Start with no runtime = %v, want the accept timeout", err)
	}

	// The runtime dials after the first Start gave up.
	rt := dialRuntimeSocket(t, sp.SocketPath())
	defer rt.Close()
	waitInstalled(t, sp)

	sp.AcceptTimeout = 5 * time.Second
	if err := sp.Start(context.Background(), "s2"); err != nil {
		t.Fatalf("Start after the late dial: %v", err)
	}
	requireCarriesEnvelope(t, sp, rt, "s2")
}

// spec: 4.7.10 (Runtime process lifetime), 28.5.3 (CH-MSGSOCK)
func TestSocketRuntimeLateDialAfterCancelledStartServesTheNextStart_spec_4_7_10(t *testing.T) {
	sp := newAcceptTestRuntime(t)
	sp.AcceptTimeout = 5 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sp.Start(ctx, "s1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start with a cancelled context = %v, want context.Canceled", err)
	}

	rt := dialRuntimeSocket(t, sp.SocketPath())
	defer rt.Close()
	if err := sp.Start(context.Background(), "s2"); err != nil {
		t.Fatalf("Start after the late dial: %v", err)
	}
	requireCarriesEnvelope(t, sp, rt, "s2")
}

// spec: 4.7.10 (Runtime process lifetime), 10.1.4 (Hold state timeout)
func TestSocketRuntimeTeardownClosesAnUnclaimedLateConnection_spec_4_7_10(t *testing.T) {
	sp := newAcceptTestRuntime(t)
	sp.AcceptTimeout = 50 * time.Millisecond
	if err := sp.Start(context.Background(), "s1"); err == nil {
		t.Fatal("Start with no runtime succeeded")
	}
	rt := dialRuntimeSocket(t, sp.SocketPath())
	defer rt.Close()
	waitInstalled(t, sp)

	// No Start claims the installed connection before the pod-scope teardown.
	if err := sp.CloseListener(); err != nil {
		t.Fatalf("CloseListener: %v", err)
	}
	requireRefused(t, rt, "unclaimed late connection at teardown")
	if err := sp.Start(context.Background(), "s2"); !errors.Is(err, adapter.ErrRuntimeConnectionEnded) {
		t.Fatalf("Start after teardown = %v, want ErrRuntimeConnectionEnded", err)
	}
}

// spec: 4.7.10 (Runtime process lifetime), 10.1.4 (Hold state timeout)
func TestSocketRuntimeTeardownReleasesAWaitingStart_spec_4_7_10(t *testing.T) {
	sp := newAcceptTestRuntime(t)
	sp.AcceptTimeout = 30 * time.Second
	started := startAsync(sp, "s1")
	// Give the accept loop time to block in Accept before the teardown.
	time.Sleep(20 * time.Millisecond)
	if err := sp.CloseListener(); err != nil {
		t.Fatalf("CloseListener: %v", err)
	}
	select {
	case err := <-started:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("waiting Start after teardown = %v, want net.ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a waiting Start did not return after the pod-scope teardown")
	}
}

// spec: 4.7.10 (Runtime process lifetime), 10.1.4 (Hold state timeout)
func TestSocketRuntimeConnectionAcceptedDuringTeardownIsClosed_spec_4_7_10(t *testing.T) {
	sp := newAcceptTestRuntime(t)
	sp.AcceptTimeout = 30 * time.Second
	// The peer check runs inside the accept loop's Accept. Running the
	// teardown there puts the teardown between the accept and the install.
	sp.SetPeerUIDLookupForTest(func(net.Conn) (uint32, error) {
		_ = sp.CloseListener()
		return uint32(os.Getuid()), nil
	})
	started := startAsync(sp, "s1")
	rt := dialRuntimeSocket(t, sp.SocketPath())
	defer rt.Close()

	select {
	case err := <-started:
		if !errors.Is(err, adapter.ErrRuntimeConnectionEnded) {
			t.Fatalf("Start whose connection arrived during teardown = %v, want ErrRuntimeConnectionEnded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after the teardown")
	}
	requireRefused(t, rt, "connection accepted during teardown")
	if sp.ServesNextSession() {
		t.Fatal("ServesNextSession reported true for a connection accepted after teardown")
	}
}
