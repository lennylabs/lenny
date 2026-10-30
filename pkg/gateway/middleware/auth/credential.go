// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"net/http"
)

// Credential is the client credential the middleware verified for a
// request. Authorization holds the Authorization header value the client
// presented ("Bearer <token>"), and DevHeaders holds the X-Lenny-* identity
// headers a dev-header request carried. At most one of the two is set.
//
// The middleware records it so that a replica that does not coordinate a
// session can relay the credential to the coordinating replica, which
// authenticates it again rather than trusting a principal the forwarding
// replica asserts. A request the in-process service layer builds, which
// carries no Authorization header, finds the credential on its context.
//
// A Credential is secret material. It is never written to a log line, an
// audit record, or an error message.
//
// spec: §29.3 (Interactive message send) Forwarding rule 2 and rule 3,
// §13.3 (Credential Flow)
type Credential struct {
	Authorization string
	DevHeaders    http.Header
}

// Empty reports whether c carries no credential.
func (c Credential) Empty() bool {
	return c.Authorization == "" && len(c.DevHeaders) == 0
}

type credentialCtxKey struct{}

// WithCredential returns a copy of ctx carrying c.
func WithCredential(ctx context.Context, c Credential) context.Context {
	return context.WithValue(ctx, credentialCtxKey{}, c)
}

// CredentialFrom returns the credential recorded on ctx, and false when the
// request was admitted without one.
func CredentialFrom(ctx context.Context) (Credential, bool) {
	c, ok := ctx.Value(credentialCtxKey{}).(Credential)
	if !ok || c.Empty() {
		return Credential{}, false
	}
	return c, true
}

// PresentsCredential reports whether h carries a client credential: an
// Authorization header or an X-Lenny-* identity header.
func PresentsCredential(h http.Header) bool {
	if h.Get("Authorization") != "" {
		return true
	}
	for _, name := range identityHeaders {
		if h.Get(name) != "" {
			return true
		}
	}
	return false
}

// bearerCredential records the Authorization header of a verified bearer.
func bearerCredential(r *http.Request) Credential {
	return Credential{Authorization: r.Header.Get("Authorization")}
}

// devHeaderCredential copies the identity headers a dev-header request
// presented, before the middleware rewrites X-Lenny-Tenant-ID with the
// resolved tenant. An absent header stays absent, so a single-tenant
// request that named no tenant resolves to the default tenant again on
// the coordinating replica.
func devHeaderCredential(r *http.Request) Credential {
	h := http.Header{}
	for _, name := range identityHeaders {
		if v := r.Header.Values(name); len(v) > 0 {
			h[http.CanonicalHeaderKey(name)] = append([]string(nil), v...)
		}
	}
	return Credential{DevHeaders: h}
}
