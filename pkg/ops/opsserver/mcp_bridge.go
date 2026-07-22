// SPDX-License-Identifier: MIT

package opsserver

import (
	"context"
	"net/http"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/mcp"
)

// mcpBridge is the §25.12 middleware that sits between the withOpsAuth
// authentication wrapper and the mounted /mcp/management server. It does
// two things on every request, both derived from the Principal the auth
// middleware attached to the context:
//
//  1. It bridges the authenticated RFC 9068 scope claim onto the MCP
//     request context so the §25.12 MCP-boundary scope gate sees the JWT
//     claim. Without this bridge a Bearer-JWT caller's scopes never reach
//     the pre-dispatch gate, which only reads the X-Lenny-Scope dev
//     header, so a scope-narrowed JWT is enforced at the REST-replay gate
//     but not at the MCP boundary the spec names ("A caller whose scope
//     doesn't match receives 403 SCOPE_FORBIDDEN from the MCP adapter
//     before any REST call is issued").
//
//  2. It captures the caller identity the gateway proxy forwards on a
//     gateway-owned tool call: the raw Authorization bearer when present,
//     or the dev-mode identity headers the gateway auth layer consumes
//     under AllowDevHeaders. The invoker reads it back with
//     MCPCallerIdentityHeaders and forwards it verbatim so the gateway
//     re-authorizes as the actual caller.
//
// The middleware only carries and enriches context; wiring it onto the
// mount is done where s.mcp is served.
//
// spec: §25.12 (Security Model layer 2; Headers and Correlation).
type mcpBridge struct {
	inner http.Handler
	// allowDevHeaders mirrors AuthConfig.Options.AllowDevHeaders. It gates
	// the dev-mode identity-header fallback: when false, only a real
	// Authorization bearer is captured for forwarding, so an
	// unauthenticated client-controlled identity header is never
	// propagated to the gateway (fail closed on the identity-forwarding
	// path).
	allowDevHeaders bool
}

// newMCPBridge wraps the mounted §25.12 MCP server in the scope-bridge
// and caller-identity middleware. allowDevHeaders comes from the auth
// configuration and gates the dev-mode identity-header capture.
func newMCPBridge(inner http.Handler, allowDevHeaders bool) http.Handler {
	return &mcpBridge{inner: inner, allowDevHeaders: allowDevHeaders}
}

// ServeHTTP enriches the request context and serves the wrapped MCP
// server. It attaches a context scope set only when the Principal carries
// a present scope claim, so the AllowDevHeaders posture (which attaches a
// Principal with a zero Scopes set) and the no-AuthConfig dev path both
// fall through to the X-Lenny-Scope header fallback that requestScopes
// applies. An absent JWT scope claim applies no narrowing per §25.1.
//
// spec: §25.12 (Security Model layer 2), §25.1 (absent-claim semantics).
func (b *mcpBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if p, ok := authmw.FromContext(ctx); ok && p.Scopes.Present() {
		// Bridge the validated JWT scope claim onto the MCP context so the
		// MCP-boundary gate reads the RFC 9068 space-separated claim rather
		// than only the X-Lenny-Scope dev header.
		ctx = mcp.WithScopes(ctx, p.Scopes)
	}
	ctx = withMCPCallerIdentity(ctx, b.callerIdentityHeaders(r))
	b.inner.ServeHTTP(w, r.WithContext(ctx))
}

// callerIdentityHeaders assembles the identity headers the gateway proxy
// forwards for a gateway-owned tool. A present Authorization bearer is
// forwarded verbatim and takes precedence; the gateway re-authorizes the
// real caller from it. Absent a bearer, the dev-mode identity headers the
// gateway auth layer consumes (X-Lenny-Tenant-ID, X-Lenny-Roles, and
// X-Lenny-Groups) are forwarded, but only when AllowDevHeaders is set, so
// unauthenticated client-controlled identity headers cannot reach the
// gateway in a production posture. The singular X-Lenny-Role and the
// X-Lenny-Scope header are intentionally excluded: the gateway auth layer
// reads neither for its dev-header RBAC decision.
//
// spec: §25.12 (Headers and Correlation; the authenticated identity from
// the MCP session maps to the Authorization header on gateway calls).
func (b *mcpBridge) callerIdentityHeaders(r *http.Request) http.Header {
	h := http.Header{}
	if authz := r.Header.Get("Authorization"); authz != "" {
		h.Set("Authorization", authz)
		return h
	}
	if !b.allowDevHeaders {
		return h
	}
	for _, name := range []string{"X-Lenny-Tenant-ID", "X-Lenny-Roles", "X-Lenny-Groups"} {
		if v := r.Header.Get(name); v != "" {
			h.Set(name, v)
		}
	}
	return h
}

// mcpCallerIdentityCtxKey is the opsserver-internal context key under
// which the bridge stashes the forwarded caller-identity headers. The
// key type is unexported so only this package writes the value.
type mcpCallerIdentityCtxKey struct{}

// withMCPCallerIdentity returns a copy of ctx carrying the caller-identity
// headers the gateway proxy forwards.
func withMCPCallerIdentity(ctx context.Context, h http.Header) context.Context {
	return context.WithValue(ctx, mcpCallerIdentityCtxKey{}, h)
}

// MCPCallerIdentityHeaders returns the §25.12 caller-identity headers the
// bridge stashed for the gateway proxy, or nil when the request did not
// pass through the bridge (for example an in-process invocation with no
// forwarded identity). The gateway invoker forwards the returned headers
// verbatim on a gateway-owned tool call.
//
// spec: §25.12 (Headers and Correlation).
func MCPCallerIdentityHeaders(ctx context.Context) http.Header {
	h, _ := ctx.Value(mcpCallerIdentityCtxKey{}).(http.Header)
	return h
}
