// SPDX-License-Identifier: MIT

package translator

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// SingleShotBinder runs the shared session create-and-start service for
// one built-in-adapter request: it applies the admission gates, claims a
// warm pod, launches the runtime, and registers the pod binding, returning
// the created session id. A pool or credential exhaustion, or an
// admission-gate rejection, is returned as a *SingleShotError carrying the
// HTTP status, error code, and Retry-After the adapter surfaces in its
// native envelope.
//
// The interface is defined here, at the consumer, because
// pkg/gateway/sessionserver imports this package; the translator therefore
// cannot import sessionserver without an import cycle. The gateway wiring
// binds this interface to a sessionserver-backed implementation.
//
// spec: §15 built-in adapter single-shot compute model.
type SingleShotBinder interface {
	BindSingleShot(ctx context.Context, spec SingleShotSpec) (sessionID string, err error)
}

// SingleShotSpec carries the adapter-resolved fields the binder maps into
// a sessionserver create-and-start request. It carries no lineage pointer:
// the create-and-start reuse surface has no parent field, and setting the
// row's ParentSessionID would collide with the §8.2/§8.6 delegated-child
// credential-lease semantics, so an Open Responses previous_response_id is
// not threaded through the single-shot path.
//
// spec: §15 built-in adapter single-shot compute model.
type SingleShotSpec struct {
	TenantID    string
	UserID      string
	RuntimeRef  string
	Environment string
}

// SingleShotError is the typed binder failure the adapter maps into its
// native error envelope. RetryAfterSeconds is the value the adapter emits
// as a Retry-After header when it is positive (the retryable pool-claim
// exhaustion case); it is zero for a non-retryable failure such as a
// credential-pool-exhaustion pre-check miss, which carries no header.
//
// spec: §15 built-in adapter single-shot compute model.
type SingleShotError struct {
	HTTPStatus        int
	Code              string
	Message           string
	RetryAfterSeconds int
	Retryable         bool
}

func (e *SingleShotError) Error() string { return e.Code + ": " + e.Message }

// noopSingleShotBinder is the default SingleShotBinder a handler falls
// back to when no binder is injected (the pure in-process translator unit
// tests and the §17.4 in-memory mode). It generates a session id and
// persists a minimal running session row through the handler's own store,
// replicating the inline store.Create the handlers performed before the
// single-shot bind path existed. It claims no pod, so a dispatch against
// the in-memory EchoExecutor still round-trips on the one code path.
//
// spec: §15 built-in adapter single-shot compute model; §17.4 in-memory mode.
type noopSingleShotBinder struct {
	store sessionstore.Store
	idFn  func() string
	clock func() time.Time
}

// newNoopSingleShotBinder builds the default no-op binder over a handler's
// injected store, id generator, and clock so the persisted row matches the
// handler's own identity and timestamps.
func newNoopSingleShotBinder(store sessionstore.Store, idFn func() string, clock func() time.Time) *noopSingleShotBinder {
	return &noopSingleShotBinder{store: store, idFn: idFn, clock: clock}
}

// BindSingleShot generates a session id and persists a running row,
// returning the id for the subsequent dispatch. It never claims a pod.
//
// spec: §15 built-in adapter single-shot compute model.
func (b *noopSingleShotBinder) BindSingleShot(ctx context.Context, spec SingleShotSpec) (string, error) {
	sessionID := b.idFn()
	now := b.clock()
	row := sessionstore.Session{
		ID:          sessionID,
		TenantID:    spec.TenantID,
		UserID:      spec.UserID,
		RuntimeRef:  spec.RuntimeRef,
		Environment: spec.Environment,
		State:       session.StateRunning,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := b.store.Create(ctx, row); err != nil {
		return "", fmt.Errorf("single-shot session create %s: %w", sessionID, err)
	}
	return sessionID, nil
}
