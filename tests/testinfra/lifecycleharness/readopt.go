// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordfence"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

// readopter is the harness's coordination.Readopter. It re-adopts a
// still-running pod on the replica whose Sweeper took the session over, as
// the gateway's crash-takeover re-adopt does: it dials the pod named on the
// session row, fences it at the generation the takeover's compare-and-swap
// wrote as the first RPC, and returns a publish callback that places the
// serving binding in the replica's registry. Any failure closes the dialed
// connection, publishes nothing, and releases the lease the Sweeper
// acquired, so a later sweep re-adopts.
//
// spec: §10.1.2 (Coordinator Handoff Protocol), §10.1.5 (Stale Replica
// Behavior)
type readopter struct {
	h *Harness
	r *Replica
}

// generationReader reads a session's coordination generation from the
// shared store for the Fencer.
type generationReader struct{ h *Harness }

func (g generationReader) CoordinationGeneration(ctx context.Context, tenantID, sessionID string) (int64, error) {
	row, err := g.h.Inner.Get(ctx, tenantID, sessionID)
	if err != nil {
		return 0, err
	}
	return row.CoordinationGeneration, nil
}

// ReadoptAndFence implements coordination.Readopter.
func (a readopter) ReadoptAndFence(ctx context.Context, tenantID, sessionID string, generation int64) (func(), error) {
	row, err := a.h.Inner.Get(ctx, tenantID, sessionID)
	if err != nil {
		return nil, a.release(ctx, tenantID, sessionID, fmt.Errorf("read session %s: %w", sessionID, err))
	}
	sb, adapter, err := a.r.Binder.ReadoptConnect(ctx, row.PodAssignment)
	if err != nil {
		return nil, a.release(ctx, tenantID, sessionID, fmt.Errorf("re-adopt dial %s: %w", sessionID, err))
	}
	fencer := coordfence.New(generationReader{a.h}, a.h.Leases, a.r.ID, nil, coordfence.Options{})
	relinquished, err := fencer.FenceAt(ctx, adapter, tenantID, sessionID, generation)
	if err != nil {
		_ = adapter.Close()
		cause := fmt.Errorf("re-adopt fence %s: %w", sessionID, err)
		if relinquished {
			return nil, cause
		}
		return nil, a.release(ctx, tenantID, sessionID, cause)
	}
	bind := &podsession.BindResult{
		SessionID: row.ID, TenantID: row.TenantID, SandboxName: row.PodAssignment,
		PodIP: sb.Status.PodIP, Adapter: adapter, Recycle: a.h.recycling,
		CoordinationGeneration: generation,
	}
	// spec: §5.2 (Pool Configuration and Execution Modes), §10.1.2 — on a
	// concurrent-workspace pool the re-adopted binding is the session's
	// slot, so its release removes the session's member, as the gateway's
	// re-adopt binding does.
	if a.h.Concurrent() {
		bind.SlotID = row.ID
	}
	return func() { a.r.Registry.Put(bind) }, nil
}

// release relinquishes the lease the takeover acquired and returns cause.
func (a readopter) release(ctx context.Context, tenantID, sessionID string, cause error) error {
	if err := a.h.Leases.Release(ctx, tenantID, sessionID, a.r.ID); err != nil {
		return fmt.Errorf("%w; release lease: %v", cause, err)
	}
	return cause
}
