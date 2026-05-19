// SPDX-License-Identifier: MIT

package runtime

import "context"

// The §15.7 Handler interface keeps OnCreate, OnMessage, and
// OnTerminate to a context and one value argument. The Standard-level
// platform MCP tools and the Full-level lifecycle channel reach a
// handler through the context the SDK passes to those methods. This
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

// LifecycleFrom returns the §15.4.3 Full-level lifecycle channel
// carried on ctx, or nil when the runtime is not Full level. Handlers
// rarely need it directly; the SDK answers lifecycle events.
func LifecycleFrom(ctx context.Context) *Lifecycle {
	lc, _ := ctx.Value(ctxKeyLifecycle).(*Lifecycle)
	return lc
}

// CredentialsFrom returns the current §4.7 credential bundle carried on
// ctx, or nil when the runtime's pool has no active lease. The bundle
// reflects the most recent rotation the lifecycle channel processed.
func CredentialsFrom(ctx context.Context) *CredentialBundle {
	c, _ := ctx.Value(ctxKeyCredentials).(*CredentialBundle)
	return c
}

// withSessionContext attaches the session-scoped Tools, Lifecycle, and
// credentials to ctx before the SDK invokes a Handler method.
func (s *session) withSessionContext(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, ctxKeyTools, s.tools)
	ctx = context.WithValue(ctx, ctxKeyLifecycle, s.lifecycle)
	ctx = context.WithValue(ctx, ctxKeyCredentials, s.Credentials())
	return ctx
}
