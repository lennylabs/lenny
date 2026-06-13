//go:build contract

// SPDX-License-Identifier: MIT

// Package gatewaycontrol_scrub_test is the Tier 3 contract suite for the
// §4.7 GatewayControl scrub-report RPCs (ReportSessionScrub and
// ReportPodScrub). It pins the wire messages two ways: a proto
// marshal/unmarshal round-trip that asserts every field survives the
// binary encoding, and an end-to-end gRPC call through the real client
// and server stubs that asserts the request the adapter sends is the
// request the gateway receives. spec: §4.7; §5.2; §3.4.
package gatewaycontrol_scrub_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// scrubServer is a minimal GatewayControl server that captures the
// scrub-report requests it receives so the contract test can compare the
// received message against the sent one.
type scrubServer struct {
	adapterv1.UnimplementedGatewayControlServer

	gotSession *adapterv1.ReportSessionScrubRequest
	gotPod     *adapterv1.ReportPodScrubRequest
}

func (s *scrubServer) ReportSessionScrub(_ context.Context, req *adapterv1.ReportSessionScrubRequest) (*adapterv1.ReportSessionScrubResponse, error) {
	s.gotSession = req
	return &adapterv1.ReportSessionScrubResponse{}, nil
}

func (s *scrubServer) ReportPodScrub(_ context.Context, req *adapterv1.ReportPodScrubRequest) (*adapterv1.ReportPodScrubResponse, error) {
	s.gotPod = req
	return &adapterv1.ReportPodScrubResponse{}, nil
}

func dialScrub(t *testing.T, srv *scrubServer) adapterv1.GatewayControlClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterGatewayControlServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return adapterv1.NewGatewayControlClient(conn)
}

// TestReportSessionScrubRequestRoundTrip pins that every field of the
// ReportSessionScrubRequest (pod id, session id, slot id, outcome)
// survives a proto binary marshal/unmarshal, so a sender and a receiver
// built from the same schema agree on the wire.
// spec: 4.7 (Adapter → Gateway RPCs), 5.2 (scrub model)
//
// diagnosis: a failure means a field of ReportSessionScrubRequest was
// renumbered, retyped, or dropped in schemas/lenny-adapter.proto without
// the generated Go being regenerated, so the binary encoding no longer
// round-trips and the adapter's per-slot scrub report would be silently
// truncated or misread on the gateway side.
func TestReportSessionScrubRequestRoundTrip_spec_5_2(t *testing.T) {
	in := &adapterv1.ReportSessionScrubRequest{
		PodId:     "pod-7",
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		SlotId:    &adapterv1.SlotId{Value: "slot-3"},
		Outcome:   adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_LEAKED,
	}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out adapterv1.ReportSessionScrubRequest
	if err := proto.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !proto.Equal(in, &out) {
		t.Errorf("round-trip mismatch:\n got %v\nwant %v", &out, in)
	}
}

// TestReportPodScrubRequestRoundTrip pins that the ReportPodScrubRequest
// fields (pod id, outcome, detail) survive a proto binary round-trip.
// spec: 4.7 (Adapter → Gateway RPCs), 3.4 (recycle disposition)
//
// diagnosis: a failure means a field of ReportPodScrubRequest was
// renumbered, retyped, or dropped in schemas/lenny-adapter.proto without
// the generated Go being regenerated, so the whole-pod scrub outcome no
// longer round-trips and the occupancy-zero recycle disposition cannot be
// computed from the report.
func TestReportPodScrubRequestRoundTrip_spec_3_4(t *testing.T) {
	in := &adapterv1.ReportPodScrubRequest{
		PodId:   "pod-7",
		Outcome: adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_FAILED,
		Detail:  "shred timed out on /tmp",
	}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out adapterv1.ReportPodScrubRequest
	if err := proto.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !proto.Equal(in, &out) {
		t.Errorf("round-trip mismatch:\n got %v\nwant %v", &out, in)
	}
}

// TestReportSessionScrubGRPCContract drives the ReportSessionScrub RPC end
// to end through the real generated client and a server stub, asserting
// the message the server receives equals the message the client sent.
// spec: 4.7 (Adapter → Gateway RPCs), 5.2 (scrub model)
//
// diagnosis: a failure means the ReportSessionScrub wire contract drifted
// between the generated client and server — a field was renumbered,
// retyped, or dropped in schemas/lenny-adapter.proto without regenerating
// both ends, so the adapter's per-slot cleanup report no longer reaches
// the gateway intact.
func TestReportSessionScrubGRPCContract_spec_5_2(t *testing.T) {
	srv := &scrubServer{}
	client := dialScrub(t, srv)

	sent := &adapterv1.ReportSessionScrubRequest{
		PodId:     "pod-7",
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Outcome:   adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_RELEASED,
	}
	if _, err := client.ReportSessionScrub(context.Background(), sent); err != nil {
		t.Fatalf("ReportSessionScrub: %v", err)
	}
	if !proto.Equal(sent, srv.gotSession) {
		t.Errorf("server received %v, want %v", srv.gotSession, sent)
	}
}

// TestReportPodScrubGRPCContract drives the ReportPodScrub RPC end to end
// and asserts the received message equals the sent message.
// spec: 4.7 (Adapter → Gateway RPCs), 3.4 (recycle disposition)
//
// diagnosis: a failure means the ReportPodScrub wire contract drifted
// between the generated client and server, so the adapter's whole-pod
// scrub outcome at the occupancy-zero recycle boundary no longer reaches
// the gateway intact and the recycle disposition cannot be computed.
func TestReportPodScrubGRPCContract_spec_3_4(t *testing.T) {
	srv := &scrubServer{}
	client := dialScrub(t, srv)

	sent := &adapterv1.ReportPodScrubRequest{
		PodId:   "pod-7",
		Outcome: adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_SUCCEEDED,
	}
	if _, err := client.ReportPodScrub(context.Background(), sent); err != nil {
		t.Fatalf("ReportPodScrub: %v", err)
	}
	if !proto.Equal(sent, srv.gotPod) {
		t.Errorf("server received %v, want %v", srv.gotPod, sent)
	}
}

// TestScrubOutcomeEnumWireValues pins the integer wire values of the
// scrub-outcome enums. A renumbering silently reinterprets a RELEASED
// report as LEAKED (or a SUCCEEDED scrub as FAILED) across a mixed-version
// client and server, so the values are part of the contract.
// spec: 5.2 (scrub model)
//
// diagnosis: a failure means a scrub-outcome enum value was renumbered in
// schemas/lenny-adapter.proto, so a mixed-version client and server would
// reinterpret a RELEASED report as LEAKED (or a SUCCEEDED scrub as FAILED)
// and the gateway's leak accounting and recycle disposition would be wrong.
func TestScrubOutcomeEnumWireValues_spec_5_2(t *testing.T) {
	if got := int32(adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_UNSPECIFIED); got != 0 {
		t.Errorf("SESSION_SCRUB_OUTCOME_UNSPECIFIED = %d, want 0", got)
	}
	if got := int32(adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_RELEASED); got != 1 {
		t.Errorf("SESSION_SCRUB_OUTCOME_RELEASED = %d, want 1", got)
	}
	if got := int32(adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_LEAKED); got != 2 {
		t.Errorf("SESSION_SCRUB_OUTCOME_LEAKED = %d, want 2", got)
	}
	if got := int32(adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_UNSPECIFIED); got != 0 {
		t.Errorf("POD_SCRUB_OUTCOME_UNSPECIFIED = %d, want 0", got)
	}
	if got := int32(adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_SUCCEEDED); got != 1 {
		t.Errorf("POD_SCRUB_OUTCOME_SUCCEEDED = %d, want 1", got)
	}
	if got := int32(adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_FAILED); got != 2 {
		t.Errorf("POD_SCRUB_OUTCOME_FAILED = %d, want 2", got)
	}
}
