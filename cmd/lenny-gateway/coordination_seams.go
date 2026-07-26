// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// coordinationBindings adapts the per-replica podsession registry and the pod
// executor to the coordination.BindingRegistry the §10.1 lease Sweeper
// consumes. It keeps the coordination lease co-located with the live pod
// binding: the Sweeper renews the lease only for the sessions this replica
// binds, and on a bound session whose held gateway-to-pod channel has died it
// evicts the binding and releases the lease so a subsequent sweep re-adopts
// the still-running pod before its §10.1 hold-state self-termination.
//
// The collaborators (the podsession registry and the executor) are constructed
// in later composition-root build steps than the Sweeper, so this adapter reads
// them off the accumulator at call time rather than capturing them at
// construction. The Sweeper's Run loop starts only after the whole composition
// root is wired, so both are populated by the first sweep.
//
// spec: §4.6.1 (coordinating replica holds the lease), §10.1 (per-session
// coordination lease; hold state on connection loss), §4.7 (single content
// consumer per session / Attach content stream).
type coordinationBindings struct {
	w *gatewayWiring
}

// streamEvicter is the executor seam the dead-connection eviction calls to drop
// the session's cached Attach stream. *executor.PodExecutor satisfies it; the
// echo/subprocess executors do not, and a dead-connection eviction on those is
// a no-op because they hold no pod binding for the Sweeper to renew.
type streamEvicter interface {
	EvictStream(sessionID string)
}

// Bound reports whether this replica holds a live pod binding for the session.
func (c coordinationBindings) Bound(sessionID string) bool {
	if c.w.podRegistry == nil {
		return false
	}
	_, ok := c.w.podRegistry.Get(sessionID)
	return ok
}

// ConnAlive reports whether the bound session's held gateway-to-pod gRPC
// channel is still live. It is consulted only for a bound session. A binding
// with no live adapter (a channel in TransientFailure or Shutdown, or a missing
// adapter) reports dead, so the Sweeper surfaces the lease for re-adoption
// instead of pinning it to a replica that can no longer reach the pod.
func (c coordinationBindings) ConnAlive(sessionID string) bool {
	if c.w.podRegistry == nil {
		return false
	}
	bind, ok := c.w.podRegistry.Get(sessionID)
	if !ok || bind.Adapter == nil {
		return false
	}
	return bind.Adapter.Alive()
}

// EvictBinding drops the session's binding from the podsession registry and the
// executor's cached Attach stream in one call. Dropping the cached stream is
// required because the executor consults its stream cache before the registry,
// so a registry-only eviction would let a same-replica re-adopt keep serving
// over the stale dead cached stream instead of Attaching over the freshly
// published binding. spec: §4.7 (single content consumer per session), §10.1.
func (c coordinationBindings) EvictBinding(sessionID string) {
	if c.w.podRegistry != nil {
		c.w.podRegistry.Remove(sessionID)
	}
	if se, ok := c.w.exec.(streamEvicter); ok {
		se.EvictStream(sessionID)
	}
}

// readoptDialer re-opens the §4.7 adapter connection to a still-running pod on
// the crash-takeover edge, without the §15.5 version handshake, so the caller
// can send CoordinatorFence as the first RPC. *podsession.Binder satisfies it
// through ReadoptConnect.
type readoptDialer interface {
	ReadoptConnect(ctx context.Context, sandboxName string) (*lennyv1.Sandbox, *adapterclient.Client, error)
}

// readoptFencer announces the session's coordination_generation to the
// re-adopted pod. sessionserver.CoordinationFencer (a *coordfence.Fencer)
// satisfies it. It returns relinquished=true only when it released the
// coordination lease itself on a terminal fence failure (the retry budget was
// exhausted or the pod is fenced to a higher generation). A best-effort fence
// failure — a coordination_generation read error or a context cancellation —
// returns relinquished=false with the lease still held, and the caller
// releases it so the lapse surfaces for re-adoption. spec: §10.1, §11.3.
type readoptFencer interface {
	Fence(ctx context.Context, adapter *adapterclient.Client, tenantID, sessionID string) (relinquished bool, err error)
}

// leaseReleaser releases the coordination lease this replica holds for a
// session. leasestore.LeaseStore satisfies it through Release. The re-adopt
// releases the lease itself on every re-adopt failure that leaves it held: a
// session-row read fault, a pod dial fault, or a best-effort fence fault the
// Fencer did not relinquish. Only a terminal fence failure arrives with the
// lease already relinquished. Releasing the still-held lease surfaces its lapse
// for a subsequent sweep to re-adopt the still-running pod rather than pinning
// the lease to a replica that never established the serving binding.
// spec: §10.1 (relinquish-and-backoff; hold state on connection loss).
type leaseReleaser interface {
	Release(ctx context.Context, tenantID, sessionID, holder string) error
}

// readoptPublisher places the re-established BindResult into the shared
// per-replica registry. *podsession.Registry satisfies it through Put.
type readoptPublisher interface {
	Put(b *podsession.BindResult)
}

// sandboxNameReader resolves the persisted SandboxName that names the
// still-running pod to re-adopt. sessionstore.Store satisfies it through Get.
type sandboxNameReader interface {
	Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error)
}

// coordinationReadopter adapts the podsession Binder's fence-first re-adopt
// entry point, the reused §10.1 coordfence Fencer, and the podsession registry
// to the coordination.Readopter the Sweeper drives on the crash-takeover edge.
// On a successful Acquire that changed the holder to this replica for a
// still-running-pod session it does not bind, the Sweeper bumps
// coordination_generation and calls ReadoptAndFence to re-establish the serving
// binding on this replica.
//
// The collaborators are read off the accumulator at call time (the same reason
// as coordinationBindings): they are constructed in later build steps than the
// Sweeper, and the Sweeper's Run loop starts only after the whole composition
// root is wired.
//
// spec: §10.1 (coordinator handoff re-adopts the still-running pod;
// CoordinatorFence is the first RPC; no operational RPC before the fence
// acknowledges), §4.7 (Attach content stream stays lazy).
type coordinationReadopter struct {
	w *gatewayWiring
}

// ReadoptAndFence re-establishes the serving binding on this replica after the
// Sweeper's crash-takeover Acquire. The generation the Sweeper bumped is
// re-read by the Fencer from the session row, so this method does not thread it
// through; it names the parameter to satisfy the coordination.Readopter
// contract. A nil collaborator fails closed so the Sweeper publishes no binding
// it cannot back and a peer replica that has the seams re-adopts the pod.
func (r coordinationReadopter) ReadoptAndFence(ctx context.Context, tenantID, sessionID string, generation int64) (func(), error) {
	_ = generation
	if r.w.podBinder == nil || r.w.podRegistry == nil || r.w.coordFencer == nil || r.w.sessions == nil || r.w.coordLeaseStore == nil {
		return nil, fmt.Errorf("gateway: crash-takeover re-adopt seams not wired for session %s", sessionID)
	}
	return readoptAndFence(ctx, r.w.podBinder, r.w.coordFencer, r.w.podRegistry, r.w.sessions, r.w.coordLeaseStore, tenantID, sessionID, r.w.replica)
}

// releaseAfterReadoptFailure relinquishes the coordination lease the Sweeper
// acquired for the crash-takeover after a re-adopt failure that left the lease
// held: a session-row read fault, a pod dial fault, or a best-effort fence
// fault the Fencer did not relinquish. Releasing it surfaces the lease lapse
// for a subsequent sweep to re-adopt the still-running pod rather than pinning
// the lease to a replica that never fenced it. It returns the triggering error
// and, when the release itself faults, appends the release error so the
// fail-closed lapse is not lost silently.
// spec: §10.1 (relinquish-and-backoff; hold state on connection loss).
func releaseAfterReadoptFailure(ctx context.Context, releaser leaseReleaser, tenantID, sessionID, holder string, cause error) error {
	if rerr := releaser.Release(ctx, tenantID, sessionID, holder); rerr != nil {
		return fmt.Errorf("%w; release still-held lease: %v", cause, rerr)
	}
	return cause
}

// readoptAndFence dials the still-running pod, fences it as the first RPC, and
// returns a publish callback the Sweeper invokes only after the fence
// acknowledges. Every failure closes any dialed connection, publishes no
// binding, and returns the error, so the Sweeper records a per-session
// adoption backoff. On every failure that leaves the coordination lease held —
// the session-row read fault and the pod dial fault (both before the fence),
// and a best-effort fence fault the Fencer did not relinquish — this releases
// the lease itself. Only a terminal fence failure arrives with the lease
// already relinquished (relinquish-and-backoff, relinquished=true), so on that
// one path this closes the connection without a redundant release. Releasing
// the still-held lease is what surfaces its lapse for a subsequent sweep to
// re-adopt the still-running pod; without it the lease pins to this replica,
// which never established the serving binding, the next sweep renews it
// forever, and the fenced-by-nothing pod self-terminates in its §10.1 hold
// state at 120s with no recovery. Publishing only after the acknowledgement
// honors the §10.1 precondition that no operational RPC (the executor's first
// Attach) reaches the pod until the fence acknowledges; the held connection
// keeps the pod continuously coordinated so it does not re-enter hold state.
// spec: §10.1 (relinquish-and-backoff; hold state on connection loss), §11.3
// line 209, §4.7.
func readoptAndFence(
	ctx context.Context,
	dialer readoptDialer,
	fencer readoptFencer,
	registry readoptPublisher,
	sessions sandboxNameReader,
	releaser leaseReleaser,
	tenantID, sessionID, holder string,
) (func(), error) {
	row, err := sessions.Get(ctx, tenantID, sessionID)
	if err != nil {
		// A pre-fence failure leaves the lease the Sweeper acquired held by
		// this replica; release it so its lapse surfaces for a subsequent
		// re-adopt rather than pinning the lease to a replica that never fenced
		// the pod. spec: §10.1 (relinquish-and-backoff).
		return nil, releaseAfterReadoptFailure(ctx, releaser, tenantID, sessionID, holder,
			fmt.Errorf("gateway: read session %s for crash-takeover re-adopt: %w", sessionID, err))
	}
	// Dial the pod without the §15.5 version handshake so CoordinatorFence is
	// the first RPC to the hold-state pod.
	sb, adapter, err := dialer.ReadoptConnect(ctx, row.PodAssignment)
	if err != nil {
		// The dial failed before the fence, so the Fencer never ran to
		// relinquish the lease; release the still-held lease here for the same
		// reason as the row-read failure above.
		// spec: §10.1 (relinquish-and-backoff).
		return nil, releaseAfterReadoptFailure(ctx, releaser, tenantID, sessionID, holder,
			fmt.Errorf("gateway: re-adopt dial for session %s: %w", sessionID, err))
	}
	// Fence as the first RPC. On any fence error close the connection and
	// report it so the Sweeper backs off and publishes no binding. A terminal
	// failure (relinquished) already released the lease; a best-effort failure
	// (a coordination_generation read error or a context cancellation) leaves
	// the lease held, so release it here so its lapse surfaces for a subsequent
	// re-adopt. spec: §10.1 (relinquish-and-backoff), §11.3 line 209.
	relinquished, ferr := fencer.Fence(ctx, adapter, tenantID, sessionID)
	if ferr != nil {
		_ = adapter.Close()
		cause := fmt.Errorf("gateway: re-adopt fence for session %s: %w", sessionID, ferr)
		if relinquished {
			return nil, cause
		}
		return nil, releaseAfterReadoptFailure(ctx, releaser, tenantID, sessionID, holder, cause)
	}
	bind := &podsession.BindResult{
		SessionID:   sessionID,
		TenantID:    tenantID,
		SandboxName: row.PodAssignment,
		PodIP:       sb.Status.PodIP,
		Adapter:     adapter,
	}
	return func() { registry.Put(bind) }, nil
}
