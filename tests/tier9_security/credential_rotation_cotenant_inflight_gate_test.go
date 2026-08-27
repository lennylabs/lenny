// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 credential-handling test for the coupling the merged per-session
// rotation leaves between co-tenant sessions on one pod.
//
// Every session is bound to a slot, so the credential file a rotation
// rewrites and the acknowledgement it waits for are per session (§6.1).
// The §4.7 Full-level in-flight completion gate is not: one runtime
// process serves every slot on the pod over the pod's single
// CH-RUNTIMEOPS, and the gate counts outstanding LLM requests pod-wide per
// provider. A co-tenant session's outstanding request for the same
// provider therefore gates a sibling session's rotation and can carry it
// to the revocation ceiling on its own, which §6.1 states in terms.
//
// The limit is recorded here rather than left implicit, because a limit
// nothing asserts is a limit nothing detects: a later change that gives
// the gate a session dimension turns this case red, and the red is the
// notice that the recorded limit no longer holds.
//
// The 300 s ceiling and the 60 s acknowledgement timeout are
// operator-tunable defaults armed with real timers, so the case scales
// them to short real durations. The assertions are on the observable
// contract: which frame the runtime sees and when, the ceiling counter,
// the durable audit event, and the co-tenant's own credential file.

package tier9_security_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/slotlayout"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/rotationgate"
)

// assignRotationSession binds one session to its slot on the pod and
// returns its resolved §6.1 credential file.
func assignRotationSession(t *testing.T, s *adapter.Server, sessionID, provider, leaseID string) string {
	t.Helper()
	if _, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Leases: map[string]*adapterv1.CredentialLease{
			provider: {LeaseId: leaseID, Provider: provider, Payload: []byte("{}")},
		},
	}); err != nil {
		t.Fatalf("AssignCredentials(%s): %v", sessionID, err)
	}
	paths, err := slotlayout.Resolve(slotlayout.Roots{Credentials: s.CredentialsDir}, sessionID)
	if err != nil {
		t.Fatalf("resolve session %s credential path: %v", sessionID, err)
	}
	return paths.CredentialsFile
}

// readCredentialFile returns the session's credential file content.
func readCredentialFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read credential file %s: %v", path, err)
	}
	return string(data)
}

// spec: 6.1 (the in-flight gate and its ceiling remain pod-wide per
// provider while the file rewrite is per session), 4.7 (Full-level
// credential rotation protocol), 4.9 (rotationTrigger ceiling
// applicability)
//
// diagnosis: the pod-wide in-flight gate no longer behaves as §6.1
//
//	records it. Either a co-tenant's outstanding request for the provider
//	stopped gating a sibling session's rotation (the gate gained a session
//	dimension, so the recorded limit and the spec text are stale), or the
//	ceiling forensics went missing: the ceiling counter did not increment,
//	or the durable credential.rotation_ceiling_hit audit event did not name
//	the rotating session with the pod's outstanding count. A missing
//	forensic record means a rotation that shipped a credential while a
//	request was still outstanding leaves no trace. A failure on the
//	co-tenant's file instead means the rewrite crossed the session
//	boundary, which is a credential leak between co-tenants.
func TestCoTenantInflightRequestGatesASiblingSessionRotation_spec_6_1(t *testing.T) {
	const (
		pool     = "rotation-cotenant"
		provider = "anthropic"
		alice    = "sess-alice"
		bob      = "sess-bob"
		trigger  = "fault_rate_limited"
	)
	s, socketPath, audit := rotationgate.NewPodAdapter(t, pool)
	aliceFile := assignRotationSession(t, s, alice, provider, "l-alice-old")
	bobFile := assignRotationSession(t, s, bob, provider, "l-bob-old")
	s.RotationInflightCeiling = 400 * time.Millisecond
	s.CredentialsAckTimeout = 5 * time.Second

	peer := rotationgate.DialPeer(t, socketPath)
	if !s.Lifecycle.WaitHandshake(context.Background(), 2*time.Second) {
		t.Fatal("lifecycle handshake did not complete")
	}
	// The pod's one runtime holds an outstanding request for the provider.
	// The frame carries no session: the gate counts per provider across the
	// whole pod, which is what couples the two sessions.
	peer.StartWithheldInflight(s, provider, "r-bob")

	before := rotationgate.CounterValue(t, "lenny_credential_rotation_inflight_ceiling_hit_total",
		map[string]string{"pool": pool, "trigger": trigger})

	errc := make(chan error, 1)
	go func() {
		_, err := s.RotateCredentials(context.Background(), &adapterv1.RotateCredentialsRequest{
			SessionId:       &adapterv1.SessionId{Value: alice},
			RotationTrigger: trigger,
			Leases: map[string]*adapterv1.CredentialLease{
				provider: {LeaseId: "l-alice-new", Provider: provider, Payload: []byte("{}")},
			},
		})
		errc <- err
	}()

	// The co-tenant's request never completes, so alice's rotation waits on
	// the pod-wide counter rather than proceeding on its own session state.
	peer.ExpectSilence(150 * time.Millisecond)

	// At the ceiling the rotation proceeds regardless, addressed to alice's
	// own credential file.
	got := peer.Read()
	if got.Type != "credentials_rotated" || got.LeaseID != "l-alice-new" {
		t.Fatalf("runtime saw %+v, want credentials_rotated for lease l-alice-new", got)
	}
	if got.CredentialsPath != aliceFile {
		t.Errorf("credentials_rotated credentialsPath = %q, want the rotating session's file %q", got.CredentialsPath, aliceFile)
	}
	peer.Send(rotationgate.Frame{Type: "credentials_acknowledged", LeaseID: "l-alice-new", Provider: provider})
	if err := <-errc; err != nil {
		t.Fatalf("RotateCredentials for %s: %v", alice, err)
	}

	if after := rotationgate.CounterValue(t, "lenny_credential_rotation_inflight_ceiling_hit_total",
		map[string]string{"pool": pool, "trigger": trigger}); after != before+1 {
		t.Errorf("ceiling counter = %v, want %v; the co-tenant's outstanding request carried the rotation to the ceiling",
			after, before+1)
	}
	if len(audit.Hits) != 1 {
		t.Fatalf("credential.rotation_ceiling_hit audit events = %d, want 1", len(audit.Hits))
	}
	hit := audit.Hits[0]
	if hit.SessionID != alice {
		t.Errorf("audit session = %q, want the rotating session %q", hit.SessionID, alice)
	}
	if hit.Provider != provider || hit.LeaseID != "l-alice-new" || hit.Pool != pool || hit.Trigger != trigger {
		t.Errorf("audit event identity = %+v, want provider %s lease l-alice-new pool %s trigger %s",
			hit, provider, pool, trigger)
	}
	// The count recorded at the ceiling is the pod's outstanding request,
	// which belongs to the co-tenant rather than to the rotating session.
	if hit.OutstandingInflight != 1 {
		t.Errorf("audit outstanding_inflight_count = %d, want 1 (the co-tenant's withheld request)", hit.OutstandingInflight)
	}

	// The rotation rewrote the rotating session's file alone.
	if !strings.Contains(readCredentialFile(t, aliceFile), "l-alice-new") {
		t.Errorf("%s does not carry the rotated lease l-alice-new", aliceFile)
	}
	bobContent := readCredentialFile(t, bobFile)
	if !strings.Contains(bobContent, "l-bob-old") {
		t.Errorf("%s no longer carries the co-tenant's own lease l-bob-old", bobFile)
	}
	if strings.Contains(bobContent, "l-alice-new") {
		t.Errorf("%s carries the sibling session's rotated lease; the rewrite crossed the session boundary", bobFile)
	}
}
