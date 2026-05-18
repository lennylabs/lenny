// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// startedSession builds an adapter Server with a session already
// assigned, returning the server and its fake runtime.
func startedSession(t *testing.T, sessionID string) (*adapter.Server, *fakeRuntime) {
	t.Helper()
	s, rt, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq(sessionID)); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return s, rt
}

func TestInterruptCleanSignalsTheRuntime(t *testing.T) {
	s, rt := startedSession(t, "sess-1")

	resp, err := s.Interrupt(context.Background(), &adapterv1.InterruptRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Mode:      adapterv1.InterruptRequest_MODE_CLEAN,
	})
	if err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if !resp.GetAcknowledged() {
		t.Error("Interrupt did not acknowledge")
	}
	if len(rt.interrupts) != 1 || rt.interrupts[0] {
		t.Errorf("runtime interrupts = %v, want one clean interrupt", rt.interrupts)
	}
}

func TestInterruptHardSignalsTheRuntime(t *testing.T) {
	s, rt := startedSession(t, "sess-1")

	if _, err := s.Interrupt(context.Background(), &adapterv1.InterruptRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Mode:      adapterv1.InterruptRequest_MODE_HARD,
	}); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if len(rt.interrupts) != 1 || !rt.interrupts[0] {
		t.Errorf("runtime interrupts = %v, want one hard interrupt", rt.interrupts)
	}
}

func TestInterruptRequiresASessionID(t *testing.T) {
	s, _ := startedSession(t, "sess-1")

	_, err := s.Interrupt(context.Background(), &adapterv1.InterruptRequest{
		Mode: adapterv1.InterruptRequest_MODE_CLEAN,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("error code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestInterruptRequiresAMode(t *testing.T) {
	s, _ := startedSession(t, "sess-1")

	_, err := s.Interrupt(context.Background(), &adapterv1.InterruptRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("error code = %v, want InvalidArgument for an unspecified mode", status.Code(err))
	}
}

func TestInterruptRejectsAnUnassignedSession(t *testing.T) {
	s, _, _ := sessionServer(t) // no StartSession ran.

	_, err := s.Interrupt(context.Background(), &adapterv1.InterruptRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-absent"},
		Mode:      adapterv1.InterruptRequest_MODE_CLEAN,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("error code = %v, want FailedPrecondition for a pod with no session", status.Code(err))
	}
}

func TestDemoteSDKReturnsUnimplementedForAPodWarmAdapter(t *testing.T) {
	s := adapter.New("test")

	_, err := s.DemoteSDK(context.Background(), &adapterv1.DemoteSDKRequest{
		Reason: "incoming WorkspacePlan matched an sdkWarmBlockingPaths pattern",
	})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("error code = %v, want Unimplemented for a pod-warm adapter", status.Code(err))
	}
}

func TestInterruptReportsARuntimeFailure(t *testing.T) {
	s, rt := startedSession(t, "sess-1")
	rt.interruptErr = errors.New("signal delivery failed")

	_, err := s.Interrupt(context.Background(), &adapterv1.InterruptRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Mode:      adapterv1.InterruptRequest_MODE_HARD,
	})
	if status.Code(err) != codes.Internal {
		t.Errorf("error code = %v, want Internal when the runtime signal fails", status.Code(err))
	}
}

// startLifecycle creates a lifecycle channel on a temporary socket and
// runs it; Run is torn down on test cleanup.
func startLifecycle(t *testing.T) *adapter.LifecycleChannel {
	t.Helper()
	dir, err := os.MkdirTemp("", "lc-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	lc, err := adapter.NewLifecycleChannel(filepath.Join(dir, "lifecycle.sock"))
	if err != nil {
		t.Fatalf("NewLifecycleChannel: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runErr := make(chan error, 1)
	go func() { runErr <- lc.Run(ctx) }()
	t.Cleanup(func() {
		_ = lc.Close()
		<-runErr
	})
	return lc
}

// lifecycleRuntime is a scripted fake runtime for the §4.7 lifecycle
// channel, exercising the adapter's Full-level RPC paths.
type lifecycleRuntime struct {
	t    *testing.T
	conn net.Conn
	dec  *json.Decoder
	enc  *json.Encoder
}

// dialLifecycle dials lc as a runtime, completes the handshake, and
// blocks until the adapter has recorded the runtime's capabilities.
func dialLifecycle(t *testing.T, lc *adapter.LifecycleChannel) *lifecycleRuntime {
	t.Helper()
	conn, err := net.Dial("unix", lc.SocketPath())
	if err != nil {
		t.Fatalf("dial lifecycle socket: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	lr := &lifecycleRuntime{t: t, conn: conn, dec: json.NewDecoder(conn), enc: json.NewEncoder(conn)}

	caps := lr.recv()
	if caps["type"] != "lifecycle_capabilities" {
		t.Fatalf("first adapter frame = %v, want lifecycle_capabilities", caps["type"])
	}
	lr.send(map[string]any{"type": "lifecycle_support", "capabilities": caps["capabilities"]})

	deadline := time.Now().Add(2 * time.Second)
	for !lc.Supports("interrupt") {
		if time.Now().After(deadline) {
			t.Fatal("adapter did not complete the lifecycle handshake")
		}
		time.Sleep(time.Millisecond)
	}
	return lr
}

func (lr *lifecycleRuntime) recv() map[string]any {
	lr.t.Helper()
	_ = lr.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var m map[string]any
	if err := lr.dec.Decode(&m); err != nil {
		lr.t.Fatalf("lifecycle runtime recv: %v", err)
	}
	return m
}

func (lr *lifecycleRuntime) send(m map[string]any) {
	lr.t.Helper()
	if err := lr.enc.Encode(m); err != nil {
		lr.t.Fatalf("lifecycle runtime send: %v", err)
	}
}

func TestInterruptCleanUsesLifecycleChannel(t *testing.T) {
	s, rt := startedSession(t, "sess-1")
	lc := startLifecycle(t)
	s.Lifecycle = lc
	lr := dialLifecycle(t, lc)

	respc := make(chan *adapterv1.InterruptResponse, 1)
	go func() {
		resp, err := s.Interrupt(context.Background(), &adapterv1.InterruptRequest{
			SessionId:  &adapterv1.SessionId{Value: "sess-1"},
			Mode:       adapterv1.InterruptRequest_MODE_CLEAN,
			DeadlineMs: 2000,
		})
		if err != nil {
			t.Errorf("Interrupt: %v", err)
			respc <- nil
			return
		}
		respc <- resp
	}()

	req := lr.recv()
	if req["type"] != "interrupt_request" {
		t.Fatalf("runtime saw %v, want interrupt_request", req["type"])
	}
	lr.send(map[string]any{"type": "interrupt_acknowledged", "interruptId": req["interruptId"]})

	resp := <-respc
	if resp == nil {
		return
	}
	if resp.GetStatus() != adapterv1.InterruptResponse_STATUS_ACKNOWLEDGED {
		t.Errorf("status = %v, want STATUS_ACKNOWLEDGED", resp.GetStatus())
	}
	// The clean Full-level interrupt goes over the channel, not the signal path.
	if len(rt.interrupts) != 0 {
		t.Errorf("runtime received %d signal interrupts, want 0 (lifecycle path)", len(rt.interrupts))
	}
}

func TestInterruptCleanTimesOutOnLifecycleChannel(t *testing.T) {
	s, _ := startedSession(t, "sess-1")
	lc := startLifecycle(t)
	s.Lifecycle = lc
	// The runtime connects and handshakes but never acknowledges.
	dialLifecycle(t, lc)

	resp, err := s.Interrupt(context.Background(), &adapterv1.InterruptRequest{
		SessionId:  &adapterv1.SessionId{Value: "sess-1"},
		Mode:       adapterv1.InterruptRequest_MODE_CLEAN,
		DeadlineMs: 150,
	})
	if err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if resp.GetStatus() != adapterv1.InterruptResponse_STATUS_INTERRUPT_TIMEOUT {
		t.Errorf("status = %v, want STATUS_INTERRUPT_TIMEOUT", resp.GetStatus())
	}
}
