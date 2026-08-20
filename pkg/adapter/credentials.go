// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// §4.7 Full-level rotation protocol constants.
const (
	// defaultRotationInflightCeiling caps the in-flight gate wait for any
	// rotationTrigger other than proactive_renewal (spec line 822).
	defaultRotationInflightCeiling = 300 * time.Second
	// defaultCredentialsAckTimeout bounds the credentials_acknowledged
	// wait before falling through to the standard rotation path
	// (spec line 824).
	defaultCredentialsAckTimeout = 60 * time.Second
	// rotationGatePollInterval is how often the in-flight gate re-checks
	// the per-provider counter while waiting for it to drain.
	rotationGatePollInterval = 50 * time.Millisecond
	// inflightWaitLongThreshold is the spec line 820 warning threshold
	// above which the adapter logs credential_rotation_inflight_wait_long.
	inflightWaitLongThreshold = 60 * time.Second
	// proactiveRenewalTrigger keeps the unbounded in-flight wait: the old
	// credential is still valid, so a timeout would risk a false auth
	// failure (spec line 822).
	proactiveRenewalTrigger = "proactive_renewal"
)

// RotationCeilingHit carries the §4.7 / §4.9.2
// credential.rotation_ceiling_hit forensic fields to the audit emitter.
type RotationCeilingHit struct {
	SessionID           string
	LeaseID             string
	Provider            string
	Pool                string
	Trigger             string
	OutstandingInflight int
	ElapsedSeconds      float64
}

// RotationAuditEmitter records the durable credential.rotation_ceiling_hit
// audit event (§4.9.2) when the Full-level in-flight gate hits its 300s
// ceiling. The gateway wires it to the EventStore; the dev-mode adapter
// leaves it nil.
type RotationAuditEmitter interface {
	EmitRotationCeilingHit(ctx context.Context, e RotationCeilingHit)
}

// AssignCredentials materializes the session's per-provider credential
// leases into the pod's credential file before the runtime starts
// (§4.7 item 4). The leases replace any previously assigned set. The
// request payload carries credential material; per §4.7 item 6 it must
// never be written to access logs or telemetry.
func (s *Server) AssignCredentials(_ context.Context, req *adapterv1.AssignCredentialsRequest) (*adapterv1.AssignCredentialsResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "AssignCredentials requires a session id")
	}
	// spec: §6.1 — the assignment writes the session's own
	// /run/lenny/slots/{sessionId}/credentials.json, so a co-tenant's
	// credential file is untouched. Every session is bound to a slot on
	// every pod, so this is the only assignment path.
	return s.assignCredentialsSlot(sessionID, sessionID, req.GetLeases())
}

// RotateCredentials replaces the leases for the providers named in the
// request and rewrites the credential file (§4.7 item 4). Leases for
// providers not named in the request are retained. The request payload
// carries credential material; per §4.7 item 6 it must never be
// written to access logs or telemetry.
func (s *Server) RotateCredentials(ctx context.Context, req *adapterv1.RotateCredentialsRequest) (*adapterv1.RotateCredentialsResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "RotateCredentials requires a session id")
	}
	// spec: §6.1 — the rotation rewrites the session's own credential
	// file, so a co-tenant's in-flight requests are unaffected.
	resp, err := s.rotateCredentialsSlot(sessionID, sessionID, req.GetLeases())
	if err != nil {
		return nil, err
	}
	rotated := make([]rotatedLease, 0, len(req.GetLeases()))
	for provider, lease := range req.GetLeases() {
		rotated = append(rotated, rotatedLease{provider: provider, leaseID: lease.GetLeaseId()})
	}

	// §4.7: a Full-level runtime rebinds the rotated credential in place.
	// The adapter runs the strict in-flight-gate / ceiling / ack-timeout
	// rotation protocol per provider against that session's own file.
	if s.Lifecycle != nil && s.Lifecycle.Supports("credential_rotation") {
		path, perr := s.sessionCredentialFile(sessionID)
		if perr != nil {
			return nil, perr
		}
		trigger := req.GetRotationTrigger()
		for _, r := range rotated {
			if err := s.rotateProviderFull(ctx, sessionID, r, path, trigger); err != nil {
				return nil, err
			}
		}
	}
	return resp, nil
}

// sessionCredentialFile returns the absolute path of the session's own
// §6.1 credential file. spec: §6.1.
func (s *Server) sessionCredentialFile(sessionID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slotStateLocked(sessionID)
	if !ok || st.paths.CredentialsDir == "" {
		return "", status.Errorf(codes.FailedPrecondition,
			"session %s has no credential directory on this pod", sessionID)
	}
	return filepath.Join(st.paths.CredentialsDir, credfile.FileName), nil
}

// ExtendCredentialLease re-arms a still-valid direct-mode credential
// lease's expiry timer to a later deadline without delivering credential
// material and without running the §4.7 rebind handshake. It is the §4.9
// Token Service unavailability guard's adapter-side surface: when the
// Token Service circuit breaker is open and the lease has not yet expired,
// the gateway extends the enforced deadline from the lease record it
// already holds, so no Token Service call is made. It touches neither the
// credential file nor s.credLeases.
//
// spec: §4.9.
func (s *Server) ExtendCredentialLease(_ context.Context, req *adapterv1.ExtendCredentialLeaseRequest) (*adapterv1.ExtendCredentialLeaseResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "ExtendCredentialLease requires a session id")
	}
	newExpiresAt := time.UnixMilli(req.GetExpiresAtUnixMs())
	// spec: §6.1 — the extension re-arms only this session's own timer,
	// so a co-tenant's deadline is untouched.
	return s.extendCredentialLeaseSlot(sessionID, req.GetProvider(), req.GetLeaseId(), newExpiresAt)
}

// rotateProviderFull runs the §4.7 Full-level rotation protocol for one provider: the in-flight LLM-request completion gate
// with the 300s revocation ceiling (line 822), the credentials_rotated
// send, and the 60s credentials_acknowledged timeout that falls through
// to the standard rotation path (line 824). It records the four §4.7 metrics and the grace-period interval.
//
// spec: §4.7
func (s *Server) rotateProviderFull(ctx context.Context, sessionID string, r rotatedLease, credentialsPath, trigger string) error {
	pool := s.CheckpointPoolLabel

	// §4.7: wait for in-flight LLM requests to drain before
	// signalling rotation, so no request reaches the provider with the
	// old credential after the rotation decision.
	elapsed, ceilingHit, err := s.awaitInflightGate(ctx, r.provider, trigger)
	if err != nil {
		return err
	}
	observeRotationInflightWait(pool, r.provider, elapsed.Seconds())
	if elapsed >= inflightWaitLongThreshold {
		log.Printf("lenny-adapter: credential_rotation_inflight_wait_long session=%s lease=%s provider=%s elapsed=%s",
			sessionID, r.leaseID, r.provider, elapsed)
	}
	if ceilingHit {
		// §4.7: send credentials_rotated regardless and record the
		// compromise-indicator signals — the counter, alert, and durable
		// audit event are the forensic record of every ceiling-hit rotation.
		incRotationCeilingHit(pool, ceilingTriggerLabel(trigger))
		if s.RotationAudit != nil {
			s.RotationAudit.EmitRotationCeilingHit(ctx, RotationCeilingHit{
				SessionID:           sessionID,
				LeaseID:             r.leaseID,
				Provider:            r.provider,
				Pool:                pool,
				Trigger:             ceilingTriggerLabel(trigger),
				OutstandingInflight: s.Lifecycle.InflightCount(r.provider),
				ElapsedSeconds:      elapsed.Seconds(),
			})
		}
	}

	// §4.7: bound the credentials_acknowledged wait at 60s. The
	// old credential is released either on ack or on timeout (line 829),
	// so the grace interval is recorded on both outcomes.
	ackTimeout := s.CredentialsAckTimeout
	if ackTimeout <= 0 {
		ackTimeout = defaultCredentialsAckTimeout
	}
	ackCtx, cancel := context.WithTimeout(ctx, ackTimeout)
	defer cancel()
	sentAt := time.Now()
	err = s.Lifecycle.RotateCredentials(ackCtx, r.provider, credentialsPath, r.leaseID)
	observeRotationGracePeriod(pool, r.provider, time.Since(sentAt).Seconds())
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
		// §4.7: the runtime did not acknowledge within 60s.
		// Emit the timeout signals and hand back to the gateway so it can
		// take the Standard-level path (Checkpoint → terminate → replace →
		// AssignCredentials → Resume).
		log.Printf("lenny-adapter: credential_rotation_timeout session=%s lease=%s provider=%s elapsed=%s",
			sessionID, r.leaseID, r.provider, ackTimeout)
		incRotationTimeout(pool, r.provider, s.RuntimeName)
		return status.Errorf(codes.DeadlineExceeded,
			"credentials_acknowledged timeout for provider %s; gateway must take the standard rotation path", r.provider)
	default:
		// §4.7: the runtime could not rebind to the rotated
		// credential (or the caller cancelled), so surface LEASE_REJECTED
		// to the gateway before failing the RPC.
		s.EmitLeaseRejected(r.provider, r.leaseID, err.Error())
		return status.Errorf(codes.Internal, "lifecycle credential rotation: %v", err)
	}
}

// awaitInflightGate blocks until the per-provider in-flight LLM-request
// counter reaches zero (spec line 820). For any trigger other than
// proactive_renewal the wait is capped at the §4.7 ceiling; on
// ceiling hit it returns ceilingHit=true so the caller sends
// credentials_rotated regardless. proactive_renewal waits unbounded
// (only the caller's context cancels it). It returns the elapsed wait.
func (s *Server) awaitInflightGate(ctx context.Context, provider, trigger string) (time.Duration, bool, error) {
	start := time.Now()
	if s.Lifecycle.InflightCount(provider) == 0 {
		return 0, false, nil
	}

	var ceiling <-chan time.Time
	if trigger != proactiveRenewalTrigger {
		d := s.RotationInflightCeiling
		if d <= 0 {
			d = defaultRotationInflightCeiling
		}
		t := time.NewTimer(d)
		defer t.Stop()
		ceiling = t.C
	}

	ticker := time.NewTicker(rotationGatePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return time.Since(start), false, ctx.Err()
		case <-ceiling:
			return time.Since(start), true, nil
		case <-ticker.C:
			if s.Lifecycle.InflightCount(provider) == 0 {
				return time.Since(start), false, nil
			}
		}
	}
}

// ceilingTriggerLabel normalizes an empty trigger to the fail-closed
// fault label for the ceiling counter and audit event, so a rotation
// sent without an explicit trigger is still attributed as a capped one.
func ceilingTriggerLabel(trigger string) string {
	if trigger == "" {
		return "unspecified"
	}
	return trigger
}

// rotatedLease names one provider's credential lease rotated by a
// RotateCredentials call, for the CH-RUNTIMEOPS notification.
type rotatedLease struct {
	provider string
	leaseID  string
}

// RevokeCredentials removes the named providers' leases and rewrites
// the credential file without them (§4.7 item 4).
func (s *Server) RevokeCredentials(_ context.Context, req *adapterv1.RevokeCredentialsRequest) (*adapterv1.RevokeCredentialsResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "RevokeCredentials requires a session id")
	}
	// spec: §6.1 — the revoke drops the named providers from the
	// session's own credential file only.
	return s.revokeCredentialsSlot(sessionID, sessionID, req.GetProviders())
}
