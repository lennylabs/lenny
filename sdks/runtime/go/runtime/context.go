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

// withSessionContext returns the context the SDK passes to a Handler
// method for st: the session's own context, carrying the process-scoped
// Tools and Lifecycle and the session's credentials.
func (p *process) withSessionContext(st *sessionState) context.Context {
	ctx := context.WithValue(st.ctx, ctxKeyTools, p.tools)
	ctx = context.WithValue(ctx, ctxKeyLifecycle, p.lifecycle)
	ctx = context.WithValue(ctx, ctxKeyCredentials, st.Credentials())
	return ctx
}
