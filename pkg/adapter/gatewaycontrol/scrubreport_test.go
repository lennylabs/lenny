// SPDX-License-Identifier: MIT

package gatewaycontrol_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// TestReportSessionScrubReleased: a released per-slot cleanup is reported
// with the pod, session, slot, and a RELEASED outcome on the wire.
// spec: 4.7 (Adapter → Gateway RPCs), 5.2 (scrub model)
func TestReportSessionScrubReleased_spec_5_2(t *testing.T) {
	stub := &stubGatewayControl{}
	client := dialStub(t, stub)

	err := client.ReportSessionScrub(context.Background(), "pod-7", "sess-1", "slot-3", gatewaycontrol.SessionScrubReleased)
	if err != nil {
		t.Fatalf("ReportSessionScrub: %v", err)
	}
	got := stub.gotSessionScrubReq
	if got.GetPodId() != "pod-7" {
		t.Errorf("pod id = %q, want pod-7", got.GetPodId())
	}
	if got.GetSessionId().GetValue() != "sess-1" {
		t.Errorf("session id = %q, want sess-1", got.GetSessionId().GetValue())
	}
	if got.GetSlotId().GetValue() != "slot-3" {
		t.Errorf("slot id = %q, want slot-3", got.GetSlotId().GetValue())
	}
	if got.GetOutcome() != adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_RELEASED {
		t.Errorf("outcome = %v, want RELEASED", got.GetOutcome())
	}
}

// TestReportSessionScrubLeakedOmitsSlotWhenEmpty: a leaked cleanup on a
// single-session pod (maxConcurrentSessions: 1) carries no slot id, so the
// optional SlotId sub-message is absent on the wire.
// spec: 5.2 (leaked slot semantics)
func TestReportSessionScrubLeakedOmitsSlotWhenEmpty_spec_5_2(t *testing.T) {
	stub := &stubGatewayControl{}
	client := dialStub(t, stub)

	if err := client.ReportSessionScrub(context.Background(), "pod-7", "sess-1", "", gatewaycontrol.SessionScrubLeaked); err != nil {
		t.Fatalf("ReportSessionScrub: %v", err)
	}
	got := stub.gotSessionScrubReq
	if got.GetSlotId() != nil {
		t.Errorf("slot id = %+v, want nil for a single-session pod", got.GetSlotId())
	}
	if got.GetOutcome() != adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_LEAKED {
		t.Errorf("outcome = %v, want LEAKED", got.GetOutcome())
	}
}

// TestReportSessionScrubTransportError: a gRPC error from the gateway
// surfaces as a wrapped error rather than being swallowed.
// spec: 4.7 (Adapter → Gateway RPCs)
func TestReportSessionScrubTransportError_spec_4_7(t *testing.T) {
	stub := &stubGatewayControl{sessionScrubErr: status.Error(codes.Unavailable, "gateway down")}
	client := dialStub(t, stub)

	err := client.ReportSessionScrub(context.Background(), "pod-7", "sess-1", "", gatewaycontrol.SessionScrubReleased)
	if err == nil {
		t.Fatal("ReportSessionScrub should return the gateway error")
	}
	if status.Code(err) != codes.Unavailable {
		t.Errorf("error code = %v, want Unavailable", status.Code(err))
	}
}

// TestReportPodScrubSucceeded: a successful whole-pod scrub is reported
// with the pod id and a SUCCEEDED outcome and no session (occupancy is
// zero at the recycle boundary).
// spec: 4.7 (Adapter → Gateway RPCs), 5.2 (scrub model), 3.4 (recycle disposition)
func TestReportPodScrubSucceeded_spec_3_4(t *testing.T) {
	stub := &stubGatewayControl{}
	client := dialStub(t, stub)

	if err := client.ReportPodScrub(context.Background(), "pod-7", gatewaycontrol.PodScrubSucceeded, ""); err != nil {
		t.Fatalf("ReportPodScrub: %v", err)
	}
	got := stub.gotPodScrubReq
	if got.GetPodId() != "pod-7" {
		t.Errorf("pod id = %q, want pod-7", got.GetPodId())
	}
	if got.GetOutcome() != adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_SUCCEEDED {
		t.Errorf("outcome = %v, want SUCCEEDED", got.GetOutcome())
	}
	if got.GetDetail() != "" {
		t.Errorf("detail = %q, want empty on success", got.GetDetail())
	}
}

// TestReportPodScrubFailedCarriesDetail: a failed whole-pod scrub carries
// the FAILED outcome and the adapter-side failure detail for the audit
// trail.
// spec: 5.2 (scrub model), 3.4 (recycle disposition)
func TestReportPodScrubFailedCarriesDetail_spec_3_4(t *testing.T) {
	stub := &stubGatewayControl{}
	client := dialStub(t, stub)

	if err := client.ReportPodScrub(context.Background(), "pod-7", gatewaycontrol.PodScrubFailed, "shred timed out on /tmp"); err != nil {
		t.Fatalf("ReportPodScrub: %v", err)
	}
	got := stub.gotPodScrubReq
	if got.GetOutcome() != adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_FAILED {
		t.Errorf("outcome = %v, want FAILED", got.GetOutcome())
	}
	if got.GetDetail() != "shred timed out on /tmp" {
		t.Errorf("detail = %q, want the failure description", got.GetDetail())
	}
}

// TestReportPodScrubTransportError: a gRPC error from the gateway during a
// pod-scrub report surfaces as a wrapped error.
// spec: 4.7 (Adapter → Gateway RPCs)
func TestReportPodScrubTransportError_spec_4_7(t *testing.T) {
	stub := &stubGatewayControl{podScrubErr: status.Error(codes.Internal, "claim patch failed")}
	client := dialStub(t, stub)

	err := client.ReportPodScrub(context.Background(), "pod-7", gatewaycontrol.PodScrubSucceeded, "")
	if err == nil {
		t.Fatal("ReportPodScrub should return the gateway error")
	}
	if status.Code(err) != codes.Internal {
		t.Errorf("error code = %v, want Internal", status.Code(err))
	}
}

// TestScrubOutcomeUnspecifiedMapsToProtoZero: an unspecified typed outcome
// maps to the proto unspecified zero value, which the gateway rejects
// fail-closed rather than treating as a success.
// spec: 5.2 (scrub model)
func TestScrubOutcomeUnspecifiedMapsToProtoZero_spec_5_2(t *testing.T) {
	stub := &stubGatewayControl{}
	client := dialStub(t, stub)

	if err := client.ReportSessionScrub(context.Background(), "pod-7", "sess-1", "", gatewaycontrol.SessionScrubUnspecified); err != nil {
		t.Fatalf("ReportSessionScrub: %v", err)
	}
	if got := stub.gotSessionScrubReq.GetOutcome(); got != adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_UNSPECIFIED {
		t.Errorf("session outcome = %v, want UNSPECIFIED", got)
	}

	if err := client.ReportPodScrub(context.Background(), "pod-7", gatewaycontrol.PodScrubUnspecified, ""); err != nil {
		t.Fatalf("ReportPodScrub: %v", err)
	}
	if got := stub.gotPodScrubReq.GetOutcome(); got != adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_UNSPECIFIED {
		t.Errorf("pod outcome = %v, want UNSPECIFIED", got)
	}
}
