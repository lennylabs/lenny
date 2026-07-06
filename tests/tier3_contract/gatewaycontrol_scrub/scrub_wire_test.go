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
	"errors"
	"net"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// scrubServer is a minimal GatewayControl server that captures the
// scrub-report requests it receives so the contract test can compare the
// received message against the sent one.
type scrubServer struct {
	adapterv1.UnimplementedGatewayControlServer

	gotSession     *adapterv1.ReportSessionScrubRequest
	sessionReports []*adapterv1.ReportSessionScrubRequest
	gotPod         *adapterv1.ReportPodScrubRequest
}

func (s *scrubServer) ReportSessionScrub(_ context.Context, req *adapterv1.ReportSessionScrubRequest) (*adapterv1.ReportSessionScrubResponse, error) {
	s.gotSession = req
	s.sessionReports = append(s.sessionReports, req)
	return &adapterv1.ReportSessionScrubResponse{}, nil
}

func (s *scrubServer) ReportPodScrub(_ context.Context, req *adapterv1.ReportPodScrubRequest) (*adapterv1.ReportPodScrubResponse, error) {
	s.gotPod = req
	return &adapterv1.ReportPodScrubResponse{}, nil
}

// bufconnScrub stands up the scrub server over a bufconn and returns a raw
// gRPC connection to it. The two dial helpers wrap it: dialScrub for the
// generated client contract tests, dialScrubClient for the adapter-server
// emission test that wires a real gatewaycontrol.Client.
func bufconnScrub(t *testing.T, srv *scrubServer) *grpc.ClientConn {
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
	return conn
}

func dialScrub(t *testing.T, srv *scrubServer) adapterv1.GatewayControlClient {
	t.Helper()
	return adapterv1.NewGatewayControlClient(bufconnScrub(t, srv))
}

// dialScrubClient returns the production adapter-side gatewaycontrol.Client
// bound to the bufconn scrub server, the exact SessionScrubReporter
// ConnectGateway wires onto the adapter Server.
func dialScrubClient(t *testing.T, srv *scrubServer) *gatewaycontrol.Client {
	t.Helper()
	return gatewaycontrol.New(bufconnScrub(t, srv))
}

// closeStubRuntime is a minimal adapter.RuntimeProcess double whose Close
// returns a programmed error, so the adapter's per-slot teardown observes a
// clean or failed runtime close and derives the ReportSessionScrub outcome
// from it. Start/WriteEnvelope/Interrupt/Output are no-ops the slot path needs
// only to accept a StartSession and reach the slot shutdown.
type closeStubRuntime struct {
	closeErr error
}

func (r *closeStubRuntime) Start(context.Context, string) error           { return nil }
func (r *closeStubRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (r *closeStubRuntime) Interrupt(context.Context, string, bool) error { return nil }

func (r *closeStubRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (r *closeStubRuntime) Close(context.Context, string) error { return r.closeErr }

// slotShutdownServer builds a production adapter.Server for the concurrent
// (slot) path wired to the bufconn scrub server as its SessionScrubReporter,
// a runtime whose Close returns closeErr, and a POD_NAME env so the cached pod
// identity is non-empty. It returns the server and the scrub server the
// adapter reports to.
func slotShutdownServer(t *testing.T, closeErr error) (*adapter.Server, *scrubServer) {
	t.Helper()
	t.Setenv("POD_NAME", "claude-code-pool-xyz")
	srv := &scrubServer{}
	base := t.TempDir()
	s := adapter.New("contract-test")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.Runtime = &closeStubRuntime{closeErr: closeErr}
	s.SessionScrubReporter = dialScrubClient(t, srv)
	return s, srv
}

// startAndShutdownSlot starts one concurrent-workspace slot then shuts it down,
// returning the ShutdownResponse so the caller can correlate the wire outcome
// with the ExitedCleanly flag the same closeErr sets.
func startAndShutdownSlot(t *testing.T, s *adapter.Server, sessionID, slotID string) *adapterv1.ShutdownResponse {
	t.Helper()
	ctx := context.Background()
	if _, err := s.StartSession(ctx, &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
		SlotId:    &adapterv1.SlotId{Value: slotID},
	}); err != nil {
		t.Fatalf("StartSession(slot %s): %v", slotID, err)
	}
	resp, err := s.Shutdown(ctx, &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		SlotId:    &adapterv1.SlotId{Value: slotID},
	})
	if err != nil {
		t.Fatalf("Shutdown(slot %s): %v", slotID, err)
	}
	return resp
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

// TestAdapterSlotShutdownEmitsLeakedOnFailedClose drives the production
// adapter Server through a concurrent-slot Shutdown whose runtime Close
// returns an error, and asserts the adapter emits exactly one
// ReportSessionScrub for the slot carrying outcome=LEAKED and the cached
// pod id, session id, and slot id. The leaked outcome is the sole feeder of
// the gateway leak ledger, the persistent leaked count, and the drain chain,
// so this pins the adapter's leaked-vs-released determination at the wire.
// spec: 5.2 (per-slot cleanup outcome reporting), 4.7 (runtime adapter leaked
// determination)
//
// diagnosis: a failure means the adapter mis-reports a failed per-slot cleanup
// as released or drops the report, so the leak ledger, the persistent leaked
// count, and the maxSessionsPerPod/drain liveness chain never fire; it would
// pass against the pre-fix code only if that code had emitted ReportSessionScrub
// at all, which it did not.
func TestAdapterSlotShutdownEmitsLeakedOnFailedClose_spec_5_2(t *testing.T) {
	s, srv := slotShutdownServer(t, errors.New("runtime close timed out reclaiming slot resources"))

	resp := startAndShutdownSlot(t, s, "sess-a", "slot-a")

	// The wire outcome derives from the same closeErr that sets ExitedCleanly:
	// a failed close is an unclean exit and a leaked cleanup.
	if resp.GetExitedCleanly() {
		t.Error("ExitedCleanly = true for a failed runtime close; the outcome and the flag must agree")
	}
	if len(srv.sessionReports) != 1 {
		t.Fatalf("ReportSessionScrub calls = %d, want exactly 1 per slot release", len(srv.sessionReports))
	}
	got := srv.sessionReports[0]
	if got.GetOutcome() != adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_LEAKED {
		t.Errorf("outcome = %v, want LEAKED for a failed slot cleanup", got.GetOutcome())
	}
	if got.GetPodId() != "claude-code-pool-xyz" {
		t.Errorf("pod id = %q, want the cached POD_NAME identity", got.GetPodId())
	}
	if got.GetSessionId().GetValue() != "sess-a" {
		t.Errorf("session id = %q, want sess-a", got.GetSessionId().GetValue())
	}
	if got.GetSlotId().GetValue() != "slot-a" {
		t.Errorf("slot id = %q, want slot-a", got.GetSlotId().GetValue())
	}
}

// TestAdapterSlotShutdownEmitsReleasedOnCleanClose is the released-outcome
// counterpart: a clean runtime Close yields exactly one ReportSessionScrub
// with outcome=RELEASED, so the two branches of the closeErr-derived outcome
// are both pinned at the wire.
// spec: 5.2 (per-slot cleanup outcome reporting), 4.7 (runtime adapter leaked
// determination)
//
// diagnosis: a failure means the adapter drops the release report or reports
// the wrong outcome on a clean slot teardown, so sessions_served never advances
// and the maxSessionsPerPod retirement stays inert on a healthy concurrent pool.
func TestAdapterSlotShutdownEmitsReleasedOnCleanClose_spec_5_2(t *testing.T) {
	s, srv := slotShutdownServer(t, nil)

	resp := startAndShutdownSlot(t, s, "sess-a", "slot-a")

	if !resp.GetExitedCleanly() {
		t.Error("ExitedCleanly = false for a clean runtime close")
	}
	if len(srv.sessionReports) != 1 {
		t.Fatalf("ReportSessionScrub calls = %d, want exactly 1 per slot release", len(srv.sessionReports))
	}
	if got := srv.sessionReports[0].GetOutcome(); got != adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_RELEASED {
		t.Errorf("outcome = %v, want RELEASED for a clean slot cleanup", got)
	}
	if got := srv.sessionReports[0].GetSlotId().GetValue(); got != "slot-a" {
		t.Errorf("slot id = %q, want slot-a", got)
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
