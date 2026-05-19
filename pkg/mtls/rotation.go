// SPDX-License-Identifier: MIT

// Package mtls hosts the §10.3 mTLS PKI primitives that are runtime
// state in the gateway's address space: the in-memory certificate
// deny list (subpackage denylist), the SPIFFE-URI parser
// (subpackage spiffe), and the CA-rotation procedure helper in this
// file. The cert-manager `Certificate` and `Issuer` resources that
// drive issuance live in the Helm chart; this package is the
// in-process orchestrator the gateway calls to walk the rotation
// state machine.
package mtls

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultCARotationOverlap is the §10.3 CA-rotation overlap window:
// once the new CA is deployed the gateway's trust bundle includes
// both CAs for this duration before the old CA is removed. The 24h
// default matches the §10.3 JWT signing-key overlap so an operator
// performs both rotations against a single mental model.
const DefaultCARotationOverlap = 24 * time.Hour

// CARotationStage names a position in the §10.3 CA-rotation state
// machine. The closed set is enumerated below; CARotation transitions
// through every stage in order. Each stage is durable: an operator
// who interrupts the procedure (e.g., to wait for cert-manager to
// reissue leaf certificates) resumes from the recorded stage rather
// than restarting.
type CARotationStage string

const (
	// CAStageIdle is the resting state: a single trusted CA, no
	// rotation in flight. The gateway's trust bundle contains only
	// the current CA. NewCARotation starts here.
	CAStageIdle CARotationStage = "idle"

	// CAStageNewCADeployed is set by BeginNewCARotation. The new CA
	// has been issued (cert-manager has minted the new `ClusterIssuer`
	// and the Helm chart has rolled out the new trust bundle) and
	// every gateway / controller / Token Service replica trusts BOTH
	// CAs. New leaf certificates are still signed by the old CA.
	CAStageNewCADeployed CARotationStage = "new_ca_deployed"

	// CAStagePromoted is set by PromoteNewCA. The new CA is now the
	// issuer; cert-manager signs every new `Certificate` with it.
	// The old CA stays in the trust bundle so leaves still signed
	// under it keep validating until they renew. Both CAs are
	// trusted; only the new one issues.
	CAStagePromoted CARotationStage = "promoted"

	// CAStageOldCARetired is the terminal stage of a rotation cycle:
	// every leaf has rotated under the new CA (24h-TTL gateway certs
	// have rolled at 2/3 lifetime; 4h-TTL agent certs have rolled at
	// least once), the old CA's overlap window has closed, and
	// RetireOldCA has dropped it from the trust bundle. The next
	// rotation starts from CAStageIdle again (a fresh CARotation is
	// constructed for it).
	CAStageOldCARetired CARotationStage = "old_ca_retired"
)

// AllCARotationStages returns the closed §10.3 stage enum in the
// order CARotation advances through them.
func AllCARotationStages() []CARotationStage {
	return []CARotationStage{
		CAStageIdle, CAStageNewCADeployed, CAStagePromoted, CAStageOldCARetired,
	}
}

// IsValid reports whether s is one of the §10.3 stages.
func (s CARotationStage) IsValid() bool {
	for _, v := range AllCARotationStages() {
		if s == v {
			return true
		}
	}
	return false
}

// CARotationSnapshot is a point-in-time view of a rotation's state.
// CARotation.Snapshot returns one; tests assert against it without
// reaching into the rotation's private fields. The fields mirror the
// audit row the gateway commits per stage transition so a caller
// building one from the other does not have to translate.
type CARotationSnapshot struct {
	// Stage is the current stage of the rotation.
	Stage CARotationStage

	// CurrentCAID names the CA that signs new leaf certificates at
	// this stage. Up to CAStagePromoted this is the old CA; from
	// CAStagePromoted onward it is the new CA.
	CurrentCAID string

	// TrustedCAIDs are every CA the gateway's trust bundle accepts
	// at this stage. CAStageIdle and CAStageOldCARetired carry one
	// entry; CAStageNewCADeployed and CAStagePromoted carry two.
	TrustedCAIDs []string

	// OverlapStartedAt is the verifier-clock instant the new CA
	// joined the trust bundle (the BeginNewCARotation call). Zero
	// until BeginNewCARotation runs.
	OverlapStartedAt time.Time

	// OverlapClosesAt is OverlapStartedAt + OverlapWindow. Past this
	// instant RetireOldCA is safe to call: every leaf signed under
	// the old CA has either renewed (cert-manager auto-renews at
	// 2/3 TTL) or expired naturally.
	OverlapClosesAt time.Time
}

// CARotationOptions configure a CARotation at construction.
type CARotationOptions struct {
	// OverlapWindow is the §10.3 minimum time the new and old CA
	// coexist in the trust bundle. RetireOldCA refuses to run before
	// OverlapStartedAt + OverlapWindow has elapsed. A value <= 0
	// takes DefaultCARotationOverlap.
	OverlapWindow time.Duration

	// Now overrides the wall clock. Tests substitute a fake clock so
	// the overlap window can be traversed deterministically. Nil
	// defaults to time.Now.
	Now func() time.Time

	// Observer receives one CARotationEvent per committed stage
	// transition. The gateway-side wiring commits a
	// `platform.ca_rotated` audit row per event; tests use a
	// recording observer. Nil disables notification.
	Observer CARotationObserver
}

// CARotationEvent is one §10.3 stage transition. CARotationObserver
// receives one event per successful Begin / Promote / Retire call.
type CARotationEvent struct {
	// From is the stage the rotation just left.
	From CARotationStage

	// To is the stage the rotation just entered.
	To CARotationStage

	// CurrentCAID is the CA that signs new leaves after the
	// transition.
	CurrentCAID string

	// TrustedCAIDs are the CAs in the trust bundle after the
	// transition.
	TrustedCAIDs []string

	// At is the rotation-clock instant the transition committed.
	At time.Time
}

// CARotationObserver receives one CARotationEvent per §10.3 stage
// transition. The callback runs synchronously under the rotation's
// write lock and MUST NOT block on external IO.
type CARotationObserver interface {
	OnCARotation(ev CARotationEvent)
}

// CARotation walks the §10.3 CA-rotation state machine in-process.
// It is the operator's progress tracker: the actual cert-manager
// `Issuer` and `Certificate` resources are out of scope (those live
// in the chart), but the staged transition between trust postures
// is recorded here so the gateway audit pipeline can emit
// `platform.ca_rotated` events at each step and the operator runbook
// has a single object to query.
//
// A CARotation is built with a starting CA (the current issuer at
// rotation start). BeginNewCARotation introduces the new CA to the
// trust bundle; PromoteNewCA makes it the issuer; RetireOldCA drops
// the old CA after the overlap window closes. The transitions are
// strictly linear: each call rejects a stage transition that
// violates the sequence.
//
// CARotation is safe for concurrent use; a sync.Mutex serialises
// every transition and the snapshot read.
type CARotation struct {
	mu               sync.Mutex
	stage            CARotationStage
	oldCAID          string
	newCAID          string
	overlapStartedAt time.Time
	overlapWindow    time.Duration
	now              func() time.Time
	observer         CARotationObserver
}

// NewCARotation returns a rotation tracker in CAStageIdle with
// currentCAID as the active issuer and trust anchor.
//
// currentCAID must be non-empty: a rotation tracker that does not
// name the starting CA cannot record the trust-bundle transitions
// meaningfully. Returns a *RotationError on an empty currentCAID.
func NewCARotation(currentCAID string, opts CARotationOptions) (*CARotation, error) {
	if currentCAID == "" {
		return nil, &RotationError{Kind: "invalid_argument", Detail: "currentCAID is empty"}
	}
	window := opts.OverlapWindow
	if window <= 0 {
		window = DefaultCARotationOverlap
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &CARotation{
		stage:         CAStageIdle,
		oldCAID:       currentCAID,
		overlapWindow: window,
		now:           now,
		observer:      opts.Observer,
	}, nil
}

// BeginNewCARotation introduces newCAID into the trust bundle.
// After this call the gateway trusts both the old and new CAs; the
// old CA still signs new leaves. Records the rotation start instant
// so RetireOldCA can refuse to run before the overlap window closes.
//
// Refuses with a *RotationError when the rotation is not in
// CAStageIdle (a second Begin would clobber an in-flight rotation),
// when newCAID is empty, or when newCAID equals the existing CA.
func (r *CARotation) BeginNewCARotation(newCAID string) error {
	if newCAID == "" {
		return &RotationError{Kind: "invalid_argument", Detail: "newCAID is empty"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stage != CAStageIdle {
		return &RotationError{
			Kind:   "invalid_stage",
			Detail: fmt.Sprintf("BeginNewCARotation requires stage=%q, have %q", CAStageIdle, r.stage),
		}
	}
	if newCAID == r.oldCAID {
		return &RotationError{
			Kind:   "invalid_argument",
			Detail: fmt.Sprintf("newCAID %q equals the current CA", newCAID),
		}
	}
	r.newCAID = newCAID
	r.overlapStartedAt = r.now()
	prev := r.stage
	r.stage = CAStageNewCADeployed
	r.emit(prev)
	return nil
}

// PromoteNewCA swaps the issuer role from the old CA to the new CA.
// After this call cert-manager signs every new `Certificate` with
// the new CA; both CAs stay trusted so leaves still signed under the
// old CA verify until they renew or expire.
//
// Refuses with a *RotationError when the rotation is not in
// CAStageNewCADeployed.
func (r *CARotation) PromoteNewCA() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stage != CAStageNewCADeployed {
		return &RotationError{
			Kind: "invalid_stage",
			Detail: fmt.Sprintf("PromoteNewCA requires stage=%q, have %q",
				CAStageNewCADeployed, r.stage),
		}
	}
	prev := r.stage
	r.stage = CAStagePromoted
	r.emit(prev)
	return nil
}

// RetireOldCA drops the old CA from the trust bundle. After this
// call the gateway accepts only certificates issued under the new
// CA. The rotation reaches its terminal CAStageOldCARetired.
//
// Refuses with a *RotationError when the rotation is not in
// CAStagePromoted, or when the overlap window has not closed
// (now < overlapStartedAt + overlapWindow). The overlap guard is
// load-bearing: retiring the old CA before every leaf has rotated
// breaks active connections that still present an old-CA cert.
func (r *CARotation) RetireOldCA() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stage != CAStagePromoted {
		return &RotationError{
			Kind:   "invalid_stage",
			Detail: fmt.Sprintf("RetireOldCA requires stage=%q, have %q", CAStagePromoted, r.stage),
		}
	}
	now := r.now()
	closes := r.overlapStartedAt.Add(r.overlapWindow)
	if now.Before(closes) {
		return &RotationError{
			Kind: "overlap_open",
			Detail: fmt.Sprintf(
				"overlap window still open: now=%s, closes at %s (remaining %s)",
				now.UTC().Format(time.RFC3339),
				closes.UTC().Format(time.RFC3339),
				closes.Sub(now)),
		}
	}
	prev := r.stage
	r.stage = CAStageOldCARetired
	r.emit(prev)
	return nil
}

// Snapshot returns the current state of the rotation. Allocated
// fresh on each call; the returned slices are owned by the caller.
func (r *CARotation) Snapshot() CARotationSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked()
}

// snapshotLocked composes the snapshot. Must be called with r.mu
// held.
func (r *CARotation) snapshotLocked() CARotationSnapshot {
	snap := CARotationSnapshot{
		Stage:            r.stage,
		OverlapStartedAt: r.overlapStartedAt,
	}
	if !r.overlapStartedAt.IsZero() {
		snap.OverlapClosesAt = r.overlapStartedAt.Add(r.overlapWindow)
	}
	switch r.stage {
	case CAStageIdle:
		snap.CurrentCAID = r.oldCAID
		snap.TrustedCAIDs = []string{r.oldCAID}
	case CAStageNewCADeployed:
		snap.CurrentCAID = r.oldCAID
		snap.TrustedCAIDs = []string{r.oldCAID, r.newCAID}
	case CAStagePromoted:
		snap.CurrentCAID = r.newCAID
		snap.TrustedCAIDs = []string{r.oldCAID, r.newCAID}
	case CAStageOldCARetired:
		snap.CurrentCAID = r.newCAID
		snap.TrustedCAIDs = []string{r.newCAID}
	}
	return snap
}

// emit notifies the observer of a committed stage transition. Must
// be called with r.mu held and after r.stage has advanced.
func (r *CARotation) emit(prev CARotationStage) {
	if r.observer == nil {
		return
	}
	snap := r.snapshotLocked()
	// Detach the slice so a slow observer cannot mutate the rotation's
	// internal view.
	trusted := append([]string(nil), snap.TrustedCAIDs...)
	r.observer.OnCARotation(CARotationEvent{
		From:         prev,
		To:           r.stage,
		CurrentCAID:  snap.CurrentCAID,
		TrustedCAIDs: trusted,
		At:           r.now(),
	})
}

// RotationError captures a §10.3 CA-rotation precondition violation
// or invariant break. Kind classifies the failure for callers that
// branch on it; Detail carries the human-readable cause for the
// operator-facing runbook.
type RotationError struct {
	Kind   string
	Detail string
}

func (e *RotationError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("mtls: ca_rotation: %s: %s", e.Kind, e.Detail)
	}
	return "mtls: ca_rotation: " + e.Kind
}

// IsOverlapOpen reports whether err is a *RotationError signalling
// that RetireOldCA was called before the overlap window closed. The
// operator runbook surfaces this distinctly from other invariant
// breaks because the remediation is to wait rather than to recover
// from a state-machine fault.
func IsOverlapOpen(err error) bool {
	var re *RotationError
	if errors.As(err, &re) {
		return re.Kind == "overlap_open"
	}
	return false
}
