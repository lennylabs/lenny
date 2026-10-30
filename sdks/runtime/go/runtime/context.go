// SPDX-License-Identifier: MIT

package runtime

import "context"

// The §15.7 Handler interface keeps OnCreate, OnMessage, and
// OnTerminate to a context and one value argument. The Standard-level
// platform MCP tools and the Full-level CH-RUNTIMEOPS surface reach
// a handler through the context the SDK passes to those methods. This
// keeps the Handler signature stable across integration levels: a
// Basic-level handler ignores the context extras, a Standard-level
// handler reads Tools, a Full-level handler reads both.

type ctxKey int

const (
	ctxKeyTools ctxKey = iota
	ctxKeyLifecycle
	ctxKeyCredentials
	ctxKeyAdapterTools
	ctxKeySession
)

// ToolsFrom returns the §15.7 platform MCP tool surface carried on ctx,
// or nil when the runtime is Basic level or the channel is not open.
// Handlers call it inside OnMessage to invoke §8.5 platform tools.
func ToolsFrom(ctx context.Context) *Tools {
	t, _ := ctx.Value(ctxKeyTools).(*Tools)
	return t
}

// LifecycleFrom returns the §15.4.3 Full-level CH-RUNTIMEOPS
// carried on ctx, or nil when the runtime is not Full level. Handlers
// rarely need it directly; the SDK answers lifecycle events.
func LifecycleFrom(ctx context.Context) *Lifecycle {
	lc, _ := ctx.Value(ctxKeyLifecycle).(*Lifecycle)
	return lc
}

// CredentialsFrom returns the session's credential bundle carried on ctx,
// or nil when the session's session_start named no credential file. The
// bundle is the one the session held when the SDK invoked the Handler
// method; a credentials_rotated event naming the session replaces it for
// later calls.
func CredentialsFrom(ctx context.Context) *CredentialBundle {
	c, _ := ctx.Value(ctxKeyCredentials).(*CredentialBundle)
	return c
}

// HoldBusy reports work the handler does for the session ctx names after
// the handler returns, such as a background task, a tool call made outside
// a turn, or a wait on children. While a hold is unreleased, the SDK
// answers each heartbeat naming the session with busy: true, so the
// platform does not idle-suspend the session and release its pod. Work
// after a handler returns is reported only through a hold, and a release
// of the session's pod can end work that no hold reports.
//
// release drops the hold; a second call does nothing. A context naming no
// live session returns a no-op release. OnCreate, OnMessage, and
// OnTerminate each receive a context that names their session, so work
// started in OnCreate, before any message, takes a hold the same way. The
// session's session_end clears every hold.
//
// spec: §15.7 (Runtime Author SDKs, Busy reporting), §28.5.3 (CH-MSGSOCK).
func HoldBusy(ctx context.Context) (release func()) {
	st, _ := ctx.Value(ctxKeySession).(*sessionState)
	if st == nil {
		return func() {}
	}
	return st.holdBusy()
}

// withSessionContext returns the context the SDK passes to a Handler
// method for st: the session's own context, carrying the process-scoped
// Tools and Lifecycle, the session's credentials, and the session that
// HoldBusy reads.
func (p *process) withSessionContext(st *sessionState) context.Context {
	ctx := context.WithValue(st.ctx, ctxKeyTools, p.tools)
	ctx = context.WithValue(ctx, ctxKeyLifecycle, p.lifecycle)
	ctx = context.WithValue(ctx, ctxKeyCredentials, st.Credentials())
	ctx = context.WithValue(ctx, ctxKeySession, st)
	return ctx
}
