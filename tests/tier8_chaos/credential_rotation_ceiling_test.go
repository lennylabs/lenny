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
// and the real adapter.RuntimeOps over a real Unix-socket connection
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
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/slotlayout"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/rotationgate"
)

// startAdapter brings up a real adapter.Server bound to a real
// CH-RUNTIMEOPS on a Unix socket, with sessionID bound to a slot on the pod
// and credentials assigned for one direct-mode provider. Every session is
// bound to a slot on every pod, so the assignment binds the named session's
// slot and its credential file lands under that slot's own tree. It returns
// the server, the socket path, the recording audit emitter wired to the
// §4.9.2 EventStore hook, and the session's resolved §6.1 credential file.
//
// spec: §6.1; §6.4.
func startAdapter(t *testing.T, pool, sessionID string) (*adapter.Server, string, *rotationgate.CeilingAudit, string) {
	t.Helper()
	s, socketPath, audit := rotationgate.NewPodAdapter(t, pool)
	if _, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": {LeaseId: "l-old", Provider: "anthropic", Payload: []byte("{}")},
		},
	}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}
	return s, socketPath, audit, assertSlotCredentialFile(t, s, sessionID)
}

// assertSlotCredentialFile asserts the named session's §6.1 credential file
// sits under that session's own slot tree,
// `<CredentialsDir>/slots/<sessionId>/credentials.json`, and that no
// pod-global credential file was written beside it. The per-slot tree is the
// only layout, so the per-session path is the one place an assignment or a
// rotation lands, on a pod of any concurrency. It returns the resolved path.
//
// spec: §6.1; §6.4.
func assertSlotCredentialFile(t *testing.T, s *adapter.Server, sessionID string) string {
	t.Helper()
	paths, err := slotlayout.Resolve(slotlayout.Roots{Credentials: s.CredentialsDir}, sessionID)
	if err != nil {
		t.Fatalf("resolve session %s credential path: %v", sessionID, err)
	}
	if _, err := os.Stat(paths.CredentialsFile); err != nil {
		t.Fatalf("session %s has no credential file at its slot path %s: %v",
			sessionID, paths.CredentialsFile, err)
	}
	podGlobal := filepath.Join(s.CredentialsDir, slotlayout.CredentialsFileName)
	if _, err := os.Stat(podGlobal); !os.IsNotExist(err) {
		t.Fatalf("a pod-global credential file exists at %s: the assignment must land under the session's slot tree",
			podGlobal)
	}
	return paths.CredentialsFile
}

// rotateRequest builds a rotation for the named session's bound slot.
func rotateRequest(sessionID, leaseID, trigger string) *adapterv1.RotateCredentialsRequest {
	return &adapterv1.RotateCredentialsRequest{
		SessionId:       &adapterv1.SessionId{Value: sessionID},
		RotationTrigger: trigger,
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": {LeaseId: leaseID, Provider: "anthropic", Payload: []byte("{}")},
		},
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
	const session = "sess-chaos-ceiling"
	s, socketPath, audit, credFile := startAdapter(t, pool, session)
	s.RotationInflightCeiling = 150 * time.Millisecond
	s.CredentialsAckTimeout = 5 * time.Second

	peer := rotationgate.DialPeer(t, socketPath)
	if !s.Lifecycle.WaitHandshake(context.Background(), 2*time.Second) {
		t.Fatal("lifecycle handshake did not complete")
	}
	peer.StartWithheldInflight(s, "anthropic", "r1")

	before := rotationgate.CounterValue(t, "lenny_credential_rotation_inflight_ceiling_hit_total",
		map[string]string{"pool": pool, "trigger": "fault_rate_limited"})

	errc := make(chan error, 1)
	go func() {
		_, err := s.RotateCredentials(context.Background(), rotateRequest(session, "l-new", "fault_rate_limited"))
		errc <- err
	}()

	// The ceiling fires despite the in-flight request never draining, so the
	// runtime sees credentials_rotated. The runtime then acknowledges so the
	// RPC completes cleanly on the rotated path.
	got := peer.Read()
	if got.Type != "credentials_rotated" || got.LeaseID != "l-new" {
		t.Fatalf("runtime saw %+v, want credentials_rotated for lease l-new", got)
	}
	// The frame points the runtime at the rotating session's own slot
	// credential file, so a co-tenant slot's bundle is untouched (§6.1).
	if got.CredentialsPath != credFile {
		t.Errorf("credentials_rotated credentialsPath = %q, want the session's slot file %q", got.CredentialsPath, credFile)
	}
	peer.Send(rotationgate.Frame{Type: "credentials_acknowledged", LeaseID: "l-new", Provider: "anthropic"})
	if err := <-errc; err != nil {
		t.Fatalf("RotateCredentials at ceiling: %v", err)
	}
	// The rotated lease landed in that session's own file rather than in a
	// pod-global one.
	assertCredentialFileHasLease(t, credFile, "l-new")

	if after := rotationgate.CounterValue(t, "lenny_credential_rotation_inflight_ceiling_hit_total",
		map[string]string{"pool": pool, "trigger": "fault_rate_limited"}); after != before+1 {
		t.Errorf("ceiling counter = %v, want %v", after, before+1)
	}

	if len(audit.Hits) != 1 {
		t.Fatalf("credential.rotation_ceiling_hit audit events = %d, want 1", len(audit.Hits))
	}
	h := audit.Hits[0]
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
	const session = "sess-chaos-proactive"
	s, socketPath, audit, credFile := startAdapter(t, pool, session)
	// A short ceiling that MUST be ignored for proactive_renewal.
	s.RotationInflightCeiling = 150 * time.Millisecond
	s.CredentialsAckTimeout = 5 * time.Second

	peer := rotationgate.DialPeer(t, socketPath)
	if !s.Lifecycle.WaitHandshake(context.Background(), 2*time.Second) {
		t.Fatal("lifecycle handshake did not complete")
	}
	peer.StartWithheldInflight(s, "anthropic", "r1")

	before := rotationgate.CounterValue(t, "lenny_credential_rotation_inflight_ceiling_hit_total",
		map[string]string{"pool": pool, "trigger": "proactive_renewal"})

	errc := make(chan error, 1)
	go func() {
		_, err := s.RotateCredentials(context.Background(), rotateRequest(session, "l-new", "proactive_renewal"))
		errc <- err
	}()

	// The gate holds well past the configured ceiling: no credentials_rotated
	// frame arrives while the request is in flight.
	peer.ExpectSilence(10 * s.RotationInflightCeiling)

	// Completing the in-flight request drains the gate the natural way.
	peer.Send(rotationgate.Frame{Type: "llm_request_completed", Provider: "anthropic", RequestID: "r1", Status: "ok"})
	got := peer.Read()
	if got.Type != "credentials_rotated" || got.LeaseID != "l-new" {
		t.Fatalf("runtime saw %+v, want credentials_rotated after natural drain", got)
	}
	if got.CredentialsPath != credFile {
		t.Errorf("credentials_rotated credentialsPath = %q, want the session's slot file %q", got.CredentialsPath, credFile)
	}
	peer.Send(rotationgate.Frame{Type: "credentials_acknowledged", LeaseID: "l-new", Provider: "anthropic"})
	if err := <-errc; err != nil {
		t.Fatalf("RotateCredentials (proactive_renewal): %v", err)
	}

	if after := rotationgate.CounterValue(t, "lenny_credential_rotation_inflight_ceiling_hit_total",
		map[string]string{"pool": pool, "trigger": "proactive_renewal"}); after != before {
		t.Errorf("proactive_renewal ceiling counter moved: got %v, want %v", after, before)
	}
	if len(audit.Hits) != 0 {
		t.Errorf("proactive_renewal emitted %d ceiling audit events, want 0", len(audit.Hits))
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
	const session = "sess-chaos-acktimeout"
	s, socketPath, _, credFile := startAdapter(t, pool, session)
	s.CredentialsAckTimeout = 150 * time.Millisecond

	peer := rotationgate.DialPeer(t, socketPath)
	if !s.Lifecycle.WaitHandshake(context.Background(), 2*time.Second) {
		t.Fatal("lifecycle handshake did not complete")
	}

	beforeGrace := rotationgate.HistogramCount(t, "lenny_credential_rotation_grace_period_seconds",
		map[string]string{"pool": pool, "provider": "anthropic"})

	errc := make(chan error, 1)
	go func() {
		_, err := s.RotateCredentials(context.Background(), rotateRequest(session, "l-new", "fault_auth_expired"))
		errc <- err
	}()

	// The adapter sends credentials_rotated; the runtime deliberately never
	// acknowledges, so the ack timeout must elapse.
	got := peer.Read()
	if got.Type != "credentials_rotated" {
		t.Fatalf("runtime saw %+v, want credentials_rotated", got)
	}
	if got.CredentialsPath != credFile {
		t.Errorf("credentials_rotated credentialsPath = %q, want the session's slot file %q", got.CredentialsPath, credFile)
	}
	err := <-errc
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("RotateCredentials err = %v, want DeadlineExceeded so the gateway takes the standard path", err)
	}

	// The grace-period metric is recorded on the timeout outcome too: the old
	// credential stayed valid until the 60 s (here scaled) timeout elapsed.
	if afterGrace := rotationgate.HistogramCount(t, "lenny_credential_rotation_grace_period_seconds",
		map[string]string{"pool": pool, "provider": "anthropic"}); afterGrace != beforeGrace+1 {
		t.Errorf("grace-period histogram count = %d, want %d", afterGrace, beforeGrace+1)
	}
}

// assertCredentialFileHasLease asserts the session's own slot credential
// file carries the named lease id, which is how a rotation is observed to
// have rewritten that session's bundle rather than a pod-global one.
// spec: §6.1.
func assertCredentialFileHasLease(t *testing.T, credFile, leaseID string) {
	t.Helper()
	data, err := os.ReadFile(credFile)
	if err != nil {
		t.Fatalf("read session credential file %s: %v", credFile, err)
	}
	if !strings.Contains(string(data), leaseID) {
		t.Errorf("session credential file %s does not carry the rotated lease %s", credFile, leaseID)
	}
}
