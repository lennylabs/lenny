// SPDX-License-Identifier: MIT

package idempotencymw

import (
	"context"
	"sync/atomic"
)

// SkipReasonForwardRefused is the lenny_idempotency_cache_skipped_total
// reason the middleware records when a handler marked its response as not
// to be stored because a forward to the coordinating replica was refused.
const SkipReasonForwardRefused = "forward_refused"

// SkipReasonCredential is the lenny_idempotency_cache_skipped_total reason
// the middleware records when a handler marked its response as not to be
// stored because the response carries a credential the store cannot hold.
const SkipReasonCredential = "credential"

// gate is the per-request no-store mark. A handler sets refused through
// DoNotStore and credential through DoNotStoreCredential; the middleware
// reads both after the inner handler returns. The inter-replica forward
// listener reads refused alone, so a credential mark never reports a
// forward refusal.
type gate struct{ refused, credential atomic.Bool }

type gateCtxKey struct{}

// WithGate returns a copy of ctx carrying a fresh no-store gate. The
// middleware installs one on every request it may store, and the
// inter-replica forward listener installs one on every forwarded request so
// that it can report a refusal back to the forwarding replica.
//
// spec: §11.5 (Idempotency) Mechanism, §29.3 (Interactive message send)
// Forwarding rules 6 and 7
func WithGate(ctx context.Context) context.Context {
	return context.WithValue(ctx, gateCtxKey{}, &gate{})
}

// DoNotStore marks the request's response as not to be stored, so a retry
// with the same Idempotency-Key runs the operation again. It does nothing
// when ctx carries no gate.
//
// spec: §11.5 (Idempotency) Mechanism — a TARGET_NOT_READY response that
// §29.3 Forwarding rule 6 or rule 7 produces is not stored.
func DoNotStore(ctx context.Context) {
	if g, ok := ctx.Value(gateCtxKey{}).(*gate); ok {
		g.refused.Store(true)
	}
}

// DoNotStoreCredential marks the request's response as not to be stored
// because it carries a credential that a replay cannot mint again from a
// redaction reference, so a retry with the same Idempotency-Key runs the
// operation again and issues a new credential. It does nothing when ctx
// carries no gate.
//
// spec: §11.5 (Idempotency)
func DoNotStoreCredential(ctx context.Context) {
	if g, ok := ctx.Value(gateCtxKey{}).(*gate); ok {
		g.credential.Store(true)
	}
}

// credentialNotStored reports whether DoNotStoreCredential was called on
// ctx's gate.
func credentialNotStored(ctx context.Context) bool {
	g, ok := ctx.Value(gateCtxKey{}).(*gate)
	return ok && g.credential.Load()
}

// NotStored reports whether DoNotStore was called on ctx's gate. It is
// false when ctx carries no gate.
func NotStored(ctx context.Context) bool {
	g, ok := ctx.Value(gateCtxKey{}).(*gate)
	return ok && g.refused.Load()
}
