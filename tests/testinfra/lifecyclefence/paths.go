// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// Path names one creation path by the store calls it makes.
type Path string

// The creation paths, each reproducing its gateway path's store calls.
const (
	// PathCreate is a fenced insert with no write before it: POST
	// /v1/sessions, /v1/sessions/start, MCP create_session, the single-shot
	// implicit create, and both fallback creators.
	PathCreate Path = "create"
	// PathDerive reserves the derived ID, copies the snapshot objects and
	// writes the derived manifest row under it, and inserts through
	// CreateReserved; on any failure it deletes what it wrote and then
	// releases the reservation.
	PathDerive Path = "derive"
	// PathDeriveFailure is a derive whose copy fails: the reservation, the
	// partial copy, its undo, and then the terminal derive-failure audit
	// row through Create.
	PathDeriveFailure Path = "derive-failure"
	// PathReplay is a prompt_history replay through CreateWithTranscript.
	PathReplay Path = "replay"
	// PathDelegate is a delegated child with a file export: a delegated-child
	// reservation, the export under the child ID, and CreateReserved with a
	// non-zero delegation depth.
	PathDelegate Path = "delegate"
)

// Paths lists every creation path.
var Paths = []Path{PathCreate, PathDerive, PathDeriveFailure, PathReplay, PathDelegate}

// CreateOutcome records one create.
type CreateOutcome struct {
	ID    string
	Path  Path
	Owner string
	// Err is the create's error. A *sessionstore.CreationRefusal is a
	// refusal; any other non-nil error is a defect.
	Err error
	// Reserved reports that the path's reservation committed.
	Reserved bool
	// ReservedSeq is the sequence number taken after the reservation
	// committed, and CommittedSeq the one taken after the insert committed.
	ReservedSeq, CommittedSeq int64
	// Crashed reports that the path stopped after its pre-insert writes,
	// as a gateway replica that dies between the copy and the insert does.
	Crashed bool
	// ReservationKept reports that the undo after a failed insert could not
	// delete what the path wrote under the ID, so the path left the
	// reservation in place for the owner's erasure to reach.
	ReservationKept bool
}

// Refused reports whether the creation fence refused the create.
func (o CreateOutcome) Refused() bool {
	var ref *sessionstore.CreationRefusal
	return errors.As(o.Err, &ref)
}

// errCopyFailed is the snapshot copy failure of PathDeriveFailure.
var errCopyFailed = errors.New("lifecyclefence: snapshot copy failed")

// Create runs one create of owner on path and records its outcome.
func (w *World) Create(ctx context.Context, path Path, owner string) CreateOutcome {
	o := w.run(ctx, path, owner, false)
	w.record(o)
	return o
}

// CreateCrashing runs a derive of owner that stops after its reservation
// and snapshot copy, before its insert and before any undo, and records
// its outcome.
func (w *World) CreateCrashing(ctx context.Context, owner string) CreateOutcome {
	o := w.run(ctx, PathDerive, owner, true)
	w.record(o)
	return o
}

func (w *World) record(o CreateOutcome) {
	if o.Err != nil && !o.Refused() {
		w.Violatef("%s create %s of %s: unexpected error %v", o.Path, o.ID, o.Owner, o.Err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.outcomes = append(w.outcomes, o)
}

func (w *World) run(ctx context.Context, path Path, owner string, crash bool) CreateOutcome {
	o := CreateOutcome{ID: uuid.NewString(), Path: path, Owner: owner}
	row := sessionstore.Session{ID: o.ID, TenantID: w.Tenant, UserID: owner, RuntimeRef: "echo", State: session.StateCreated}
	switch path {
	case PathCreate:
		o.Err = w.S.Sessions.Create(ctx, row)
	case PathReplay:
		w.mu.Lock()
		src := w.sources[owner]
		w.mu.Unlock()
		o.Err = w.S.Sessions.CreateWithTranscript(ctx, row, src)
	case PathDerive:
		w.reservedWrite(ctx, &o, false, crash, func() error { return w.S.Sessions.CreateReserved(ctx, row) })
		return o
	case PathDelegate:
		row.DelegationDepth = 1
		w.reservedWrite(ctx, &o, true, false, func() error { return w.S.Sessions.CreateReserved(ctx, row) })
		return o
	case PathDeriveFailure:
		w.reservedWrite(ctx, &o, false, false, func() error { return errCopyFailed })
		if o.Reserved && errors.Is(o.Err, errCopyFailed) {
			row.State, row.FailureClass, row.FailureReason = session.StateFailed, session.FailureClassRuntime, "DERIVE_SNAPSHOT_UNAVAILABLE"
			o.Err = w.S.Sessions.Create(ctx, row)
		}
	default:
		o.Err = fmt.Errorf("lifecyclefence: unknown path %q", path)
	}
	if o.Err == nil {
		o.CommittedSeq = w.next()
	}
	return o
}

// reservedWrite runs the reserve, write, insert, and undo sequence of a
// path that writes data keyed to the new session ID before its insert. A
// crash stops after the write. Any insert failure runs the undo.
func (w *World) reservedWrite(ctx context.Context, o *CreateOutcome, delegatedChild, crash bool, insert func() error) {
	if o.Err = w.S.Sessions.Reserve(ctx, w.Tenant, o.Owner, o.ID, delegatedChild); o.Err != nil {
		return
	}
	o.Reserved, o.ReservedSeq = true, w.next()
	w.Objects.Put(w.Tenant, o.ID)
	if !delegatedChild {
		w.Manifests.Put(w.Tenant, o.ID)
	}
	if crash {
		o.Crashed = true
		return
	}
	if fn := w.Faults.takeBeforeInsert(); fn != nil {
		fn()
	}
	if o.Err = insert(); o.Err == nil {
		o.CommittedSeq = w.next()
		return
	}
	w.undoReservedWrite(ctx, o)
}

// undoReservedWrite deletes the objects and the manifest row the path wrote
// under the ID, on a detached context, and releases the reservation only
// when both deletions succeeded. A deletion error keeps the reservation, so
// the owner's erasure still lists the ID and reaches what the failed
// deletion left.
//
// spec: §12.8 (Compliance Interfaces: Lifecycle fence and drain)
func (w *World) undoReservedWrite(ctx context.Context, o *CreateOutcome) {
	undo := context.WithoutCancel(ctx)
	_, objErr := w.Objects.DeleteBySession(undo, w.Tenant, o.ID)
	_, manErr := w.Manifests.DeleteBySession(undo, w.Tenant, o.ID)
	if objErr != nil || manErr != nil {
		o.ReservationKept = true
		return
	}
	if err := w.S.Sessions.ReleaseReservation(undo, w.Tenant, o.ID); err != nil {
		w.Violatef("%s %s: release reservation: %v", o.Path, o.ID, err)
	}
}

// Outcomes returns a copy of the recorded create outcomes.
func (w *World) Outcomes() []CreateOutcome {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]CreateOutcome(nil), w.outcomes...)
}

// Summary counts the recorded creates by outcome and the suspends whose
// drain a resume overtook, for a test's log.
func (w *World) Summary() string {
	var admitted, refused, crashed int
	for _, o := range w.Outcomes() {
		switch {
		case o.Crashed:
			crashed++
		case o.Refused():
			refused++
		case o.Err == nil:
			admitted++
		}
	}
	w.mu.Lock()
	raced := len(w.racedSuspends)
	w.mu.Unlock()
	return fmt.Sprintf("creates admitted=%d refused=%d crashed=%d; raced suspends=%d", admitted, refused, crashed, raced)
}
