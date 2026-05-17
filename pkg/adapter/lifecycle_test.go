// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"errors"
	"testing"

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
