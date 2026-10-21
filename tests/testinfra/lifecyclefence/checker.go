// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// Check reports every recorded violation and then runs the end-of-scenario
// invariants over the World's stores. Call it once every goroutine of the
// scenario has returned.
func (w *World) Check(t testing.TB) {
	t.Helper()
	ctx := context.Background()
	w.mu.Lock()
	violations := append([]string(nil), w.violations...)
	raced := append([]racedObservation(nil), w.racedSuspends...)
	erasedAt := make(map[string]int64, len(w.erasedAt))
	for k, v := range w.erasedAt {
		erasedAt[k] = v
	}
	w.mu.Unlock()
	for _, v := range violations {
		t.Error(v)
	}
	reserved, err := w.reservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range w.Outcomes() {
		if msg := w.checkOutcome(ctx, o, erasedAt, reserved); msg != "" {
			t.Errorf("%s create %s of %s (err %v): %s", o.Path, o.ID, o.Owner, o.Err, msg)
		}
	}
	for _, o := range raced {
		for _, v := range w.resumeDuringDrainViolations(o) {
			t.Error(v)
		}
	}
	w.checkSuspendAudit(t)
	if err := w.checkOneJobPerUser(ctx); err != nil {
		t.Error(err)
	}
}

// reservations returns every reserved session ID of the World's users.
func (w *World) reservations(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	for _, u := range Users {
		ids, err := w.S.Sessions.ReservedSessionIDs(ctx, w.Tenant, u)
		if err != nil {
			return nil, fmt.Errorf("list reservations of %s: %w", u, err)
		}
		for _, id := range ids {
			out[id] = true
		}
	}
	return out, nil
}

// residue describes what remains under one session ID.
type residue struct {
	row                bool
	objects, manifests int
	transcript         int
	reserved           bool
	readErr            error
}

func (w *World) residueOf(ctx context.Context, id string, reserved map[string]bool) residue {
	r := residue{
		objects:   w.Objects.Count(w.Tenant, id),
		manifests: w.Manifests.Count(w.Tenant, id),
		reserved:  reserved[id],
	}
	_, err := w.S.Sessions.Get(ctx, w.Tenant, id)
	switch {
	case err == nil:
		r.row = true
	case !errors.Is(err, sessionstore.ErrNotFound):
		r.readErr = err
	}
	if n, err := w.S.transcriptLen(ctx, w.Tenant, id); err != nil {
		r.readErr = err
	} else {
		r.transcript = n
	}
	return r
}

func (r residue) empty() bool {
	return !r.row && r.objects == 0 && r.manifests == 0 && r.transcript == 0 && !r.reserved
}

// inErasureReach reports whether the outcome's writes committed before the
// owner's erasure wrote its completion receipt. A derive-failure row is
// judged by its insert, because the row its insert commits after the
// erasure is a new session admitted once the restriction was cleared; a
// derive failure whose undo kept the reservation and whose row was refused
// is judged by its reservation, which is all that reaches its writes.
func inErasureReach(o CreateOutcome, erasedAt map[string]int64) bool {
	at, erased := erasedAt[o.Owner]
	if !erased {
		return false
	}
	seq := o.CommittedSeq
	if o.Reserved && (o.Path != PathDeriveFailure || keptWithoutRow(o)) {
		seq = o.ReservedSeq
	}
	return seq > 0 && seq < at
}

// keptWithoutRow reports that the create inserted no row and its undo kept
// the reservation.
func keptWithoutRow(o CreateOutcome) bool { return o.ReservationKept && o.Err != nil }

// checkOutcome returns what is wrong with one create's residue, or "".
func (w *World) checkOutcome(ctx context.Context, o CreateOutcome, erasedAt map[string]int64, reserved map[string]bool) string {
	r := w.residueOf(ctx, o.ID, reserved)
	if r.readErr != nil {
		return r.readErr.Error()
	}
	switch {
	case inErasureReach(o, erasedAt):
		// The checkpoint-manifest eraser is not wired to the erasure, so a
		// manifest row of a derive admitted before the erasure is outside
		// its reach; every other store is inside it.
		r.manifests = 0
		if !r.empty() {
			return fmt.Sprintf("outlived its owner's erasure: %+v", r)
		}
	case keptWithoutRow(o):
		// The undo could not delete what the path wrote, so the reservation
		// must still stand: it is the only thing that lists the ID to the
		// owner's erasure.
		if !r.reserved || r.row {
			return fmt.Sprintf("an undo whose deletion failed must keep the reservation and no row: %+v", r)
		}
	case o.Refused():
		if !r.empty() {
			return fmt.Sprintf("refused create left residue: %+v", r)
		}
	case o.Crashed:
		if r.row {
			return "a create that stopped before its insert has a row"
		}
	case o.Err == nil:
		if !r.row {
			return "admitted create has no row"
		}
		if o.Path == PathReplay && r.transcript != replayWant(o, erasedAt) {
			return fmt.Sprintf("admitted replay copied %d transcript entries, want %d", r.transcript, replayWant(o, erasedAt))
		}
	}
	return ""
}

// replayWant is the transcript length an admitted replay copies: the
// source's entries, or none once the owner's erasure has deleted the source.
func replayWant(o CreateOutcome, erasedAt map[string]int64) int {
	if at, erased := erasedAt[o.Owner]; erased && o.CommittedSeq > at {
		return 0
	}
	return sourceEntries
}

// checkSuspendAudit asserts one tenant.suspended row with
// already_suspended=false per suspension-marker transition.
func (w *World) checkSuspendAudit(t testing.TB) {
	t.Helper()
	first := 0
	for _, e := range w.Audit.Events("tenant.suspended") {
		if e.Detail["already_suspended"] == false {
			first++
		}
	}
	if got, want := first, w.SuspendTransitions(); got != want {
		t.Errorf("tenant.suspended rows with already_suspended=false = %d, marker transitions = %d", got, want)
	}
}

// checkOneJobPerUser asserts that no user of the World has more than one
// in-progress erasure job.
func (w *World) checkOneJobPerUser(ctx context.Context) error {
	active, err := w.S.Jobs.ListActive(ctx)
	if err != nil {
		return fmt.Errorf("list active erasure jobs: %w", err)
	}
	per := map[string][]string{}
	for _, j := range active {
		if j.TenantID == w.Tenant && !j.Phase.Terminal() {
			per[j.UserID] = append(per[j.UserID], j.ID)
		}
	}
	for u, ids := range per {
		if len(ids) > 1 {
			return fmt.Errorf("user %s has %d in-progress erasure jobs: %v", u, len(ids), ids)
		}
	}
	return nil
}

// JobPhaseLog returns the phases the job passed through, for a test that
// asserts a resumed job re-ran no completed step.
func JobPhaseLog(j erasurejob.Job) []erasurejob.Phase {
	out := make([]erasurejob.Phase, len(j.PhaseLog))
	for i, tr := range j.PhaseLog {
		out[i] = tr.Phase
	}
	return out
}
