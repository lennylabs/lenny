// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// runDelegate drives a delegation with a file export through the
// production delegation service, with the World's object store injected as
// its SessionObjects. The child-ID reservation, the export under the child
// ID, the insert through CreateReserved, and the undo that deletes the
// export objects and then releases the reservation are all production
// code; pathStore observes them. The parent is a running session of the
// owner, created through the fenced insert just before the delegation.
//
// spec: §12.8 (Compliance Interfaces: Lifecycle fence and drain)
func (w *World) runDelegate(ctx context.Context, o *CreateOutcome) {
	parent, err := w.createParent(ctx, o.Owner)
	if err != nil {
		o.Err = err
		return
	}
	p := w.newPathStore(o, false)
	svc := delegation.NewService(p, delegation.Options{
		IDFunc:             func() string { return o.ID },
		ExportMaterializer: exportWriter{objects: w.Objects},
		SessionObjects:     w.Objects,
	})
	_, err = svc.Delegate(ctx, w.Tenant, delegation.Request{
		ParentSessionID: parent,
		// A child on the parent's own runtime and pool is a self-recursive
		// hop, which the cycle gate refuses by default.
		RuntimeRef:       "echo-child",
		PoolRef:          "echo-child-pool",
		IsolationProfile: isolation.ProfileSandboxed,
		FileExport:       []export.Spec{{Source: "out/*"}},
	})
	o.ReservationKept = p.reservationKept()
	switch {
	case err == nil && p.inserted:
		o.Err = nil
	case errors.Is(err, delegation.ErrParentNotFound), errors.Is(err, delegation.ErrParentNotRunning):
		// A drain or the owner's erasure ended the parent before the
		// delegation read it.
		o.Err = fmt.Errorf("%w: %w", ErrSourceGone, err)
	case err == nil:
		o.Err = errors.New("lifecyclefence: delegation answered success without a reserved insert")
	default:
		o.Err = err
	}
}

// createParent inserts a running session of owner, through the fenced
// insert, to delegate from, and records its commit sequence number so a
// raced suspend's check can tell whether it was created after the resume.
func (w *World) createParent(ctx context.Context, owner string) (string, error) {
	row := sessionstore.Session{
		ID: uuid.NewString(), TenantID: w.Tenant, UserID: owner, RuntimeRef: "echo", PoolRef: "echo-pool",
		State: session.StateRunning, IsolationProfile: isolation.ProfileSandboxed,
	}
	if err := w.S.Sessions.Create(ctx, row); err != nil {
		return "", err
	}
	seq := w.next()
	w.mu.Lock()
	w.parentCommits[row.ID] = seq
	w.mu.Unlock()
	return row.ID, nil
}

// exportWriter is the delegation's export materializer: it writes one
// exported file under the child session ID in the World's object store, as
// the production materializer persists each exported file, and returns no
// workspace plan sources.
type exportWriter struct{ objects *Objects }

// Materialize implements delegation.ExportMaterializer.
func (e exportWriter) Materialize(_ context.Context, p export.Params) (export.Result, error) {
	u := blobstore.URI{
		TenantID: p.TenantID, ObjectType: blobstore.ObjectTypeExport,
		SessionID: p.ChildSessionID, PartID: "out.txt", TTL: time.Hour,
	}
	if _, err := e.objects.Put(u, "text/plain", strings.NewReader("exported")); err != nil {
		return export.Result{}, fmt.Errorf("persist export of %s: %w", p.ChildSessionID, err)
	}
	return export.Result{FileCount: 1}, nil
}
