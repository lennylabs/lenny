// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §4.7 Full-level credential-rotation protocol
// under a withholding runtime, the compromise-defeating failure path the
// spec's "Full-level credential rotation protocol" section defines.
//
// The threat the ceiling closes: in direct mode a compromised or buggy
// runtime can indefinitely block credentials_rotated by never emitting
// llm_request_completed for an in-flight LLM request, keeping a revoked or
// faulted credential reachable at the provider. The unit coverage in
// pkg/adapter/rotationgate_test.go drives the adapter server in-package with
// an internal fake runtime; this chaos test drives the real adapter.Server
// and the real adapter.LifecycleChannel over a real Unix-socket connection
// with an external runtime peer that speaks the §4.7 JSONL lifecycle
// protocol and deliberately withholds the completion frame, so the ceiling,
// the proactive_renewal exemption, and the ack-timeout fallback are pinned
// above unit against the wire the production runtime uses.
//
// The 300 s ceiling and the 60 s ack timeout are operator-tunable defaults
// (Server.RotationInflightCeiling / Server.CredentialsAckTimeout); the
// adapter arms them with real time.Timer/context deadlines rather than an
// injected clock, so the test overrides them to short real durations to run
// deterministically without waiting minutes of wall-clock. The assertions
// are on the observable contract (which frame the runtime peer sees and
// when, the ceiling counter, the durable audit event, the DeadlineExceeded
// fall-through, and the grace-period metric), not on the exact ceiling value.
package tier8_chaos_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// rotationFrame is one §4.7 runtime<->adapter lifecycle JSONL frame, in the
// subset this test needs to speak from the external runtime peer. Field
// names match the §4.7 message-schema table (camelCase).
type rotationFrame struct {
	Type            string   `json:"type"`
	ProtocolVersion string   `json:"protocolVersion,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	CredentialsPath string   `json:"credentialsPath,omitempty"`
	LeaseID         string   `json:"leaseId,omitempty"`
	RequestID       string   `json:"requestId,omitempty"`
	Status          string   `json:"status,omitempty"`
}

// runtimePeer is the external §4.7 lifecycle-channel runtime, dialing the
// adapter's Unix socket and driving frames over the wire. It models a
// direct-mode Full-level runtime that can start an LLM request and then
// withhold the completion frame to hold the in-flight gate open.
type runtimePeer struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	enc  *json.Encoder
}

// dialRuntimePeer connects to the adapter lifecycle socket, completes the
// lifecycle_capabilities / lifecycle_support handshake advertising
// credential_rotation, and returns the connected peer.
func dialRuntimePeer(t *testing.T, socketPath string) *runtimePeer {
	t.Helper()
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial lifecycle socket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	p := &runtimePeer{t: t, conn: conn, r: bufio.NewReader(conn), enc: json.NewEncoder(conn)}

	// The adapter opens with lifecycle_capabilities; the runtime replies
	// with lifecycle_support naming the subset it implements.
	cap := p.read()
	if cap.Type != "lifecycle_capabilities" {
		t.Fatalf("handshake: got %q, want lifecycle_capabilities", cap.Type)
	}
	p.send(rotationFrame{
		Type:            "lifecycle_support",
		ProtocolVersion: "1.0",
		Capabilities:    []string{"checkpoint", "interrupt", "credential_rotation", "deadline_signal"},
	})
	return p
}

func (p *runtimePeer) send(f rotationFrame) {
	p.t.Helper()
	if err := p.enc.Encode(f); err != nil {
		p.t.Fatalf("send %q frame: %v", f.Type, err)
	}
}

// read blocks for the next frame from the adapter.
func (p *runtimePeer) read() rotationFrame {
	p.t.Helper()
	line, err := p.r.ReadBytes('\n')
	if err != nil {
		p.t.Fatalf("read lifecycle frame: %v", err)
	}
	var f rotationFrame
	if err := json.Unmarshal(line, &f); err != nil {
		p.t.Fatalf("decode lifecycle frame: %v", err)
	}
	return f
}

// expectSilence asserts the adapter sends no frame within d. The in-flight
// gate must hold credentials_rotated while the withheld request is counted
// as in flight (§4.7 in-flight request completion gate).
func (p *runtimePeer) expectSilence(d time.Duration) {
	p.t.Helper()
	_ = p.conn.SetReadDeadline(time.Now().Add(d))
	defer func() { _ = p.conn.SetReadDeadline(time.Time{}) }()
	if _, err := p.r.ReadBytes('\n'); err == nil {
		p.t.Fatal("adapter sent a frame while the in-flight gate should have blocked it")
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		p.t.Fatalf("expected read timeout while gate holds, got %v", err)
	}
}

// startAdapter brings up a real adapter.Server bound to a real lifecycle
// channel on a Unix socket, with credentials assigned for one direct-mode
// provider. It returns the server, the socket path, and the recording audit
// emitter wired to the §4.9.2 EventStore hook.
func startAdapter(t *testing.T, pool string) (*adapter.Server, string, *recordingCeilingAudit) {
	t.Helper()
	// t.TempDir() embeds the (long) test name, so a socket path under it can
	// overflow the platform sun_path limit (~104 bytes on darwin); bind under
	// os.MkdirTemp's short root to stay within it.
	sockDir, err := os.MkdirTemp("", "lenny-rot-*")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	socketPath := filepath.Join(sockDir, "lc.sock")
	lc, err := adapter.NewLifecycleChannel(socketPath)
	if err != nil {
		t.Fatalf("new lifecycle channel: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = lc.Run(ctx) }()

	audit := &recordingCeilingAudit{}
	s := adapter.New("rotation-chaos")
	s.CredentialsDir = t.TempDir()
	s.CheckpointPoolLabel = pool
	s.RuntimeName = "claude-code"
	s.Lifecycle = lc
	s.RotationAudit = audit
	if _, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-chaos"},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": {LeaseId: "l-old", Provider: "anthropic", Payload: []byte("{}")},
		},
	}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}
	return s, socketPath, audit
}

func rotateRequest(leaseID, trigger string) *adapterv1.RotateCredentialsRequest {
	return &adapterv1.RotateCredentialsRequest{
		SessionId:       &adapterv1.SessionId{Value: "sess-chaos"},
		RotationTrigger: trigger,
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": {LeaseId: leaseID, Provider: "anthropic", Payload: []byte("{}")},
		},
	}
}

// recordingCeilingAudit captures the durable credential.rotation_ceiling_hit
// audit events the adapter emits to the §4.9.2 EventStore at the ceiling
// code point.
type recordingCeilingAudit struct {
	hits []adapter.RotationCeilingHit
}

func (r *recordingCeilingAudit) EmitRotationCeilingHit(_ context.Context, e adapter.RotationCeilingHit) {
	r.hits = append(r.hits, e)
}

// startWithheldInflight has the peer report one llm_request_started and
// waits until the adapter's in-flight counter observes it, then returns
// without ever sending the matching llm_request_completed. This models the
// compromised/buggy runtime that never completes.
func (p *runtimePeer) startWithheldInflight(s *adapter.Server, provider string) {
	p.t.Helper()
	p.send(rotationFrame{Type: "llm_request_started", Provider: provider, RequestID: "r1"})
	deadline := time.Now().Add(2 * time.Second)
	for s.Lifecycle.InflightCount(provider) != 1 {
		if time.Now().After(deadline) {
			p.t.Fatal("adapter never observed the withheld in-flight request")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestRotationInflightCeilingForcesRotatedFrameOnWithholdingRuntime pins the
// §4.7 "Revocation-triggered rotation ceiling": for a fault trigger, a
// runtime that never completes the in-flight request cannot hold the gate
// open forever. At the ceiling the adapter sends credentials_rotated
// regardless of the in-flight counter, increments the ceiling counter, and
// emits the durable credential.rotation_ceiling_hit audit event.
//
// spec: §4.7 (Full-level credential rotation protocol — Revocation-triggered
// rotation ceiling), §4.9 (rotationTrigger ceiling-applicability rule)
//
// diagnosis: A failure here means a compromised or buggy direct-mode runtime
// can indefinitely block a fault/revocation credential rotation by never
// emitting llm_request_completed, keeping the revoked or faulted credential
// reachable at the provider — the exact attack the ceiling closes. Either
// the ceiling did not fire (no credentials_rotated frame), or the
// compromise-indicator forensics (ceiling counter, rotation_ceiling_hit
// audit event) were not recorded.
func TestRotationInflightCeilingForcesRotatedFrameOnWithholdingRuntime_spec_4_7(t *testing.T) {
	const pool = "rotation-chaos-ceiling"
	s, socketPath, audit := startAdapter(t, pool)
	s.RotationInflightCeiling = 150 * time.Millisecond
	s.CredentialsAckTimeout = 5 * time.Second

	peer := dialRuntimePeer(t, socketPath)
	if !s.Lifecycle.WaitHandshake(context.Background(), 2*time.Second) {
		t.Fatal("lifecycle handshake did not complete")
	}
	peer.startWithheldInflight(s, "anthropic")

	before := counterValue(t, "lenny_credential_rotation_inflight_ceiling_hit_total",
		map[string]string{"pool": pool, "trigger": "fault_rate_limited"})

	errc := make(chan error, 1)
	go func() {
		_, err := s.RotateCredentials(context.Background(), rotateRequest("l-new", "fault_rate_limited"))
		errc <- err
	}()

	// The ceiling fires despite the in-flight request never draining, so the
	// runtime sees credentials_rotated. The runtime then acknowledges so the
	// RPC completes cleanly on the rotated path.
	got := peer.read()
	if got.Type != "credentials_rotated" || got.LeaseID != "l-new" {
		t.Fatalf("runtime saw %+v, want credentials_rotated for lease l-new", got)
	}
	peer.send(rotationFrame{Type: "credentials_acknowledged", LeaseID: "l-new", Provider: "anthropic"})
	if err := <-errc; err != nil {
		t.Fatalf("RotateCredentials at ceiling: %v", err)
	}

	if after := counterValue(t, "lenny_credential_rotation_inflight_ceiling_hit_total",
		map[string]string{"pool": pool, "trigger": "fault_rate_limited"}); after != before+1 {
		t.Errorf("ceiling counter = %v, want %v", after, before+1)
	}

	if len(audit.hits) != 1 {
		t.Fatalf("credential.rotation_ceiling_hit audit events = %d, want 1", len(audit.hits))
	}
	h := audit.hits[0]
	if h.Trigger != "fault_rate_limited" {
		t.Errorf("audit trigger = %q, want fault_rate_limited", h.Trigger)
	}
	if h.Provider != "anthropic" || h.LeaseID != "l-new" || h.Pool != pool {
		t.Errorf("audit event identity = %+v, want provider anthropic lease l-new pool %s", h, pool)
	}
	// The counter never drained, so the outstanding in-flight count recorded
	// at the ceiling is the withheld request (§4.9.2 outstanding_inflight_count).
	if h.OutstandingInflight != 1 {
		t.Errorf("audit outstanding_inflight = %d, want 1", h.OutstandingInflight)
	}
	// elapsed_seconds is the wall-clock the gate held; it is at least the
	// configured ceiling (§4.9.2 "always >= 300" scaled to this ceiling).
	if h.ElapsedSeconds < s.RotationInflightCeiling.Seconds() {
		t.Errorf("audit elapsed_seconds = %v, want >= ceiling %v", h.ElapsedSeconds, s.RotationInflightCeiling.Seconds())
	}
}

// TestProactiveRenewalRotationWaitsUnboundedForWithheldRequest pins the §4.7
// ceiling exemption: a proactive_renewal rotation retains the unbounded
// in-flight wait because the old credential is still valid. Even with a
// short ceiling configured, the adapter must NOT send credentials_rotated
// while the withheld request is in flight; it releases only once the runtime
// completes, and it records no ceiling hit.
//
// spec: §4.7 (Full-level credential rotation protocol — "The ceiling does
// NOT apply to rotationTrigger: proactive_renewal, which retains the
// unbounded wait"), §4.9 (only proactive_renewal carries an unbounded wait)
//
// diagnosis: A failure here means a scheduled proactive renewal imposed a
// timeout on an in-flight request whose old credential is still valid,
// risking a false auth failure on an otherwise successful request — or it
// incorrectly recorded a ceiling hit for a non-fault rotation.
func TestProactiveRenewalRotationWaitsUnboundedForWithheldRequest_spec_4_7(t *testing.T) {
	const pool = "rotation-chaos-proactive"
	s, socketPath, audit := startAdapter(t, pool)
	// A short ceiling that MUST be ignored for proactive_renewal.
	s.RotationInflightCeiling = 150 * time.Millisecond
	s.CredentialsAckTimeout = 5 * time.Second

	peer := dialRuntimePeer(t, socketPath)
	if !s.Lifecycle.WaitHandshake(context.Background(), 2*time.Second) {
		t.Fatal("lifecycle handshake did not complete")
	}
	peer.startWithheldInflight(s, "anthropic")

	before := counterValue(t, "lenny_credential_rotation_inflight_ceiling_hit_total",
		map[string]string{"pool": pool, "trigger": "proactive_renewal"})

	errc := make(chan error, 1)
	go func() {
		_, err := s.RotateCredentials(context.Background(), rotateRequest("l-new", "proactive_renewal"))
		errc <- err
	}()

	// The gate holds well past the configured ceiling: no credentials_rotated
	// frame arrives while the request is in flight.
	peer.expectSilence(10 * s.RotationInflightCeiling)

	// Completing the in-flight request drains the gate the natural way.
	peer.send(rotationFrame{Type: "llm_request_completed", Provider: "anthropic", RequestID: "r1", Status: "ok"})
	got := peer.read()
	if got.Type != "credentials_rotated" || got.LeaseID != "l-new" {
		t.Fatalf("runtime saw %+v, want credentials_rotated after natural drain", got)
	}
	peer.send(rotationFrame{Type: "credentials_acknowledged", LeaseID: "l-new", Provider: "anthropic"})
	if err := <-errc; err != nil {
		t.Fatalf("RotateCredentials (proactive_renewal): %v", err)
	}

	if after := counterValue(t, "lenny_credential_rotation_inflight_ceiling_hit_total",
		map[string]string{"pool": pool, "trigger": "proactive_renewal"}); after != before {
		t.Errorf("proactive_renewal ceiling counter moved: got %v, want %v", after, before)
	}
	if len(audit.hits) != 0 {
		t.Errorf("proactive_renewal emitted %d ceiling audit events, want 0", len(audit.hits))
	}
}

// TestRotationAckTimeoutFallsThroughToStandardPath pins the §4.7
// "credentials_acknowledged timeout": when the runtime receives
// credentials_rotated but never acknowledges within the timeout, the adapter
// returns DeadlineExceeded so the gateway takes the Standard-level path
// (Checkpoint -> terminate -> replace -> AssignCredentials -> Resume), and
// the grace-period metric records the interval the old credential remained
// valid (§4.7 "Old credential grace period").
//
// spec: §4.7 (Full-level credential rotation protocol — credentials_acknowledged
// timeout and Old credential grace period)
//
// diagnosis: A failure here means a runtime that never rebinds to the rotated
// credential can wedge a Full-level rotation indefinitely instead of falling
// back to the checkpoint-and-replace path, or the old-credential grace-period
// duration is not recorded for the release decision.
func TestRotationAckTimeoutFallsThroughToStandardPath_spec_4_7(t *testing.T) {
	const pool = "rotation-chaos-acktimeout"
	s, socketPath, _ := startAdapter(t, pool)
	s.CredentialsAckTimeout = 150 * time.Millisecond

	peer := dialRuntimePeer(t, socketPath)
	if !s.Lifecycle.WaitHandshake(context.Background(), 2*time.Second) {
		t.Fatal("lifecycle handshake did not complete")
	}

	beforeGrace := histogramCount(t, "lenny_credential_rotation_grace_period_seconds",
		map[string]string{"pool": pool, "provider": "anthropic"})

	errc := make(chan error, 1)
	go func() {
		_, err := s.RotateCredentials(context.Background(), rotateRequest("l-new", "fault_auth_expired"))
		errc <- err
	}()

	// The adapter sends credentials_rotated; the runtime deliberately never
	// acknowledges, so the ack timeout must elapse.
	got := peer.read()
	if got.Type != "credentials_rotated" {
		t.Fatalf("runtime saw %+v, want credentials_rotated", got)
	}
	err := <-errc
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("RotateCredentials err = %v, want DeadlineExceeded so the gateway takes the standard path", err)
	}

	// The grace-period metric is recorded on the timeout outcome too: the old
	// credential stayed valid until the 60 s (here scaled) timeout elapsed.
	if afterGrace := histogramCount(t, "lenny_credential_rotation_grace_period_seconds",
		map[string]string{"pool": pool, "provider": "anthropic"}); afterGrace != beforeGrace+1 {
		t.Errorf("grace-period histogram count = %d, want %d", afterGrace, beforeGrace+1)
	}
}

// counterValue reads the current value of the named counter with the exact
// label set from the default Prometheus registry, or 0 if absent. The
// adapter registers its §4.7 rotation metrics on prometheus.DefaultRegisterer.
func counterValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	m := findMetric(t, name, labels)
	if m == nil {
		return 0
	}
	return m.GetCounter().GetValue()
}

// histogramCount reads the sample count of the named histogram with the
// exact label set, or 0 if absent.
func histogramCount(t *testing.T, name string, labels map[string]string) uint64 {
	t.Helper()
	m := findMetric(t, name, labels)
	if m == nil {
		return 0
	}
	return m.GetHistogram().GetSampleCount()
}

// findMetric gathers the default registry and returns the metric whose name
// and label set match exactly, or nil.
func findMetric(t *testing.T, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	fams, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, f := range fams {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			if labelsMatch(m, labels) {
				return m
			}
		}
	}
	return nil
}

func labelsMatch(m *dto.Metric, want map[string]string) bool {
	got := make(map[string]string, len(m.GetLabel()))
	for _, lp := range m.GetLabel() {
		got[lp.GetName()] = lp.GetValue()
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
