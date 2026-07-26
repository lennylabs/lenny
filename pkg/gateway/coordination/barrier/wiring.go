// SPDX-License-Identifier: MIT

package barrier

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// defaultTargetReadTimeout is the §10.1 line 165 deadline on the
// barrier-target Postgres read: the fan-out must not consume measurable
// drain budget, so the read falls back to the in-memory lease cache on
// expiry.
const defaultTargetReadTimeout = 2 * time.Second

// PodDispatcher is the production Dispatcher: it routes a CheckpointBarrier
// to a session's pod adapter over the live in-replica connection the
// gateway already holds for a session it coordinates. A generation-stale
// rejection (codes.FailedPrecondition) is surfaced as ErrGenerationStale so
// Dispatch records it without aborting the drain.
//
// The co-located coordination model keeps the lease holder and the pod
// binding on one replica, so the coordinator always reaches the pod through
// its held connection and the barrier needs no fresh-dial fallback.
//
// spec: §10.1 lines 165-167.
type PodDispatcher struct {
	// Conn resolves the live adapter connection the gateway already holds
	// for a session it coordinates. ok is false when no local binding
	// exists, and the Dispatcher then records the target unreachable.
	Conn func(sessionID string) (*adapterclient.Client, bool)
}

var _ Dispatcher = (*PodDispatcher)(nil)

// Send dispatches the barrier to the target's pod and returns the ack.
func (d *PodDispatcher) Send(ctx context.Context, t Target, barrierID string) (Ack, error) {
	cl, err := d.resolve(t)
	if err != nil {
		return Ack{}, err
	}
	res, err := cl.CheckpointBarrier(ctx, t.SessionID, t.CoordinationGeneration, barrierID)
	if err != nil {
		if status.Code(err) == codes.FailedPrecondition {
			return Ack{}, ErrGenerationStale
		}
		return Ack{}, err
	}
	return Ack{
		CheckpointRef: res.CheckpointRef,
	}, nil
}

// resolve returns the held adapter connection for the target, or an error
// when this replica holds no live binding for the session.
func (d *PodDispatcher) resolve(t Target) (*adapterclient.Client, error) {
	if d.Conn != nil {
		if c, ok := d.Conn(t.SessionID); ok && c != nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("barrier: no adapter connection for session %s", t.SessionID)
}

// MirrorTargetLister is the production TargetLister: it enumerates the
// §10.1 line 165 barrier-target set from the coordination_lease Postgres
// mirror under a 2-second deadline (source "postgres"), and falls back to
// the in-memory lease cache (source "cache_fallback") when the mirror is
// unset, errors, or exceeds the deadline.
//
// spec: §10.1 line 165.
type MirrorTargetLister struct {
	// ReplicaID is this gateway replica's id — the coordinator_replica the
	// barrier-target query filters on.
	ReplicaID string
	// Mirror is the coordination_lease store. Nil takes the cache fallback
	// directly.
	Mirror coordlease.Store
	// Fallback enumerates the barrier-target set from the in-memory lease
	// cache (the registry snapshot). Used when the mirror read fails.
	Fallback func() []Target
	// Timeout overrides the 2-second mirror-read deadline. Zero selects
	// defaultTargetReadTimeout.
	Timeout time.Duration
}

var _ TargetLister = (*MirrorTargetLister)(nil)

// Targets returns the barrier-target set and its source label.
func (l *MirrorTargetLister) Targets(ctx context.Context) ([]Target, string, error) {
	if l.Mirror != nil {
		d := l.Timeout
		if d <= 0 {
			d = defaultTargetReadTimeout
		}
		cctx, cancel := context.WithTimeout(ctx, d)
		leases, err := l.Mirror.ListHeldByReplica(cctx, l.ReplicaID)
		cancel()
		if err == nil {
			out := make([]Target, 0, len(leases))
			for _, le := range leases {
				out = append(out, Target{
					TenantID:               le.TenantID,
					SessionID:              le.SessionID,
					CoordinationGeneration: le.CoordinationGeneration,
				})
			}
			return out, SourcePostgres, nil
		}
		// §10.1 line 165 — a degraded barrier set from the in-memory cache
		// is strictly better than none when the replica is terminating.
	}
	if l.Fallback != nil {
		return l.Fallback(), SourceCacheFallback, nil
	}
	return nil, SourceCacheFallback, nil
}
