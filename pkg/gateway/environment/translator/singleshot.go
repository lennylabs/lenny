// SPDX-License-Identifier: MIT

package translator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// defaultSingleShotReleaseTimeout bounds the detached context the deferred
// single-shot pod release runs under. The release runs after the request
// context is cancelled (client disconnect or request-context timeout), so it
// carries its own timeout rather than inheriting the request deadline. It is
// operator-tunable through the adapter options' ReleaseTimeout field.
//
// spec: §6.2 release; §15 built-in adapter single-shot compute model.
const defaultSingleShotReleaseTimeout = 30 * time.Second

// SingleShotBinder runs the shared session create-and-start service for
// one built-in-adapter request: it applies the admission gates, claims a
// warm pod, launches the runtime, and registers the pod binding, returning
// the created session's binding. A pool or credential exhaustion, or an
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
	BindSingleShot(ctx context.Context, spec SingleShotSpec) (SingleShotBinding, error)
}

// SingleShotBinding is what a successful bind returns: the created session's
// id, its recovery_generation at creation, and whether the session is held
// after a turn that succeeds. RecoveryGeneration conditions the completion
// write of the session's first Open Responses turn. Hold is true only when the
// session's conversationContinuity is "platform" and its runtime, with the
// session tenant's capabilities applied, declares multi_turn interaction with
// injection supported. The Chat Completions handler ignores Hold, because a
// Chat Completions session is single-shot whatever its runtime declares.
//
// spec: §15 (Open Responses conversation sessions), §15 (Built-in adapter
// single-shot compute model)
type SingleShotBinding struct {
	SessionID          string
	RecoveryGeneration int64
	Hold               bool
}

// HolderForwarder forwards an Open Responses warm turn to the coordinating
// replica of the session that holds its pod. ForwardResponsesTurn reports
// local when this replica serves the turn itself; otherwise it has already
// written the response (the coordinator's answer, or a refusal written
// through refuse) and the handler writes nothing more. Forwarded reports
// whether the request arrived as a forward from another replica.
//
// The interface is defined at the consumer because the translator cannot
// import pkg/gateway/sessionserver; the gateway wiring binds it to the
// session server.
//
// spec: §29.3 (Interactive message send) Forwarding; §15 (Open Responses
// conversation sessions)
type HolderForwarder interface {
	ForwardResponsesTurn(w http.ResponseWriter, r *http.Request, row sessionstore.Session, body []byte,
		refuse func(w http.ResponseWriter, currentState string)) (local bool)
	Forwarded(ctx context.Context) bool
}

// SessionTerminal is the terminal-claim and post-terminal-hook surface the
// Open Responses handler uses to end a held session whose turn failed:
// BeginTerminal opens the session's terminal claim and returns its end, and
// AfterTerminalWrite runs the post-terminal hook, which releases the pod and
// its credential lease. The session server satisfies it.
//
// spec: §15 (Open Responses conversation sessions); §7.2 (Interactive Session
// Model)
type SessionTerminal interface {
	BeginTerminal(sessionID string) (end func())
	AfterTerminalWrite(ctx context.Context, fromState session.State, sess sessionstore.Session,
		signals func(context.Context, sessionstore.Session)) sessionstore.Session
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
// returning the id for the subsequent dispatch. It never claims a pod, so it
// never holds the session.
//
// spec: §15 built-in adapter single-shot compute model; §15 (Open Responses
// conversation sessions).
func (b *noopSingleShotBinder) BindSingleShot(ctx context.Context, spec SingleShotSpec) (SingleShotBinding, error) {
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
		// spec: §12.8 (Lifecycle fence and drain) — a fence refusal is the
		// native 403 carrying the refusal's code, rather than a 500.
		var ref *sessionstore.CreationRefusal
		if errors.As(err, &ref) {
			return SingleShotBinding{}, &SingleShotError{HTTPStatus: http.StatusForbidden, Code: ref.Code, Message: ref.Message}
		}
		return SingleShotBinding{}, fmt.Errorf("single-shot session create %s: %w", sessionID, err)
	}
	return SingleShotBinding{SessionID: sessionID}, nil
}
