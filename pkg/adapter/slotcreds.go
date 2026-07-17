// SPDX-License-Identifier: MIT

package adapter

import (
	"log"
	"sort"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// assignCredentialsSlot writes the §6.1 per-slot credential file
// /run/lenny/slots/{slotId}/credentials.json from the slot's independent
// lease set, leaving sibling slots' files untouched. The slot must
// already hold the session (StartSession ran first); credentials may also
// be assigned before start during the §4.7 bind sequence, in which case
// the slot tree is created here. spec: §6.1 line 28.
func (s *Server) assignCredentialsSlot(sessionID, slotID string, reqLeases map[string]*adapterv1.CredentialLease) (*adapterv1.AssignCredentialsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.ensureSlotStateLocked(slotID)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "resolve slot %s: %v", slotID, err)
	}
	// The §4.7 bind sequence assigns credentials before StartSession, so a
	// not-yet-started slot records the session here; a started slot must
	// match.
	if st.sessionID == "" {
		st.sessionID = sessionID
	} else if st.sessionID != sessionID {
		return nil, status.Errorf(codes.FailedPrecondition,
			"slot %s credentials are already assigned to session %s", slotID, st.sessionID)
	}

	leases := make(map[string]*adapterv1.CredentialLease, len(reqLeases))
	for provider, lease := range reqLeases {
		leases[provider] = lease
	}
	if err := writeSlotCredentialFile(st.paths.CredentialsDir, leases); err != nil {
		return nil, err
	}
	st.creds = leases
	// §4.9 line 1149: arm a per-slot expiry timer for each direct-mode lease,
	// independent of sibling slots.
	s.reconcileSlotExpiryTimersLocked(st, slotID, leases)
	return &adapterv1.AssignCredentialsResponse{}, nil
}

// rotateCredentialsSlot replaces the named providers' leases in the
// slot's per-slot file. Per §6.1 the rotation is independent: a sibling
// slot's file and in-flight requests are unaffected. The §4.7 Full-level
// in-flight gate / ceiling / ack protocol is the runtime's concern over
// its lifecycle channel; the concurrent slot's runtime owns its own
// channel, so this path performs the file rewrite and lets the slot's
// runtime rebind. spec: §6.1 line 28; §4.7 lines 816-829.
func (s *Server) rotateCredentialsSlot(sessionID, slotID string, reqLeases map[string]*adapterv1.CredentialLease) (*adapterv1.RotateCredentialsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slotStateLocked(slotID)
	if !ok {
		return nil, status.Errorf(codes.FailedPrecondition, "slot %s has no assigned credentials", slotID)
	}
	if st.sessionID != sessionID {
		return nil, status.Errorf(codes.NotFound,
			"slot %s credentials are assigned to session %s, not %s", slotID, st.sessionID, sessionID)
	}
	leases := cloneLeases(st.creds)
	for provider, lease := range reqLeases {
		leases[provider] = lease
	}
	if err := writeSlotCredentialFile(st.paths.CredentialsDir, leases); err != nil {
		return nil, err
	}
	st.creds = leases
	s.reconcileSlotExpiryTimersLocked(st, slotID, leases)
	return &adapterv1.RotateCredentialsResponse{}, nil
}

// extendCredentialLeaseSlot re-arms the slot's own §4.9 direct-mode
// expiry timer for one provider to a later deadline, without rewriting the
// slot credential file. It is the per-slot analogue of extendExpiryTimer:
// the §4.9 Token Service unavailability guard extends only this slot's
// enforced deadline, leaving sibling slots' timers untouched. If the slot
// has no timer for the provider, or its lease id differs from leaseID, it
// is a no-op. The re-armed timer still targets onSlotLeaseExpired, so a
// later expiry deletes the provider's entry at the extended deadline.
// spec: §6.1 line 28; §4.9 line 1470.
func (s *Server) extendCredentialLeaseSlot(slotID, provider, leaseID string, newExpiresAt time.Time) (*adapterv1.ExtendCredentialLeaseResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slotStateLocked(slotID)
	if !ok {
		return &adapterv1.ExtendCredentialLeaseResponse{}, nil
	}
	existing, ok := st.timers[provider]
	if !ok || existing.leaseID != leaseID {
		return &adapterv1.ExtendCredentialLeaseResponse{}, nil
	}
	existing.handle.Stop()
	delay := newExpiresAt.Sub(s.expiryClockNow())
	handle := s.expiryAfter(delay, func() { s.onSlotLeaseExpired(slotID, provider, leaseID) })
	st.timers[provider] = &expiryTimer{leaseID: leaseID, handle: handle}
	return &adapterv1.ExtendCredentialLeaseResponse{}, nil
}

// revokeCredentialsSlot removes the named providers from the slot's
// per-slot file. spec: §6.1 line 28.
func (s *Server) revokeCredentialsSlot(sessionID, slotID string, providers []string) (*adapterv1.RevokeCredentialsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slotStateLocked(slotID)
	if !ok {
		return nil, status.Errorf(codes.FailedPrecondition, "slot %s has no assigned credentials", slotID)
	}
	if st.sessionID != sessionID {
		return nil, status.Errorf(codes.NotFound,
			"slot %s credentials are assigned to session %s, not %s", slotID, st.sessionID, sessionID)
	}
	leases := cloneLeases(st.creds)
	for _, provider := range providers {
		delete(leases, provider)
	}
	if err := writeSlotCredentialFile(st.paths.CredentialsDir, leases); err != nil {
		return nil, err
	}
	st.creds = leases
	s.reconcileSlotExpiryTimersLocked(st, slotID, leases)
	return &adapterv1.RevokeCredentialsResponse{}, nil
}

// cloneLeases returns a shallow copy of a lease map so a rewrite is
// computed off-line and committed only when the file write succeeds.
func cloneLeases(in map[string]*adapterv1.CredentialLease) map[string]*adapterv1.CredentialLease {
	out := make(map[string]*adapterv1.CredentialLease, len(in))
	for provider, lease := range in {
		out[provider] = lease
	}
	return out
}

// writeSlotCredentialFile materializes the slot's leases to its per-slot
// credential file, ordered by provider for a deterministic file. The
// credential directory must already exist (ensureSlotStateLocked created
// it). spec: §6.1 line 28.
func writeSlotCredentialFile(dir string, leases map[string]*adapterv1.CredentialLease) error {
	if dir == "" {
		return status.Error(codes.FailedPrecondition,
			"adapter is not configured with a credentials directory")
	}
	providers := make([]string, 0, len(leases))
	for provider := range leases {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	ordered := make([]*adapterv1.CredentialLease, 0, len(providers))
	for _, provider := range providers {
		ordered = append(ordered, leases[provider])
	}
	if err := credfile.Write(dir, ordered); err != nil {
		return status.Errorf(codes.Internal, "write slot credential file: %v", err)
	}
	return nil
}

// reconcileSlotExpiryTimersLocked brings the slot's §4.9 direct-mode
// expiry timers in line with its lease set, independent of sibling slots
// and the single-slot set. Callers hold s.mu. spec: §4.9 line 1149.
func (s *Server) reconcileSlotExpiryTimersLocked(st *slotState, slotID string, leases map[string]*adapterv1.CredentialLease) {
	if st.timers == nil {
		st.timers = map[string]*expiryTimer{}
	}
	for provider := range st.timers {
		if _, ok := leases[provider]; !ok {
			s.cancelSlotExpiryTimerLocked(st, provider)
		}
	}
	for provider, lease := range leases {
		s.armSlotExpiryTimerLocked(st, slotID, provider, lease)
	}
}

// armSlotExpiryTimerLocked arms (or re-arms) the slot's direct-mode
// expiry timer for one provider. Only direct-mode leases with a positive
// expiry get a timer. Callers hold s.mu. spec: §4.9 line 1149.
func (s *Server) armSlotExpiryTimerLocked(st *slotState, slotID, provider string, lease *adapterv1.CredentialLease) {
	ms := lease.GetExpiresAtUnixMs()
	if leaseDeliveryMode(lease) != string(directDeliveryMode) || ms <= 0 {
		s.cancelSlotExpiryTimerLocked(st, provider)
		return
	}
	leaseID := lease.GetLeaseId()
	if existing, ok := st.timers[provider]; ok {
		if existing.leaseID == leaseID {
			return
		}
		existing.handle.Stop()
	}
	delay := time.UnixMilli(ms).Sub(s.expiryClockNow())
	handle := s.expiryAfter(delay, func() { s.onSlotLeaseExpired(slotID, provider, leaseID) })
	st.timers[provider] = &expiryTimer{leaseID: leaseID, handle: handle}
}

// cancelSlotExpiryTimerLocked stops and forgets the slot's expiry timer
// for provider. Callers hold s.mu.
func (s *Server) cancelSlotExpiryTimerLocked(st *slotState, provider string) {
	if t, ok := st.timers[provider]; ok {
		t.handle.Stop()
		delete(st.timers, provider)
	}
}

// onSlotLeaseExpired fires when a slot's direct-mode lease expires. If the
// lease the timer guarded is still current it deletes the provider's entry
// from the slot's credential file and reports AUTH_EXPIRED, exactly as the
// single-slot path does but scoped to the slot so sibling slots are
// unaffected. spec: §4.9 line 1149.
func (s *Server) onSlotLeaseExpired(slotID, provider, leaseID string) {
	s.mu.Lock()
	st, ok := s.slotStateLocked(slotID)
	if !ok {
		s.mu.Unlock()
		return
	}
	t, ok := st.timers[provider]
	if !ok || t.leaseID != leaseID {
		s.mu.Unlock()
		return
	}
	delete(st.timers, provider)
	leases := cloneLeases(st.creds)
	delete(leases, provider)
	writeErr := writeSlotCredentialFile(st.paths.CredentialsDir, leases)
	if writeErr == nil {
		st.creds = leases
	}
	s.mu.Unlock()

	if writeErr != nil {
		log.Printf("lenny-adapter: slot %s credential expiry could not rewrite credential file for provider %s lease %s: %v",
			slotID, provider, leaseID, writeErr)
	}
	s.EmitAuthExpired(provider, leaseID)
}
