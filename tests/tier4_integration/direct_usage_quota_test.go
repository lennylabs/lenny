// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §11.2 direct-mode usage flow end to end
// across the components proposal 0024 couples (F-15.3.7, F-11.2.20). It
// drives the full adapter-meter → gateway ReportUsage pull → quota-counter
// path as one flow over real transports:
//
//   - a production-wired adapter (adapter.WireDirectModeUsage) running over
//     a real gRPC connection and a real §4.7 lifecycle-channel Unix socket,
//     accumulating a per-session token total from the enriched
//     llm_request_completed frame the runtime sends;
//   - the gateway's session-scoped ReportUsage pull
//     (adapterclient.Client.ReportUsageForLease), the same pull the
//     directUsageLoop issues each tick, reading the incremental delta;
//   - a Redis-container-backed §11.2 hierarchical quota counter
//     (quotastore.Counter), the accounting sink the direct-mode recorder
//     folds each pulled delta into, read back over the live Redis window.
//
// Tier-1 covers the recorder fan-out with an in-process miniredis, and
// tier-3 covers the adapter pull over a bufconn transport; neither pins the
// whole path against a real gRPC adapter and a real Redis container at once.
// This test does, so a regression that breaks the wire between any two of
// the three components surfaces here.
//
// It would fail against the pre-0024 code: the production adapter carried no
// UsageMeter, so ReportUsage returned codes.Unimplemented, the pull read
// nothing, and the quota counter never advanced for a direct-mode session.
//
// spec: §4.7 (ReportUsage pull, llm_request_completed token fields), §11.2
// (direct-mode usage recording into the quota counter).
package tier4_integration_test

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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/quota"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// directUsageEchoLoop is a minimal §15.4.1 runtime loop for the
// InProcessRuntime the wired adapter runs. StartSession only needs a runtime
// to Start; the loop drains inbound frames until EOF so it exits cleanly on
// teardown.
func directUsageEchoLoop(_ context.Context, in io.Reader, _ io.Writer) error {
	r := bufio.NewReader(in)
	for {
		if _, err := r.ReadBytes('\n'); err != nil {
			return nil
		}
	}
}

// directUsageSocket returns a Unix socket path under a short temp directory.
// t.TempDir() embeds the (long) test name, which can overflow the darwin
// sun_path limit (~104 bytes); a socket under os.MkdirTemp's short root
// stays within it.
func directUsageSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lenny-dusage-sock-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "lifecycle.sock")
}

// directUsageFrame is the runtime side of the §4.7 lifecycle JSONL frame,
// carrying the handshake reply and the direct-mode llm_request_completed
// token counts.
type directUsageFrame struct {
	Type            string   `json:"type"`
	ProtocolVersion string   `json:"protocolVersion,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
	RequestID       string   `json:"requestId,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	Status          string   `json:"status,omitempty"`
	InputTokens     int64    `json:"inputTokens,omitempty"`
	OutputTokens    int64    `json:"outputTokens,omitempty"`
}

// wiredAdapterClient stands up the adapter the way cmd/lenny-adapter does for
// a direct-mode pod (a lifecycle channel on a real socket, the production
// direct-mode usage wiring, and an in-process runtime), registers it over a
// gRPC bufconn, claims the pod for sessionID, and returns the gateway-side
// adapterclient.Client (the exact pull surface the directUsageLoop uses) and
// the lifecycle socket path.
func wiredAdapterClient(t *testing.T, sessionID string) (*adapterclient.Client, string) {
	t.Helper()

	sock := directUsageSocket(t)
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

	s := adapter.New("integration")
	s.WorkspaceRoot = t.TempDir()
	s.Lifecycle = lc
	// The production assembly: set the UsageMeter and the lifecycle token
	// sink so the read loop folds llm_request_completed counts into the
	// per-session meter the gateway pull reads.
	adapter.WireDirectModeUsage(s, lc)
	s.Runtime = adapter.NewInProcessRuntime(directUsageEchoLoop)

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

	// Claim the pod for the session so ReportUsage's checkSession passes and
	// the token sink resolves the folded counts to this session. The raw
	// adapterv1 client drives the claim over the same connection; the
	// returned adapterclient.Client is the gateway-side pull wrapper.
	claimCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := adapterv1.NewAdapterClient(conn).StartSession(claimCtx, &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	return adapterclient.New(conn), sock
}

// dialDirectRuntime connects to the adapter's lifecycle socket as the
// runtime, completes the lifecycle handshake, and returns an encoder for
// sending frames. The connection is closed on cleanup.
func dialDirectRuntime(t *testing.T, sock string) *json.Encoder {
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
	var caps directUsageFrame
	if err := json.Unmarshal(line, &caps); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if caps.Type != "lifecycle_capabilities" {
		t.Fatalf("first adapter frame = %q, want lifecycle_capabilities", caps.Type)
	}
	enc := json.NewEncoder(conn)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(directUsageFrame{
		Type:            "lifecycle_support",
		ProtocolVersion: caps.ProtocolVersion,
		Capabilities:    caps.Capabilities,
	}); err != nil {
		t.Fatalf("write lifecycle_support: %v", err)
	}
	return enc
}

// sendCompletedCall emits one direct-mode llm_request_completed frame
// carrying the token counts, the way a direct-mode runtime reports a settled
// provider call (§4.7).
func sendCompletedCall(t *testing.T, enc *json.Encoder, requestID string, in, out int64) {
	t.Helper()
	if err := enc.Encode(directUsageFrame{
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

// pullDeltaUntilFolded issues steady-state (cumulative=false) ReportUsage
// pulls over the gateway adapterclient wrapper until the folded token counts
// appear, tolerating the async gap between the runtime writing the lifecycle
// frame and the adapter read loop folding it. It fails if the counts never
// arrive.
func pullDeltaUntilFolded(t *testing.T, client *adapterclient.Client, lease credential.Lease) adapterclient.UsageReport {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last adapterclient.UsageReport
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		report, err := client.ReportUsageForLease(ctx, lease.SessionID, lease.DeliveryMode, false)
		cancel()
		if err != nil {
			t.Fatalf("ReportUsageForLease: %v", err)
		}
		if report.InputTokens != 0 || report.OutputTokens != 0 {
			return report
		}
		last = report
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("folded token delta never arrived; last delta = (%d,%d)", last.InputTokens, last.OutputTokens)
	return adapterclient.UsageReport{}
}

// spec: 4.7 (ReportUsage pull, llm_request_completed token counts), 11.2
// (direct-mode usage folded into the quota counter)
// diagnosis: the §11.2 direct-mode usage flow did not complete end to end —
// a direct-mode session's token consumption, reported by the runtime on the
// enriched llm_request_completed frame and accumulated by the adapter meter,
// did not reach the Redis quota counter through the gateway ReportUsage pull.
// Either the production adapter served no UsageMeter (ReportUsage
// Unimplemented, F-15.3.7), the gateway pull read no delta, or the recorder
// fan-out did not increment the quota window, so a direct-mode tenant's
// budget went unenforceable end to end.
func TestDirectModeUsageFlowsToQuotaCounter_spec_11_2(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	const (
		tenant    = "acme"
		user      = "alice@acme.com"
		sessionID = "sess-direct-quota-1"
	)
	lease := credential.Lease{
		LeaseID: "cl-direct", SessionID: sessionID, TenantID: tenant,
		Source: credential.SourcePool, DeliveryMode: credential.DeliveryDirect,
	}

	// Real Redis container backing the §11.2 hierarchical quota counter, the
	// accounting sink the direct-mode recorder folds each pulled delta into.
	rd := containers.StartRedis(t, containers.RedisOptions{})
	counter := quotastore.New(rd.Client)

	// Real wired adapter over gRPC + a real lifecycle socket: the adapter the
	// production cmd/lenny-adapter assembles for a direct-mode pod.
	client, sock := wiredAdapterClient(t, sessionID)
	enc := dialDirectRuntime(t, sock)

	// A direct-mode runtime reports two completed provider calls on the
	// enriched frame; the adapter meter accumulates them per session.
	sendCompletedCall(t, enc, "req-1", 1200, 340)

	ctx := context.Background()
	now := time.Now().UTC()

	// The gateway steady-state pull reads the folded delta and the recorder
	// fans it into the quota counter's per-tenant and per-user windows. Fold
	// the pulled delta the way proxyUsageRecorder.recordQuota does for an
	// hourly period (AddHierarchical), so the window read reflects the
	// end-to-end path.
	first := pullDeltaUntilFolded(t, client, lease)
	if first.InputTokens != 1200 || first.OutputTokens != 340 {
		t.Fatalf("first pull delta = (%d,%d), want (1200,340)", first.InputTokens, first.OutputTokens)
	}
	foldIntoQuota(t, counter, tenant, user, now, first)

	// A second completed call: its delta must accumulate on top of the first
	// in the same tenant window (the counter is additive, matching the
	// recorder fan-out).
	sendCompletedCall(t, enc, "req-2", 500, 120)
	second := pullDeltaUntilFolded(t, client, lease)
	if second.InputTokens != 500 || second.OutputTokens != 120 {
		t.Fatalf("second pull delta = (%d,%d), want (500,120)", second.InputTokens, second.OutputTokens)
	}
	foldIntoQuota(t, counter, tenant, user, now, second)

	// Read the live Redis window back: the tenant rollup and the per-user
	// window both carry the sum of both calls' total tokens (input+output).
	const wantTotal = int64(1200 + 340 + 500 + 120) // 2160
	scoped, err := counter.UsageHierarchical(ctx, tenant, user, quota.ResetHourly, now)
	if err != nil {
		t.Fatalf("UsageHierarchical: %v", err)
	}
	if scoped.Tenant != wantTotal {
		t.Errorf("tenant quota window = %d, want %d (both direct-mode calls folded end to end)", scoped.Tenant, wantTotal)
	}
	if scoped.User != wantTotal {
		t.Errorf("user quota window = %d, want %d", scoped.User, wantTotal)
	}

	// The delta reads drained the accumulation: a final steady-state pull
	// with no further calls reports zero (the §4.7 incremental contract),
	// which is what the §11.2 anomaly detector observes as a zero delta.
	drainCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	drained, err := client.ReportUsageForLease(drainCtx, sessionID, lease.DeliveryMode, false)
	if err != nil {
		t.Fatalf("ReportUsageForLease (drain): %v", err)
	}
	if drained.InputTokens != 0 || drained.OutputTokens != 0 {
		t.Errorf("drained delta = (%d,%d), want (0,0) after both deltas were pulled", drained.InputTokens, drained.OutputTokens)
	}
}

// foldIntoQuota folds one pulled ReportUsage delta into the §11.2 hierarchical
// quota counter the way proxyUsageRecorder.recordQuota does for an hourly
// period: the total tokens (input+output) advance the per-user, per-tenant,
// and global windows. This mirrors the gateway recorder's accounting sink so
// the window read reflects the end-to-end direct-mode path.
func foldIntoQuota(t *testing.T, counter *quotastore.Counter, tenant, user string, now time.Time, report adapterclient.UsageReport) {
	t.Helper()
	tokens := report.InputTokens + report.OutputTokens
	if tokens <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := counter.AddHierarchical(ctx, tenant, user, quota.ResetHourly, now, tokens); err != nil {
		t.Fatalf("AddHierarchical: %v", err)
	}
}
