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

// The creation paths. A plain create and a replay are single store calls.
// A derive, a failing derive, and a delegation run the production gateway
// code that owns their reserved write and its undo: the session server's
// derive handler and the delegation service.
const (
	// PathCreate is a fenced insert with no write before it: POST
	// /v1/sessions, /v1/sessions/start, MCP create_session, the single-shot
	// implicit create, and both fallback creators.
	PathCreate Path = "create"
	// PathDerive is a derive through the session server: it reserves the
	// derived ID, copies the source's checkpoint chunk and writes the
	// derived manifest row under it, and inserts through CreateReserved;
	// on a refusal it deletes what it wrote and then releases the
	// reservation.
	PathDerive Path = "derive"
	// PathDeriveFailure is a derive through the session server whose copy
	// fails after the chunk copy and the manifest write: the handler undoes
	// both and then writes the terminal derive-failure row through Create.
	PathDeriveFailure Path = "derive-failure"
	// PathReplay is a prompt_history replay through CreateWithTranscript.
	PathReplay Path = "replay"
	// PathDelegate is a delegated child with a file export through the
	// delegation service: a delegated-child reservation, the export under
	// the child ID, and CreateReserved with a non-zero delegation depth.
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
	// ReservationKept reports that the path reserved the ID and then
	// neither inserted under the reservation nor released it: its undo
	// could not delete what it wrote, so it left the reservation in place
	// for the owner's erasure to reach.
	ReservationKept bool
}

// Refused reports whether the creation fence refused the create.
func (o CreateOutcome) Refused() bool {
	var ref *sessionstore.CreationRefusal
	return errors.As(o.Err, &ref)
}

// errCopyFailed is the snapshot copy failure of PathDeriveFailure.
var errCopyFailed = errors.New("lifecyclefence: snapshot copy failed")

// ErrSourceGone marks a derive or a delegation that found its source or
// parent session gone or ended, because the owner's erasure or a drain
// reached it first. It is neither a refusal nor a defect, and the path
// wrote nothing under its session ID.
var ErrSourceGone = errors.New("lifecyclefence: source session gone")

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
	if o.Err != nil && !o.Refused() && !errors.Is(o.Err, ErrSourceGone) {
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
	case PathDerive, PathDeriveFailure:
		w.runDerive(ctx, &o, path == PathDeriveFailure, crash)
		return o
	case PathDelegate:
		w.runDelegate(ctx, &o)
		return o
	default:
		o.Err = fmt.Errorf("lifecyclefence: unknown path %q", path)
	}
	if o.Err == nil {
		o.CommittedSeq = w.next()
	}
	return o
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
	var admitted, refused, crashed, gone int
	for _, o := range w.Outcomes() {
		switch {
		case o.Crashed:
			crashed++
		case o.Refused():
			refused++
		case errors.Is(o.Err, ErrSourceGone):
			gone++
		case o.Err == nil:
			admitted++
		}
	}
	w.mu.Lock()
	raced := len(w.racedSuspends)
	w.mu.Unlock()
	return fmt.Sprintf("creates admitted=%d refused=%d crashed=%d source-gone=%d; raced suspends=%d", admitted, refused, crashed, gone, raced)
}
