// SPDX-License-Identifier: MIT

package recycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
)

// HoldExpiryGracePeriod pads the per-claim hold-TTL timer so the gateway's
// own precondition-guarded DELETE fires shortly after holdExpiresAt rather
// than racing the wall clock exactly at the deadline. The orphan GC reclaims
// a reserved claim only after holdExpiresAt plus a separate (longer) grace
// (§3.3), so a short local grace here keeps the gateway holder as the
// primary expiry path without colliding with the GC. spec: §3.2
// (precondition-guarded hold-expiry DELETE), §4.6.1 (orphan GC reclaim after
// holdExpiresAt plus grace).
const HoldExpiryGracePeriod = 250 * time.Millisecond

// claimRebinder patches a reserved claim back to bound on a same-tenant
// rebind within the hold window. *podclaim.WriteRebindStatus satisfies it
// through rebindFunc; the seam keeps HoldCoordinator unit-testable without a
// Kubernetes client. spec: §3.2 (reserved → bound rebind).
type claimRebinder func(ctx context.Context, namespace, claimName string, now func() time.Time) error

// claimDeleter performs the precondition-guarded hold-expiry DELETE.
// *podclaim.DeleteOnHoldExpiry satisfies it; aborted reports that a
// cross-replica rebind changed the resourceVersion and won the race. spec:
// §3.2 (rebind-vs-hold-expiry precondition race).
type claimDeleter func(ctx context.Context, namespace, claimName string, hold podclaim.ReservedHold) (aborted bool, err error)

// HoldCoordinator runs the §3.2 reserved-hold timers on a gateway replica.
// After the recycle disposition driver patches a recycled pod's claim to
// `reserved` (S26) it hands the coordinator the ReservedHold token; the
// coordinator arms a per-claim hold-TTL timer and, on expiry, deletes the
// claim with the token's preconditions so the pod returns to `idle`. Within
// the hold window a same-tenant session rebinds the claim (`reserved →
// bound`) through Rebind, which cancels the local timer; a rebind that
// landed on another replica changes the claim's resourceVersion so the
// expiry DELETE fails its precondition and aborts.
//
// The coordinator is replica-local: only the replica that reserved the claim
// holds its token and arms its timer. A different replica may rebind the
// claim (Rebind re-reads the claim after its patch before dispatching, per
// §3.2); the precondition on the holder's DELETE makes the cross-replica
// rebind win the race. If the holding replica crashes, the WarmPoolController
// orphan GC reclaims the reserved claim after holdExpiresAt plus a grace
// (§4.6.1), so a lost in-process timer never strands the pod.
//
// spec: §3.2 (reserved hold, precondition-guarded expiry DELETE,
// rebind-vs-hold-expiry race), §4.6.1 (reserved hold paragraph,
// claimHoldTTLSeconds), §4.6.3 (holdExpiresAt status write).
type HoldCoordinator struct {
	namespace string
	rebind    claimRebinder
	delete    claimDeleter
	now       func() time.Time
	// afterFunc schedules fn to run after d. Injectable so a unit test can
	// drive the timer deterministically; production uses time.AfterFunc.
	afterFunc func(d time.Duration, fn func()) timerHandle
	log       *slog.Logger

	mu    sync.Mutex
	holds map[string]*activeHold
}

// activeHold is a single armed reserved-hold timer: the token its expiry
// DELETE is fenced against and the handle that cancels it on a local rebind
// or on Stop.
type activeHold struct {
	hold  podclaim.ReservedHold
	timer timerHandle
}

// timerHandle cancels a scheduled timer. *time.Timer satisfies it through
// Stop; the seam keeps the afterFunc injection self-contained.
type timerHandle interface {
	Stop() bool
}

// HoldCoordinatorOptions configures a HoldCoordinator.
type HoldCoordinatorOptions struct {
	// Client patches the SandboxClaim status subresource on a rebind and
	// deletes the claim on hold expiry. Required.
	Client client.Client
	// Namespace is the agent namespace the claims live in. Required.
	Namespace string
	// Now overrides the clock for the rebind transition stamp and the
	// remaining-hold computation; nil uses wall time.
	Now func() time.Time
	// AfterFunc overrides the timer scheduler for tests; nil uses
	// time.AfterFunc. The returned handle's Stop cancels the timer.
	AfterFunc func(d time.Duration, fn func()) timerHandle
	// Logger records expiry and rebind outcomes; nil resolves to
	// slog.Default().
	Logger *slog.Logger
}

// NewHoldCoordinator builds the §3.2 reserved-hold coordinator. Client and
// Namespace are required. The concrete rebind and delete seams are wired to
// the podclaim binding-state writers (WriteRebindStatus, DeleteOnHoldExpiry).
func NewHoldCoordinator(opts HoldCoordinatorOptions) (*HoldCoordinator, error) {
	if opts.Client == nil {
		return nil, errors.New("recycle: HoldCoordinator Client is required")
	}
	if opts.Namespace == "" {
		return nil, errors.New("recycle: HoldCoordinator Namespace is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	afterFunc := opts.AfterFunc
	if afterFunc == nil {
		afterFunc = func(d time.Duration, fn func()) timerHandle {
			return time.AfterFunc(d, fn)
		}
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	cl := opts.Client
	ns := opts.Namespace
	return &HoldCoordinator{
		namespace: ns,
		rebind: func(ctx context.Context, namespace, claimName string, n func() time.Time) error {
			return podclaim.WriteRebindStatus(ctx, cl, namespace, claimName, n)
		},
		delete: func(ctx context.Context, namespace, claimName string, hold podclaim.ReservedHold) (bool, error) {
			return podclaim.DeleteOnHoldExpiry(ctx, cl, namespace, claimName, hold)
		},
		now:       now,
		afterFunc: afterFunc,
		log:       log,
		holds:     make(map[string]*activeHold),
	}, nil
}

// Hold arms the §3.2 hold-TTL timer for a freshly reserved claim. podID is
// the agent pod (the claim is claim-<podID>); hold carries the token the
// expiry DELETE is fenced against and the holdExpiresAt deadline stamped at
// the reserved patch. The timer fires at holdExpiresAt plus a short grace,
// after which the coordinator deletes the claim with the token's
// preconditions, returning the pod to `idle`.
//
// Hold is idempotent on the claim key: a second Hold for the same claim
// (a re-reservation after a rebind, or a duplicate disposition) cancels the
// prior timer and re-arms against the new token, so the expiry DELETE always
// carries the resourceVersion of the most recent reserved patch.
//
// spec: §3.2 (per-claim hold-TTL timer, precondition-guarded expiry DELETE),
// §4.6.1 (claimHoldTTLSeconds, reserved hold paragraph).
func (c *HoldCoordinator) Hold(podID string, hold podclaim.ReservedHold) {
	claimName := podclaim.ClaimName(podID)
	delay := c.holdDelay(hold.HoldExpiresAt)

	c.mu.Lock()
	defer c.mu.Unlock()
	// Cancel any prior timer for this claim so a re-reservation does not
	// leave two timers racing to delete the same claim with different
	// resourceVersions.
	if prior, ok := c.holds[claimName]; ok {
		prior.timer.Stop()
	}
	entry := &activeHold{hold: hold}
	entry.timer = c.afterFunc(delay, func() { c.expire(claimName) })
	c.holds[claimName] = entry
}

// holdDelay returns the wall-clock delay until the expiry DELETE should fire:
// the time remaining until holdExpiresAt plus a short grace, floored at the
// grace alone so an already-expired deadline (a slow reserved patch, or a
// re-armed hold after the clock advanced) fires promptly rather than
// scheduling a non-positive timer. spec: §3.2.
func (c *HoldCoordinator) holdDelay(holdExpiresAt time.Time) time.Duration {
	remaining := holdExpiresAt.Sub(c.now())
	if remaining < 0 {
		remaining = 0
	}
	return remaining + HoldExpiryGracePeriod
}

// Rebind patches a held claim back to `bound` for a same-tenant session
// arriving within the hold window and cancels this replica's expiry timer.
// The patch changes the claim's resourceVersion, so any other replica's
// expiry DELETE fenced on the reserved-patch version fails its precondition
// and aborts (§3.2). The caller re-reads the claim after this returns before
// dispatching, per §3.2.
//
// Rebind cancels the local timer before the patch so a timer that is already
// firing concurrently is detached; the precondition guard on the in-flight
// DELETE then aborts it because the rebind changed the resourceVersion. A
// rebind on a claim this replica does not hold (the reserving replica is a
// peer) still patches the claim — any replica may rebind — and is a no-op on
// the local timer map.
//
// spec: §3.2 (reserved → bound rebind, no acquisition round trip, any replica
// may rebind), §4.6.1 (within-hold rebind).
func (c *HoldCoordinator) Rebind(ctx context.Context, podID string) error {
	claimName := podclaim.ClaimName(podID)
	c.cancel(claimName)
	if err := c.rebind(ctx, c.namespace, claimName, c.now); err != nil {
		return fmt.Errorf("recycle: rebind reserved claim %s: %w", claimName, err)
	}
	return nil
}

// expire is the timer callback: it deletes the claim with the token's
// preconditions, returning the pod to `idle`. A precondition failure means a
// cross-replica rebind won the race; the claim is left intact and the entry
// is dropped without error. The entry is removed under the lock first so a
// concurrent Rebind or Hold for the same claim is not clobbered by a stale
// timer.
func (c *HoldCoordinator) expire(claimName string) {
	c.mu.Lock()
	entry, ok := c.holds[claimName]
	if ok {
		delete(c.holds, claimName)
	}
	c.mu.Unlock()
	if !ok {
		// Cancelled by a local Rebind or Stop between the timer firing and
		// this callback acquiring the lock; nothing to delete.
		return
	}

	// The expiry DELETE runs on its own bounded context: the timer fires
	// outside any request scope, so a parent deadline would be unbounded.
	ctx, cancel := context.WithTimeout(context.Background(), holdExpiryDeleteTimeout)
	defer cancel()
	aborted, err := c.delete(ctx, c.namespace, claimName, entry.hold)
	switch {
	case err != nil:
		c.log.LogAttrs(
			ctx, slog.LevelWarn, "recycle: hold-expiry delete failed",
			slog.String("claim", claimName),
			slog.String("err", err.Error()),
		)
	case aborted:
		// A rebind from any replica changed the resourceVersion; the rebound
		// claim is left intact and the pod stays claimed. spec: §3.2.
		c.log.LogAttrs(
			ctx, slog.LevelDebug, "recycle: hold-expiry delete aborted by rebind",
			slog.String("claim", claimName),
		)
	default:
		c.log.LogAttrs(
			ctx, slog.LevelDebug, "recycle: reserved hold expired, pod returned to idle",
			slog.String("claim", claimName),
		)
	}
}

// holdExpiryDeleteTimeout bounds the precondition-guarded DELETE the timer
// callback issues. The DELETE is a single API-server call; the bound keeps
// the timer goroutine from blocking indefinitely on a stalled apiserver. It
// is not operator-tunable because it bounds one control-plane call rather
// than a spec-fixed window.
const holdExpiryDeleteTimeout = 10 * time.Second

// cancel stops and forgets the local timer for claimName, if this replica
// holds it. It is the local half of a rebind: the precondition guard is the
// authoritative cross-replica race resolver.
func (c *HoldCoordinator) cancel(claimName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.holds[claimName]; ok {
		entry.timer.Stop()
		delete(c.holds, claimName)
	}
}

// Cancel stops this replica's armed expiry timer for the pod's claim without
// patching the claim. It is the local half of an acquisition-path rebind:
// when the rebind patch is performed elsewhere (the SlotClaimer / Claimer
// rebind branch already changed the claim's resourceVersion, so the holder's
// expiry DELETE would abort on its precondition anyway), Cancel drops the
// now-stale timer so it does not issue a wasted no-op DELETE. A claim this
// replica does not hold (the reserving replica is a peer) is a no-op; the
// peer's precondition guard is the authoritative race resolver. spec: §3.2.
func (c *HoldCoordinator) Cancel(podID string) {
	c.cancel(podclaim.ClaimName(podID))
}

// Holds reports whether this replica currently holds an armed expiry timer
// for the pod's claim. It exists for the §3.2 acquisition-path rebind branch
// and for tests; it does not consult the API server, so a claim reserved by a
// peer replica reports false here even though it is reserved in etcd.
func (c *HoldCoordinator) Holds(podID string) bool {
	claimName := podclaim.ClaimName(podID)
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.holds[claimName]
	return ok
}

// Stop cancels every armed timer. It is called on gateway shutdown so the
// in-process timers do not fire against a draining client. The reserved
// claims it abandons are reclaimed by the orphan GC after holdExpiresAt plus
// a grace (§4.6.1), so a clean shutdown never strands a pod.
func (c *HoldCoordinator) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, entry := range c.holds {
		entry.timer.Stop()
		delete(c.holds, name)
	}
}

// HoldRegistrar is the §3.2 seam the recycle disposition driver hands a
// freshly reserved claim's hold token to. *HoldCoordinator satisfies it. The
// driver reserves the claim (WriteReservedStatus) and then registers the hold
// so the coordinator owns the expiry timer; the seam keeps the driver free of
// the coordinator's timer state. spec: §3.2 (reserved hold timer ownership).
type HoldRegistrar interface {
	Hold(podID string, hold podclaim.ReservedHold)
}

var _ HoldRegistrar = (*HoldCoordinator)(nil)
