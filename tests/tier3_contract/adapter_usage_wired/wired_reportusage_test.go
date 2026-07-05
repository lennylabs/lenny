//go:build contract

// SPDX-License-Identifier: MIT

// Package adapter_usage_wired_test is the Tier 3 contract suite for the
// production-wired §4.7 ReportUsage pull (proposal 0024 S4, F-15.3.7).
//
// F-15.3.7 recorded that the adapter ReportUsage handler is implemented
// but returns codes.Unimplemented in production because no code sets the
// server's UsageMeter: the handler falls through to the nil-meter guard
// for every shipped runtime. S4 wires the concrete SessionUsageMeter onto
// the adapter server (adapter.WireDirectModeUsage, the exact assembly
// cmd/lenny-adapter performs) and folds the §4.7 llm_request_completed
// lifecycle frame's direct-mode token counts into it, so the gateway pull
// reads a real accounting.
//
// This suite pins that wire outcome end to end across the two component
// contracts the fix couples: the runtime→adapter llm_request_completed
// JSONL frame (the direct-mode token source) and the gateway→adapter
// ReportUsage gRPC pull. It stands up the production-assembled adapter
// over a real gRPC connection and a real lifecycle-channel socket, folds
// a completed direct-mode LLM call's token counts through the wired sink,
// and asserts the gateway's ReportUsage pull returns the folded delta with
// cumulative unset and the running cumulative total with it set, rather
// than the pre-S4 codes.Unimplemented. It fails against the pre-S4 code,
// where the production server carried no meter and ReportUsage was
// Unimplemented.
//
// spec: §4.7 (ReportUsage pull, llm_request_completed token fields), §11.2
// (direct-mode usage, crash-recovery cumulative pull).
package adapter_usage_wired_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// echoLoop is a minimal §15.4.1 runtime loop for the InProcessRuntime the
// wired adapter runs. StartSession only needs a runtime to Start; the loop
// drains inbound frames until EOF so it exits cleanly on teardown.
func echoLoop(_ context.Context, in io.Reader, _ io.Writer) error {
	r := bufio.NewReader(in)
	for {
		if _, err := r.ReadBytes('\n'); err != nil {
			return nil
		}
	}
}

// shortSocket returns a Unix socket path under a short temp directory.
// t.TempDir() embeds the (long) test name, which can overflow the darwin
// sun_path limit (~104 bytes); a socket under os.MkdirTemp's short root
// stays within it.
func shortSocket(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-usage-sock-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

// wiredAdapter builds the adapter the way cmd/lenny-adapter does for a
// direct-mode Full-level pod: a lifecycle channel on a real socket, the
// production direct-mode usage wiring (WireDirectModeUsage sets the meter
// and the token sink), and an in-process runtime so StartSession can claim
// the pod. It registers the server over bufconn and returns the gateway
// ReportUsage client and the lifecycle socket path.
func wiredAdapter(t *testing.T) (adapterv1.AdapterClient, string) {
	t.Helper()

	sock := shortSocket(t, "lifecycle.sock")
	lc, err := adapter.NewLifecycleChannel(sock)
	if err != nil {
		t.Fatalf("NewLifecycleChannel: %v", err)
	}
	lcCtx, lcCancel := context.WithCancel(context.Background())
	t.Cleanup(lcCancel)
	lcDone := make(chan error, 1)
	go func() { lcDone <- lc.Run(lcCtx) }()
	t.Cleanup(func() {
		_ = lc.Close()
		<-lcDone
	})

	root := t.TempDir()
	s := adapter.New("contract")
	s.WorkspaceRoot = root
	s.Lifecycle = lc
	// The production assembly: set the UsageMeter and the token sink the
	// lifecycle read loop folds llm_request_completed counts into. Before
	// S4 nothing performed this wiring, so ReportUsage was Unimplemented.
	adapter.WireDirectModeUsage(s, lc)
	s.Runtime = adapter.NewInProcessRuntime(echoLoop)

	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
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

	client := adapterv1.NewAdapterClient(conn)

	// Claim the pod for the session so ReportUsage's checkSession passes
	// and the token sink resolves the folded counts to this session.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.StartSession(ctx, &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	return client, sock
}

// sessionID is the direct-mode session the wired adapter holds for these
// tests (§6.1 one session per pod).
const sessionID = "sess-direct-1"

// lifecycleFrame is the runtime side of the §4.7 lifecycle JSONL frame,
// carrying just the fields these tests send: the handshake reply and the
// direct-mode llm_request_completed token counts.
type lifecycleFrame struct {
	Type            string   `json:"type"`
	ProtocolVersion string   `json:"protocolVersion,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
	RequestID       string   `json:"requestId,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	Status          string   `json:"status,omitempty"`
	InputTokens     int64    `json:"inputTokens,omitempty"`
	OutputTokens    int64    `json:"outputTokens,omitempty"`
}

// dialRuntime connects to the adapter's lifecycle socket as the runtime,
// completes the lifecycle_capabilities handshake, and returns an encoder
// and reader for sending frames. The connection is closed on cleanup.
func dialRuntime(t *testing.T, sock string) *json.Encoder {
	t.Helper()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial lifecycle socket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	r := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read lifecycle_capabilities: %v", err)
	}
	var caps lifecycleFrame
	if err := json.Unmarshal(line, &caps); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if caps.Type != "lifecycle_capabilities" {
		t.Fatalf("first adapter frame = %q, want lifecycle_capabilities", caps.Type)
	}
	enc := json.NewEncoder(conn)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(lifecycleFrame{
		Type:            "lifecycle_support",
		ProtocolVersion: caps.ProtocolVersion,
		Capabilities:    caps.Capabilities,
	}); err != nil {
		t.Fatalf("write lifecycle_support: %v", err)
	}
	return enc
}

// sendCompleted emits one direct-mode llm_request_completed frame carrying
// the token counts, the way a direct-mode runtime reports a settled
// provider call (§4.7).
func sendCompleted(t *testing.T, enc *json.Encoder, requestID string, in, out int64) {
	t.Helper()
	if err := enc.Encode(lifecycleFrame{
		Type:         "llm_request_completed",
		RequestID:    requestID,
		Provider:     "anthropic",
		Status:       "ok",
		InputTokens:  in,
		OutputTokens: out,
	}); err != nil {
		t.Fatalf("write llm_request_completed: %v", err)
	}
}

// reportUsage issues the gateway ReportUsage pull over gRPC. cumulative
// selects the §11.2 crash-recovery cumulative read; false is the
// steady-state delta poll.
func reportUsage(t *testing.T, client adapterv1.AdapterClient, cumulative bool) *adapterv1.ReportUsageResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := client.ReportUsage(ctx, &adapterv1.ReportUsageRequest{
		SessionId:  &adapterv1.SessionId{Value: sessionID},
		Cumulative: cumulative,
	})
	if err != nil {
		t.Fatalf("ReportUsage(cumulative=%v): %v (code %v)", cumulative, err, status.Code(err))
	}
	return resp
}

// pollUntilFolded issues delta ReportUsage pulls until the folded token
// counts appear, tolerating the async gap between the runtime writing the
// lifecycle frame and the adapter read loop folding it. It fails if the
// counts never arrive. Returns the delta that carried them.
func pollUntilFolded(t *testing.T, client adapterv1.AdapterClient, wantIn, wantOut int64) *adapterv1.ReportUsageResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last *adapterv1.ReportUsageResponse
	for time.Now().Before(deadline) {
		resp := reportUsage(t, client, false)
		if resp.GetInputTokens() != 0 || resp.GetOutputTokens() != 0 {
			return resp
		}
		last = resp
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("folded token delta never arrived; last delta = (%d,%d), want (%d,%d)",
		last.GetInputTokens(), last.GetOutputTokens(), wantIn, wantOut)
	return nil
}

// TestWiredReportUsageReturnsDelta pins that the production-wired adapter's
// ReportUsage pull returns the folded direct-mode token delta over the wire
// with cumulative unset, rather than the pre-S4 codes.Unimplemented. A
// direct-mode runtime reports a completed provider call carrying its token
// counts on the §4.7 llm_request_completed frame; the wired sink folds them
// into the session meter, and the gateway's steady-state ReportUsage pull
// reads the delta.
//
// spec: 4.7 (ReportUsage pull, llm_request_completed token counts), 11.2
// (direct-mode usage)
//
// diagnosis: the production adapter served no UsageMeter, so ReportUsage
// returned codes.Unimplemented for every direct-mode session (F-15.3.7) —
// the gateway usage pull read nothing and direct-mode budgets went
// unmonitored. Confirm cmd/lenny-adapter wires adapter.WireDirectModeUsage
// (the meter plus the lifecycle token sink) during server assembly.
func TestWiredReportUsageReturnsDelta_spec_4_7(t *testing.T) {
	client, sock := wiredAdapter(t)
	enc := dialRuntime(t, sock)

	// One completed direct-mode LLM call reports its token counts.
	sendCompleted(t, enc, "req-1", 1200, 340)

	delta := pollUntilFolded(t, client, 1200, 340)
	if delta.GetInputTokens() != 1200 || delta.GetOutputTokens() != 340 {
		t.Fatalf("delta = (%d,%d), want (1200,340)", delta.GetInputTokens(), delta.GetOutputTokens())
	}
	// The delta read drained the accumulation: the next steady-state pull
	// with no further calls reports zero (the §4.7 incremental contract),
	// which is what the §11.2 anomaly detector observes as a zero delta.
	drained := reportUsage(t, client, false)
	if drained.GetInputTokens() != 0 || drained.GetOutputTokens() != 0 {
		t.Errorf("second delta = (%d,%d), want (0,0) after the first drained the accumulation",
			drained.GetInputTokens(), drained.GetOutputTokens())
	}
}

// TestWiredReportUsageCumulativeReturnsRunningTotal pins that the wired
// adapter's cumulative ReportUsage pull (the flag a reconnected gateway
// replica sets) returns the session's running cumulative total over the
// wire, so the §11.2 crash-recovery MAX rule
// (MAX(postgres_checkpoint, pod-reported cumulative total)) has a wire path
// to the value it needs. Two completed direct-mode calls accumulate; the
// cumulative pull returns their sum regardless of any intervening delta
// read.
//
// spec: 4.7 (ReportUsage cumulative read), 11.2 (crash recovery,
// pod-reported cumulative total)
//
// diagnosis: the cumulative ReportUsage pull did not return the pod's
// running total, so a reconnected gateway replica's MAX-rule recovery read
// only a delta and under-counted — un-recovering the direct-mode budget
// protection §11.2 exists to provide. Confirm the wired meter serves the
// cumulative read and the handler selects it on ReportUsageRequest.cumulative.
func TestWiredReportUsageCumulativeReturnsRunningTotal_spec_11_2(t *testing.T) {
	client, sock := wiredAdapter(t)
	enc := dialRuntime(t, sock)

	// First call: fold its counts and drain the delta so a subsequent
	// delta read alone could not reconstruct the running total.
	sendCompleted(t, enc, "req-1", 1000, 250)
	first := pollUntilFolded(t, client, 1000, 250)
	if first.GetInputTokens() != 1000 || first.GetOutputTokens() != 250 {
		t.Fatalf("first delta = (%d,%d), want (1000,250)", first.GetInputTokens(), first.GetOutputTokens())
	}

	// Second call: fold its counts too. The cumulative pull must return the
	// sum of both calls, even though the first was already drained by the
	// delta read above.
	sendCompleted(t, enc, "req-2", 500, 120)
	// Wait for the second frame to fold by polling the cumulative read,
	// which is the value under test: it climbs to the running total.
	deadline := time.Now().Add(5 * time.Second)
	var cum *adapterv1.ReportUsageResponse
	for time.Now().Before(deadline) {
		cum = reportUsage(t, client, true)
		if cum.GetInputTokens() == 1500 && cum.GetOutputTokens() == 370 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cum.GetInputTokens() != 1500 || cum.GetOutputTokens() != 370 {
		t.Fatalf("cumulative pull = (%d,%d), want (1500,370) = the running total of both calls",
			cum.GetInputTokens(), cum.GetOutputTokens())
	}

	// After a cumulative read advances the watermark to the running total,
	// the next steady-state delta returns zero rather than re-adding the
	// already-recovered tokens (the §11.2:46 no-double-count invariant).
	after := reportUsage(t, client, false)
	if after.GetInputTokens() != 0 || after.GetOutputTokens() != 0 {
		t.Errorf("delta after cumulative = (%d,%d), want (0,0): a cumulative read must advance the watermark",
			after.GetInputTokens(), after.GetOutputTokens())
	}
}

// TestWiredReportUsageIsNotUnimplemented pins the load-bearing F-15.3.7
// outcome directly: the production-wired adapter's ReportUsage does not
// return codes.Unimplemented even before any token frame arrives. A session
// with no folded tokens yet reports a zero accounting, not Unimplemented —
// the gateway pull always gets an answer.
//
// spec: 4.7 (ReportUsage is implemented in production)
//
// diagnosis: ReportUsage returned codes.Unimplemented because the
// production server's UsageMeter was nil (F-15.3.7). This is the exact
// pre-S4 failure: the wiring (adapter.WireDirectModeUsage) was absent. A
// Unimplemented here means cmd/lenny-adapter no longer sets the meter.
func TestWiredReportUsageIsNotUnimplemented_spec_4_7(t *testing.T) {
	client, _ := wiredAdapter(t)

	resp := reportUsage(t, client, false)
	if resp.GetInputTokens() != 0 || resp.GetOutputTokens() != 0 {
		t.Fatalf("fresh session delta = (%d,%d), want (0,0)", resp.GetInputTokens(), resp.GetOutputTokens())
	}

	// Assert the negative directly: reportUsage would already have failed on
	// a non-nil error, but pin that the code is specifically not Unimplemented
	// so a regression that re-nils the meter is caught with the right diagnosis.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.ReportUsage(ctx, &adapterv1.ReportUsageRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	})
	if status.Code(err) == codes.Unimplemented {
		t.Fatal("wired adapter ReportUsage returned Unimplemented; the production UsageMeter is not set (F-15.3.7)")
	}
	if err != nil {
		t.Fatalf("ReportUsage: unexpected error %v", err)
	}
}
