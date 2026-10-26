// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// crashStop is the panic value pathStore raises to stop a production create
// path before its reserved insert, as a gateway replica that dies there does.
// Nothing after the panic runs, including any undo.
type crashStop struct{}

// pathStore is the session store one production create path runs against.
// It passes every call to the World's store and observes the calls keyed by
// the create's session ID: it records the reservation's and the insert's
// sequence numbers, runs the World's before-insert hook and the crash stop
// just before the reserved insert, and records whether the path released
// the reservation. At each release it asserts that the path already deleted
// everything it wrote under the ID, which is the undo order the §12.8
// lifecycle fence fixes: delete the pre-insert writes, then release.
//
// A pathStore serves one create and is used from that create's goroutine.
type pathStore struct {
	sessionstore.Store
	w     *World
	o     *CreateOutcome
	crash bool

	// fenceErr is the error of the reservation or the reserved insert,
	// which carries the fence's refusal when it refused.
	fenceErr error
	// released reports that the path released the reservation.
	released bool
	// inserted reports that the reserved insert committed.
	inserted bool
	// failureRow reports that the path inserted, or tried to insert, its
	// terminal derive-failure row, and failureRowErr is that insert's error.
	failureRow    bool
	failureRowErr error
}

func (w *World) newPathStore(o *CreateOutcome, crash bool) *pathStore {
	return &pathStore{Store: w.S.Sessions, w: w, o: o, crash: crash}
}

// Reserve implements sessionstore.Store.
func (p *pathStore) Reserve(ctx context.Context, tenantID, owner, id string, delegatedChild bool) error {
	err := p.Store.Reserve(ctx, tenantID, owner, id, delegatedChild)
	if id != p.o.ID {
		return err
	}
	if err != nil {
		p.fenceErr = err
		return err
	}
	p.o.Reserved, p.o.ReservedSeq = true, p.w.next()
	return nil
}

// CreateReserved implements sessionstore.Store.
func (p *pathStore) CreateReserved(ctx context.Context, s sessionstore.Session) error {
	if s.ID != p.o.ID {
		return p.Store.CreateReserved(ctx, s)
	}
	if fn := p.w.Faults.takeBeforeInsert(); fn != nil {
		fn()
	}
	if p.crash {
		p.o.Crashed = true
		panic(crashStop{})
	}
	err := p.Store.CreateReserved(ctx, s)
	if err != nil {
		p.fenceErr = err
		return err
	}
	p.inserted, p.o.CommittedSeq = true, p.w.next()
	return nil
}

// CreateIfGeneration implements sessionstore.Store. Under the create's ID it
// is the terminal derive-failure row's insert, which the session server
// fences on the source's coordination generation inside the insert.
// spec: §7.1 (Derive session semantics), §12.8 (Lifecycle fence and drain)
func (p *pathStore) CreateIfGeneration(ctx context.Context, s sessionstore.Session, sourceTenantID, sourceID string, wantGen int64) error {
	if s.ID != p.o.ID {
		return p.Store.CreateIfGeneration(ctx, s, sourceTenantID, sourceID, wantGen)
	}
	if fn := p.w.Faults.takeBeforeFailureRow(); fn != nil {
		fn()
	}
	err := p.Store.CreateIfGeneration(ctx, s, sourceTenantID, sourceID, wantGen)
	p.failureRow, p.failureRowErr = true, err
	if err == nil {
		p.o.CommittedSeq = p.w.next()
	}
	return err
}

// ReleaseReservation implements sessionstore.Store. A release while an
// object or a manifest row remains under the ID is a violation: the
// reservation is all that lists the ID to the owner's erasure, so releasing
// it first, or after a deletion that failed, strands what remains.
//
// spec: §12.8 (Compliance Interfaces: Lifecycle fence and drain)
func (p *pathStore) ReleaseReservation(ctx context.Context, tenantID, id string) error {
	if id == p.o.ID {
		if n, m := p.w.Objects.Count(tenantID, id), p.w.Manifests.Count(tenantID, id); n > 0 || m > 0 {
			p.w.Violatef("%s %s: released the reservation with %d objects and %d manifest rows still under the ID", p.o.Path, id, n, m)
		}
		p.released = true
	}
	return p.Store.ReleaseReservation(ctx, tenantID, id)
}

// reservationKept reports that the path reserved the ID, neither inserted
// under its reservation nor released it, and did not stop before its
// insert: its undo kept the reservation for the owner's erasure.
func (p *pathStore) reservationKept() bool {
	return p.o.Reserved && !p.inserted && !p.released && !p.o.Crashed
}
