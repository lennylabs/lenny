// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §11.2 direct-mode usage flow end to end
// across the components proposal 0024 couples (F-15.3.7, F-11.2.20). It
// drives the full adapter-meter → gateway ReportUsage pull → gateway usage
// sink → quota-counter path as one flow over real transports and the
// production gateway sink:
//
//   - a production-wired adapter (adapter.WireDirectModeUsage) running over
//     a real gRPC connection and a real §4.7 lifecycle-channel Unix socket,
//     accumulating a per-session token total from the enriched
//     llm_request_completed frame the runtime sends;
//   - the gateway's session-scoped ReportUsage pull
//     (adapterclient.Client.ReportUsageForLease), the same pull the
//     directUsageLoop issues each tick, reading the incremental delta;
//   - the production gateway usage sink (*proxyUsageRecorder.RecordDirectUsage
//     → recordAccounting → recordQuota → AddHierarchical), the fan-out the
//     direct-mode poll loop hands each pulled delta to; and
//   - a Redis-container-backed §11.2 hierarchical quota counter
//     (quotastore.Counter), the accounting sink the recorder folds each
//     pulled delta into, read back over the live Redis window.
//
// The final gateway-sink → quota-counter hop this tier exists to verify runs
// against the production recorder rather than a test-local copy of the sink:
// the test constructs newProxyUsageRecorder and calls RecordDirectUsage with
// each pulled delta, so recordQuota's period resolution and AddHierarchical
// window write are exercised by the shipped code. Tier-1 covers the recorder
// fan-out with an in-process miniredis, and tier-3 covers the adapter pull
// over a bufconn transport; neither pins the whole path against a real gRPC
// adapter, the production recorder, and a real Redis container at once. This
// test does, so a regression that breaks the wire between any two of the
// components surfaces here.
//
// This test lives in package main because the production sink
// (*proxyUsageRecorder) is unexported; the tier-4 flow the proposal names
// reaches the quota counter through that recorder, so the test is colocated
// with it rather than reimplementing the sink from an external package.
//
// It would fail against the pre-0024 code: the production adapter carried no
// UsageMeter, so ReportUsage returned codes.Unimplemented, the pull read
// nothing, and RecordDirectUsage never advanced the quota counter for a
// direct-mode session.
//
// spec: §4.7 (ReportUsage pull, llm_request_completed token fields), §11.2
// (direct-mode usage recording into the quota counter).
package main

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
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionusage"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/quota"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
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

// directQuotaLimits is a policy.TenantLimitLookup returning a fixed hourly
// reset period for every tenant, so the production recorder's recordQuota
// resolves the same §11.2 window UsageHierarchical reads back.
type directQuotaLimits struct{}

func (directQuotaLimits) LookupLimits(_ context.Context, _ string) (policy.TenantLimits, error) {
	return policy.TenantLimits{Period: quota.ResetHourly}, nil
}

// spec: 4.7 (ReportUsage pull, llm_request_completed token counts), 11.2
// (direct-mode usage folded into the quota counter through the production
// gateway sink)
// diagnosis: the §11.2 direct-mode usage flow did not complete end to end —
// a direct-mode session's token consumption, reported by the runtime on the
// enriched llm_request_completed frame and accumulated by the adapter meter,
// did not reach the Redis quota counter through the gateway ReportUsage pull
// and the production RecordDirectUsage fan-out. Either the production adapter
// served no UsageMeter (ReportUsage Unimplemented, F-15.3.7), the gateway pull
// read no delta, or the recorder's recordQuota/AddHierarchical write did not
// increment the quota window, so a direct-mode tenant's budget went
// unenforceable end to end.
func TestDirectModeUsageFlowsToQuotaCounter_spec_11_2(t *testing.T) {
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

	// The production gateway usage sink: the recorder the direct-mode poll
	// loop hands each pulled delta to. Its session store resolves the per-user
	// quota attribution; its limit lookup resolves the §11.2 reset period; its
	// quota counter is the same live Redis counter the test reads back. The
	// idle stamper, session-usage accumulator, and enforcer are left nil (this
	// flow verifies the metering-to-quota hop, not idle reset or budget breach).
	sessions := memstore.New()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: sessionID, TenantID: tenant, UserID: user, RuntimeRef: "echo", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("sessions.Create: %v", err)
	}
	var usageStore usagestore.Store = usagestore.NewMemory()
	sessUsage := sessionusage.NewMemory()
	now := time.Now().UTC()
	rec := newProxyUsageRecorder(usageStore, sessions, sessUsage, counter, directQuotaLimits{}, nil)
	if rec == nil {
		t.Fatal("newProxyUsageRecorder returned nil with a usage store set")
	}
	// Pin the recorder's quota-window clock so recordQuota keys the same hourly
	// window UsageHierarchical reads back below.
	rec.now = func() time.Time { return now }

	// Real wired adapter over gRPC + a real lifecycle socket: the adapter the
	// production cmd/lenny-adapter assembles for a direct-mode pod.
	client, sock := wiredAdapterClient(t, sessionID)
	enc := dialDirectRuntime(t, sock)

	ctx := context.Background()

	// A direct-mode runtime reports a completed provider call on the enriched
	// frame; the adapter meter accumulates it per session. The gateway
	// steady-state pull reads the folded delta and the production recorder fans
	// it into the quota counter's per-tenant and per-user windows through
	// RecordDirectUsage → recordQuota → AddHierarchical.
	sendCompletedCall(t, enc, "req-1", 1200, 340)
	first := pullDeltaUntilFolded(t, client, lease)
	if first.InputTokens != 1200 || first.OutputTokens != 340 {
		t.Fatalf("first pull delta = (%d,%d), want (1200,340)", first.InputTokens, first.OutputTokens)
	}
	rec.RecordDirectUsage(ctx, lease, llmproxy.Usage{
		InputTokens:  int(first.InputTokens),
		OutputTokens: int(first.OutputTokens),
	})

	// A second completed call: its delta must accumulate on top of the first in
	// the same tenant window (the counter is additive, matching the recorder
	// fan-out).
	sendCompletedCall(t, enc, "req-2", 500, 120)
	second := pullDeltaUntilFolded(t, client, lease)
	if second.InputTokens != 500 || second.OutputTokens != 120 {
		t.Fatalf("second pull delta = (%d,%d), want (500,120)", second.InputTokens, second.OutputTokens)
	}
	rec.RecordDirectUsage(ctx, lease, llmproxy.Usage{
		InputTokens:  int(second.InputTokens),
		OutputTokens: int(second.OutputTokens),
	})

	// Read the live Redis window back: the tenant rollup and the per-user
	// window both carry the sum of both calls' total tokens (input+output),
	// written by the production recorder rather than a test-local copy.
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

	// The production recorder also lands the counts in the §15.1 metering store:
	// the same fan-out that fed the quota counter fed the billing rollup, so a
	// direct-mode tenant's usage is both enforceable and billable.
	metered, err := usageStore.Aggregate(ctx, tenant, nil)
	if err != nil {
		t.Fatalf("usage.Aggregate: %v", err)
	}
	if metered.TotalTokens.Input != 1700 || metered.TotalTokens.Output != 460 {
		t.Errorf("metered tokens = %+v, want input=1700 output=460", metered.TotalTokens)
	}

	// The delta reads drained the accumulation: a final steady-state pull with
	// no further calls reports zero (the §4.7 incremental contract), which is
	// what the §11.2 anomaly detector observes as a zero delta.
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
