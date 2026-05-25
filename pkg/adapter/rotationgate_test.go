// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// rotationGateServer wires a Server to a freshly handshaken lifecycle
// channel with one assigned credential, ready to exercise the §4.7
// Full-level rotation protocol.
func rotationGateServer(t *testing.T) (*Server, *fakeRuntime) {
	t.Helper()
	lc, fr := startLifecycleChannel(t)
	fr.handshake()
	s := New("rotation-test")
	s.CredentialsDir = t.TempDir()
	s.CheckpointPoolLabel = "test-pool"
	s.Lifecycle = lc
	if _, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": {LeaseId: "l-1", Provider: "anthropic", Payload: []byte("{}")},
		},
	}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}
	return s, fr
}

func rotateReq(leaseID, trigger string) *adapterv1.RotateCredentialsRequest {
	return &adapterv1.RotateCredentialsRequest{
		SessionId:       &adapterv1.SessionId{Value: "sess-1"},
		RotationTrigger: trigger,
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic": {LeaseId: leaseID, Provider: "anthropic", Payload: []byte("{}")},
		},
	}
}

// expectNoFrame asserts no adapter→runtime frame arrives within d.
func expectNoFrame(t *testing.T, fr *fakeRuntime, d time.Duration) {
	t.Helper()
	_ = fr.conn.SetReadDeadline(time.Now().Add(d))
	if _, err := readLifecycleFrame(fr.r); err == nil {
		t.Fatal("a lifecycle frame arrived while the in-flight gate should have blocked it")
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("expected read timeout, got %v", err)
	}
	_ = fr.conn.SetReadDeadline(time.Time{})
}

// TestRotationInflightGateBlocksUntilDrained covers §4.7 line 820: the
// adapter waits for in-flight LLM requests to complete before sending
// credentials_rotated. proactive_renewal waits unbounded.
func TestRotationInflightGateBlocksUntilDrained_spec_4_7(t *testing.T) {
	s, fr := rotationGateServer(t)

	fr.write(lifecycleFrame{Type: "llm_request_started", RequestID: "r1", Provider: "anthropic"})
	deadline := time.Now().Add(2 * time.Second)
	for s.Lifecycle.InflightCount("anthropic") != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	errc := make(chan error, 1)
	go func() {
		_, err := s.RotateCredentials(context.Background(), rotateReq("l-2", proactiveRenewalTrigger))
		errc <- err
	}()

	// The gate must hold credentials_rotated while the request is in flight.
	expectNoFrame(t, fr, 200*time.Millisecond)

	// Draining the in-flight request releases the gate.
	fr.write(lifecycleFrame{Type: "llm_request_completed", RequestID: "r1", Provider: "anthropic", Status: "ok"})
	if got := fr.read(); got.Type != "credentials_rotated" || got.LeaseID != "l-2" {
		t.Fatalf("runtime saw %+v, want credentials_rotated lease l-2", got)
	}
	fr.write(lifecycleFrame{Type: "credentials_acknowledged", LeaseID: "l-2", Provider: "anthropic"})
	if err := <-errc; err != nil {
		t.Fatalf("RotateCredentials: %v", err)
	}
}

type recordingAudit struct{ hits []RotationCeilingHit }

func (r *recordingAudit) EmitRotationCeilingHit(_ context.Context, e RotationCeilingHit) {
	r.hits = append(r.hits, e)
}

// TestRotationCeilingHit covers §4.7 line 822: a fault/revocation trigger
// caps the in-flight wait at the ceiling, sends credentials_rotated
// regardless, and records the ceiling counter plus the durable audit
// event.
func TestRotationCeilingHit_spec_4_7(t *testing.T) {
	s, fr := rotationGateServer(t)
	s.RotationInflightCeiling = 100 * time.Millisecond
	audit := &recordingAudit{}
	s.RotationAudit = audit

	fr.write(lifecycleFrame{Type: "llm_request_started", RequestID: "r1", Provider: "anthropic"})
	deadline := time.Now().Add(2 * time.Second)
	for s.Lifecycle.InflightCount("anthropic") != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	before := testutil.ToFloat64(credRotationCeilingHit.WithLabelValues("test-pool", "fault_rate_limited"))

	errc := make(chan error, 1)
	go func() {
		_, err := s.RotateCredentials(context.Background(), rotateReq("l-2", "fault_rate_limited"))
		errc <- err
	}()

	// The ceiling fires even though the request never drains.
	if got := fr.read(); got.Type != "credentials_rotated" {
		t.Fatalf("runtime saw %+v, want credentials_rotated after ceiling", got)
	}
	fr.write(lifecycleFrame{Type: "credentials_acknowledged", LeaseID: "l-2", Provider: "anthropic"})
	if err := <-errc; err != nil {
		t.Fatalf("RotateCredentials: %v", err)
	}

	if after := testutil.ToFloat64(credRotationCeilingHit.WithLabelValues("test-pool", "fault_rate_limited")); after != before+1 {
		t.Errorf("ceiling counter = %v, want %v", after, before+1)
	}
	if len(audit.hits) != 1 {
		t.Fatalf("audit hits = %d, want 1", len(audit.hits))
	}
	h := audit.hits[0]
	if h.Provider != "anthropic" || h.Trigger != "fault_rate_limited" || h.LeaseID != "l-2" || h.OutstandingInflight != 1 {
		t.Errorf("audit event = %+v", h)
	}
}

// TestRotationAckTimeoutFallsThrough covers §4.7 lines 824-826: a missing
// credentials_acknowledged within the timeout returns DeadlineExceeded so
// the gateway takes the standard rotation path, and increments the
// timeout counter.
func TestRotationAckTimeoutFallsThrough_spec_4_7(t *testing.T) {
	s, fr := rotationGateServer(t)
	s.CredentialsAckTimeout = 150 * time.Millisecond
	s.RuntimeName = "claude-code"

	before := testutil.ToFloat64(credRotationTimeout.WithLabelValues("test-pool", "anthropic", "claude-code"))

	errc := make(chan error, 1)
	go func() {
		_, err := s.RotateCredentials(context.Background(), rotateReq("l-2", "fault_auth_expired"))
		errc <- err
	}()

	// Adapter sends credentials_rotated; the runtime never acknowledges.
	if got := fr.read(); got.Type != "credentials_rotated" {
		t.Fatalf("runtime saw %+v, want credentials_rotated", got)
	}
	err := <-errc
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("RotateCredentials err = %v, want DeadlineExceeded", err)
	}
	if after := testutil.ToFloat64(credRotationTimeout.WithLabelValues("test-pool", "anthropic", "claude-code")); after != before+1 {
		t.Errorf("timeout counter = %v, want %v", after, before+1)
	}
}

// TestRotationGateNoInflightPassesThrough confirms the gate is a no-op
// when no LLM request is outstanding (the common case).
func TestRotationGateNoInflightPassesThrough_spec_4_7(t *testing.T) {
	s, fr := rotationGateServer(t)
	errc := make(chan error, 1)
	go func() {
		_, err := s.RotateCredentials(context.Background(), rotateReq("l-2", "fault_rate_limited"))
		errc <- err
	}()
	if got := fr.read(); got.Type != "credentials_rotated" {
		t.Fatalf("runtime saw %+v, want credentials_rotated", got)
	}
	fr.write(lifecycleFrame{Type: "credentials_acknowledged", LeaseID: "l-2", Provider: "anthropic"})
	if err := <-errc; err != nil {
		t.Fatalf("RotateCredentials: %v", err)
	}
}
