// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/observability/tracing"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// slotResolve carries what a caller asserts about the entry it is resolving.
// Every field is a caller assertion rather than a discovered fact, and the
// rule 2-through-7 predicate in ensureSlotStateLocked is the only reader.
//
// spec: §4.7.1 (role and gateway RPC contract), rules 2 through 7
type slotResolve struct {
	// bindAttempt is the caller's per-attempt token. The empty string means
	// the caller asserts no attempt identity, which a §7.4 mid-session
	// request, StartSession and ConfigureWorkspace all do.
	bindAttempt string
	// allowCreate is false for a mid-session RPC, which must resolve an
	// entry that already exists and must never create one.
	allowCreate bool
	// allowStarted is true for a mid-session RPC and for
	// ConfigureWorkspace's idempotent repeat, and false everywhere else.
	allowStarted bool
}

// slotRefusal is an admission refusal the resolve raises: the reclaim
// hold, the mid-session-create refusal, the attempt identity refusal or the
// started-session refusal. It carries the gRPC status the handler answers with, so the
// gRPC server reads the code and the detail through GRPCStatus, and the
// span category the refusal is recorded under. A distinct type lets the
// resolve sites pass a refusal through unchanged while wrapping every other
// resolve failure. spec: §4.7.1 (role and gateway RPC contract).
type slotRefusal struct {
	st       *status.Status
	category tracing.ErrorCategory
}

// Error returns the status message.
func (r *slotRefusal) Error() string { return r.st.Err().Error() }

// GRPCStatus returns the status the handler answers the refusal with.
func (r *slotRefusal) GRPCStatus() *status.Status { return r.st }

// errSlotReclaimInProgress is the §5.2 reclaim-hold refusal: the slot's
// identifier is held while the cleanup that reclaims it runs, and a caller
// retries. §15.4 publishes ABORTED as the status it is answered on.
// spec: §5.2 (slot-identifier reclaim hold); §4.7.1 rule 2; §15.4.
var errSlotReclaimInProgress error = &slotRefusal{
	st: status.New(codes.Aborted,
		"slot_reclaim_in_progress: the slot identifier is held while its cleanup runs"),
	category: tracing.CategoryTransient,
}

// isSlotReclaimInProgress reports whether err is the reclaim-hold refusal.
func isSlotReclaimInProgress(err error) bool {
	return errors.Is(err, errSlotReclaimInProgress)
}

// errSlotBindAttemptSuperseded is §4.7.1 rule 5's refusal: the request
// names a bind attempt other than the one the entry is stamped with. The
// message names the slot identifier and never the token, because the token
// is a capability over a live session's teardown.
// spec: §4.7.1 (role and gateway RPC contract), rule 5.
func errSlotBindAttemptSuperseded(slotID string) error {
	return newSlotRefusal(codes.Aborted,
		fmt.Sprintf("slot %s belongs to another bind attempt", slotID),
		adapterv1.Error_ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED,
		adapterv1.Error_CATEGORY_TRANSIENT, true, tracing.CategoryTransient)
}

// errSlotBindAlreadyStarted is §4.7.1 rule 6's refusal: the request is not
// marked mid_session and the entry it resolved carries a started session.
// spec: §4.7.1 (role and gateway RPC contract), rule 6.
func errSlotBindAlreadyStarted(slotID string) error {
	return newSlotRefusal(codes.FailedPrecondition,
		fmt.Sprintf("session %s has already started on this pod", slotID),
		adapterv1.Error_ERROR_CODE_SLOT_BIND_ALREADY_STARTED,
		adapterv1.Error_CATEGORY_PERMANENT, false, tracing.CategoryPermanent)
}

// errSlotMidSessionNoEntry is §4.7.1 rule 3's refusal: a request marked
// mid_session resolved no entry, and a mid-session request never creates
// one. It is answered FAILED_PRECONDITION on the wire, so it is a
// pass-through refusal rather than a resolve failure the site wraps.
// spec: §4.7.1 (role and gateway RPC contract), rule 3.
func errSlotMidSessionNoEntry(slotID string) error {
	return &slotRefusal{
		st: status.Newf(codes.FailedPrecondition,
			"slot %s has no registry entry for a mid-session request", slotID),
		category: tracing.CategoryPermanent,
	}
}

// newSlotRefusal builds a typed refusal carrying an adapterv1.Error
// detail. Retryable is set explicitly because the proto3 default of false
// would contradict a transient category on the wire. A marshalling failure
// degrades to the bare status, so the refusal keeps its code rather than
// being lost.
func newSlotRefusal(code codes.Code, msg string, errCode adapterv1.Error_ErrorCode,
	errCategory adapterv1.Error_Category, retryable bool, span tracing.ErrorCategory,
) error {
	st := status.New(code, msg)
	if withDetail, err := st.WithDetails(&adapterv1.Error{
		Code:      errCode,
		Category:  errCategory,
		Message:   msg,
		Retryable: retryable,
	}); err == nil {
		st = withDetail
	}
	return &slotRefusal{st: st, category: span}
}

// slotResolveError returns a slot-resolve failure in the form the handler
// answers with. A refusal the resolve raised passes through unchanged, so
// rule 5's refusal stays transient and rule 6's stays permanent; every
// other failure is wrapped as InvalidArgument under the site's own prefix,
// as the resolve sites have always wrapped it.
// spec: §4.7.1 (role and gateway RPC contract), rules 2, 5 and 6.
func slotResolveError(err error, prefix string) error {
	var refusal *slotRefusal
	if errors.As(err, &refusal) {
		return refusal
	}
	return status.Errorf(codes.InvalidArgument, "%s: %v", prefix, err)
}

// slotResolveCategory returns the §16.3 span category a slot-resolve
// failure is recorded under: TRANSIENT for the reclaim hold and the
// superseded refusal, which a caller retries, and PERMANENT for the
// already-started refusal and every other failure.
// spec: §16.3 (distributed tracing); §4.7.1 rules 2, 5 and 6.
func slotResolveCategory(err error) tracing.ErrorCategory {
	var refusal *slotRefusal
	if errors.As(err, &refusal) {
		return refusal.category
	}
	return tracing.CategoryPermanent
}

// openReclaimHoldLocked inserts slotID into the reclaim-hold set and
// returns the idempotent function that ends the hold. The caller takes the
// release only once the cleanup that reclaims the slot has completed,
// because §5.2 keeps the identifier held for the life of the pod when the
// cleanup does not complete. Callers hold s.mu; the release takes s.mu
// itself, so it runs after the caller has released the lock.
// spec: §5.2 (slot-identifier reclaim hold).
func (s *Server) openReclaimHoldLocked(slotID string) func() {
	if s.reclaiming == nil {
		s.reclaiming = map[string]struct{}{}
	}
	s.reclaiming[slotID] = struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.reclaiming, slotID)
			s.mu.Unlock()
		})
	}
}

// noHoldRelease is the release a reclaim that removed nothing returns: it
// opened no hold, so there is nothing to end.
func noHoldRelease() {}

// slotGuardLocked returns slotID's per-slot guard channel, creating it on
// the first reference to the identifier. Callers hold s.mu.
func (s *Server) slotGuardLocked(slotID string) chan struct{} {
	if s.slotGuards == nil {
		s.slotGuards = map[string]chan struct{}{}
	}
	g, ok := s.slotGuards[slotID]
	if !ok {
		g = make(chan struct{}, 1)
		s.slotGuards[slotID] = g
	}
	return g
}

// acquireSlotGuardChan is the acquisition step both hand-out forms share.
// It first attempts the send without waiting, so a free guard is taken
// whatever state ctx is in, and only a contended guard is raced against
// ctx.Done(). The order matters: a select whose cases are both ready picks
// one at random, and the StartSession and SDK-warm rollbacks release their
// slot on the very context whose expiry failed Runtime.Start, so a bare
// two-case select would drop an uncontended guard at random.
// spec: §5.2 (slot-identifier reclaim hold).
func acquireSlotGuardChan(ctx context.Context, g chan struct{}) bool {
	select {
	case g <- struct{}{}:
		return true
	default:
	}
	select {
	case g <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// slotGuardRelease returns the idempotent function that gives g back.
func slotGuardRelease(g chan struct{}) func() {
	var once sync.Once
	return func() { once.Do(func() { <-g }) }
}

// noGuardRelease is the release an acquisition that took no guard returns.
func noGuardRelease() {}

// lockSlotGuard acquires slotID's per-slot guard for a destructive
// section. It never refuses: a reclaim may not be refused by the reclaim
// hold its own deregistration opened. It returns the release and true on
// an acquisition, and a no-op release and false when the acquisition
// outlives ctx, in which case the caller performs its destructive work
// unguarded and treats the cleanup as not completed. s.mu is released
// before the channel is acquired, so the lock order is s.mu never held
// across a guard acquisition. spec: §5.2 (slot-identifier reclaim hold).
func (s *Server) lockSlotGuard(ctx context.Context, slotID string) (func(), bool) {
	s.mu.Lock()
	g := s.slotGuardLocked(slotID)
	s.mu.Unlock()
	if !acquireSlotGuardChan(ctx, g) {
		return noGuardRelease, false
	}
	return slotGuardRelease(g), true
}

// acquireSlotGuardForResolve acquires slotID's per-slot guard for an
// admission RPC whose path work runs outside s.mu. It is the first of the
// reclaim hold's two test points: a held identifier is refused here with
// errSlotReclaimInProgress, without waiting on the guard, so a caller
// that would otherwise block for the whole destructive section receives
// the transient refusal §5.2 promises rather than a later resolve's answer.
// ensureSlotStateLocked is the second test point and stays, because the
// §10.1.4 first pass opens a hold under s.mu without taking any guard.
//
// An acquisition that outlives ctx returns ctx.Err(): proceeding unguarded
// would let a materialization re-create the tree a reclaim has just
// removed. On success the caller holds the guard across its resolve and
// its path work, and calls the returned release when that work has ended.
// spec: §5.2 (slot-identifier reclaim hold); §4.7.1 (role and gateway RPC
// contract), rule 2.
func (s *Server) acquireSlotGuardForResolve(ctx context.Context, slotID string) (func(), error) {
	s.mu.Lock()
	if _, held := s.reclaiming[slotID]; held {
		s.mu.Unlock()
		return noGuardRelease, errSlotReclaimInProgress
	}
	g := s.slotGuardLocked(slotID)
	s.mu.Unlock()
	if !acquireSlotGuardChan(ctx, g) {
		return noGuardRelease, ctx.Err()
	}
	return slotGuardRelease(g), nil
}

// slotGuardCategory returns the §16.3 span category of a guard-acquisition
// refusal: the refusal's own category for the reclaim hold, and TRANSIENT
// for a context that expired while the guard was contended.
// spec: §16.3 (distributed tracing).
func slotGuardCategory(err error) tracing.ErrorCategory {
	var refusal *slotRefusal
	if errors.As(err, &refusal) {
		return refusal.category
	}
	return tracing.CategoryTransient
}

// warnSlotGuardNotAcquired records that a destructive section's guard
// acquisition expired and the section is running unguarded, naming the
// slot identifier and the removing site. The section's reclaim hold is
// retained, because an unguarded removal can run beside a request still
// writing under the identifier. spec: §5.2 (slot-identifier reclaim hold).
func warnSlotGuardNotAcquired(slotID, caller string) {
	slog.Warn("slot_guard_not_acquired", "slot_id", slotID, "caller", caller)
}
