// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/json"
	"log"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// §4.9 direct-mode lease-expiry timer.
//
// In direct delivery mode the runtime holds the real upstream credential
// in its credential file, so the gateway cannot enforce lease expiry on
// the upstream path the way it does for proxy mode. The adapter MUST set
// a local timer for each direct-mode lease's expiresAt; when the timer
// fires without a replacement lease having been delivered via
// RotateCredentials, the adapter deletes that provider's entry from the
// credential file and reports AUTH_EXPIRED on the control channel,
// triggering the standard fallback flow. This caps a long-lived key
// (e.g. anthropic_direct, whose key does not itself expire) at the lease
// boundary. Proxy-mode leases get no timer: the gateway rejects expired
// proxy requests server-side.
//
// spec: §4.9 line 1149.

// expiryTimerHandle is the subset of *time.Timer the expiry tracker
// needs; the test seam swaps in a fake.
type expiryTimerHandle interface {
	Stop() bool
}

// expiryTimer is one armed direct-mode lease-expiry timer. leaseID
// records the lease the timer was armed for so a fired timer can tell
// whether the lease it guarded is still current (not yet replaced by a
// rotation).
type expiryTimer struct {
	leaseID string
	handle  expiryTimerHandle
}

// expiryAfter schedules f to run after d, using the injected test seam
// when set and time.AfterFunc otherwise. A d at or below zero fires
// immediately, which deletes an already-expired direct-mode lease on the
// next acquisition of s.mu.
func (s *Server) expiryAfter(d time.Duration, f func()) expiryTimerHandle {
	if s.ExpiryAfterFunc != nil {
		return s.ExpiryAfterFunc(d, f)
	}
	return time.AfterFunc(d, f)
}

// expiryNow returns the current time through the injected test seam when
// set and time.Now otherwise.
func (s *Server) expiryClockNow() time.Time {
	if s.ExpiryNow != nil {
		return s.ExpiryNow()
	}
	return time.Now()
}

// reconcileExpiryTimers brings the per-provider expiry timers in line
// with the current lease set: it cancels timers for providers no longer
// present and arms or re-arms a timer for each direct-mode lease.
// Callers hold s.mu. It runs after every credential-file write so the
// timer set always matches the file.
func (s *Server) reconcileExpiryTimers(leases map[string]*adapterv1.CredentialLease) {
	if s.expiryTimers == nil {
		s.expiryTimers = map[string]*expiryTimer{}
	}
	for provider := range s.expiryTimers {
		if _, ok := leases[provider]; !ok {
			s.cancelExpiryTimer(provider)
		}
	}
	for provider, lease := range leases {
		s.armExpiryTimer(provider, lease)
	}
}

// armExpiryTimer arms (or re-arms) the direct-mode expiry timer for one
// provider's lease. Only direct-mode leases with a positive expiry get a
// timer; for any other lease (proxy mode, or a missing expiry) it cancels
// a stale timer and returns. An already-armed timer for the same lease ID
// is left untouched so a rotation of a different provider does not reset
// this provider's deadline. Callers hold s.mu.
func (s *Server) armExpiryTimer(provider string, lease *adapterv1.CredentialLease) {
	ms := lease.GetExpiresAtUnixMs()
	if leaseDeliveryMode(lease) != string(directDeliveryMode) || ms <= 0 {
		s.cancelExpiryTimer(provider)
		return
	}
	leaseID := lease.GetLeaseId()
	if existing, ok := s.expiryTimers[provider]; ok {
		if existing.leaseID == leaseID {
			return
		}
		existing.handle.Stop()
	}
	delay := time.UnixMilli(ms).Sub(s.expiryClockNow())
	handle := s.expiryAfter(delay, func() { s.onLeaseExpired(provider, leaseID) })
	s.expiryTimers[provider] = &expiryTimer{leaseID: leaseID, handle: handle}
}

// extendExpiryTimer re-arms the direct-mode expiry timer for one provider
// to a later deadline, without rewriting the credential file and without
// touching s.credLeases. It is the §4.9 Token Service unavailability
// guard's direct-mode enforcement point: advancing the timer advances the
// enforced deadline in lockstep, so the direct-mode key never outlives the
// current lease. If no timer is armed for the provider, or its lease id
// differs from leaseID (the lease was replaced, or is proxy-mode with no
// timer), it is a no-op. The re-armed timer still targets
// onLeaseExpired(provider, leaseID), so a later expiry deletes the
// provider's credential-file entry exactly as before, at the extended
// deadline. Callers hold s.mu.
//
// spec: §4.9 line 1470.
func (s *Server) extendExpiryTimer(provider, leaseID string, newExpiresAt time.Time) {
	existing, ok := s.expiryTimers[provider]
	if !ok || existing.leaseID != leaseID {
		return
	}
	existing.handle.Stop()
	delay := newExpiresAt.Sub(s.expiryClockNow())
	handle := s.expiryAfter(delay, func() { s.onLeaseExpired(provider, leaseID) })
	s.expiryTimers[provider] = &expiryTimer{leaseID: leaseID, handle: handle}
}

// cancelExpiryTimer stops and forgets the expiry timer for provider, if
// one is armed. Callers hold s.mu.
func (s *Server) cancelExpiryTimer(provider string) {
	if t, ok := s.expiryTimers[provider]; ok {
		t.handle.Stop()
		delete(s.expiryTimers, provider)
	}
}

// cancelAllExpiryTimers stops every armed expiry timer, used when the pod
// is released to idle so a stale lease cannot fire AUTH_EXPIRED against a
// session that has already ended. Callers hold s.mu.
func (s *Server) cancelAllExpiryTimers() {
	for provider := range s.expiryTimers {
		s.cancelExpiryTimer(provider)
	}
}

// onLeaseExpired runs when a direct-mode lease's expiry timer fires. If
// the lease the timer guarded is still the current one for its provider
// (no replacement was delivered via RotateCredentials), it removes the
// provider's entry from the credential file and reports AUTH_EXPIRED so
// the gateway runs the standard fallback flow. A timer whose lease was
// already replaced or revoked is a no-op.
//
// spec: §4.9 line 1149.
func (s *Server) onLeaseExpired(provider, leaseID string) {
	s.mu.Lock()
	t, ok := s.expiryTimers[provider]
	if !ok || t.leaseID != leaseID {
		// The lease was replaced by a rotation or revoked before the
		// timer fired; the replacement carries its own timer.
		s.mu.Unlock()
		return
	}
	delete(s.expiryTimers, provider)
	leases := s.cloneCredentialLeases()
	delete(leases, provider)
	writeErr := s.writeCredentialFile(leases)
	if writeErr == nil {
		s.credLeases = leases
	}
	s.mu.Unlock()

	if writeErr != nil {
		// The deletion is the security-critical half (the key must not
		// outlive the lease), but an I/O failure must not swallow the
		// fallback signal: report AUTH_EXPIRED regardless and log loudly.
		log.Printf("lenny-adapter: credential expiry could not rewrite credential file for provider %s lease %s: %v",
			provider, leaseID, writeErr)
	}
	s.EmitAuthExpired(provider, leaseID)
}

// directDeliveryMode is the §4.9 deliveryMode value that arms the
// adapter expiry timer. It matches credential.DeliveryDirect; the
// adapter reads it from the lease payload rather than importing the
// gateway credential package.
const directDeliveryMode = "direct"

// leaseDeliveryMode reads the §4.9 deliveryMode out of a credential
// lease's opaque payload. The gateway encodes the payload as
// {"deliveryMode": "...", "materializedConfig": {...}} (see
// credassign.ProtoLease); the adapter inspects only the discriminator to
// decide whether to arm an expiry timer. A missing or malformed payload
// yields the empty string, which leaves the lease without a timer.
func leaseDeliveryMode(lease *adapterv1.CredentialLease) string {
	payload := lease.GetPayload()
	if len(payload) == 0 {
		return ""
	}
	var doc struct {
		DeliveryMode string `json:"deliveryMode"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return ""
	}
	return doc.DeliveryMode
}
