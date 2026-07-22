// SPDX-License-Identifier: MIT

// Package serviceidentity admits the platform's own service accounts to the
// gateway admin API.
//
// §25.4 states that `lenny-ops` calls the gateway's admin API as a regular
// authenticated HTTPS client, using a dedicated service account
// (`lenny-ops-sa`) holding the platform-admin role, and that all such calls go
// through the gateway's standard RBAC with no backdoor and no loopback
// shortcut. The credential that account presents is the projected Kubernetes
// ServiceAccount token the chart mounts, which is signed by the cluster's
// service-account issuer rather than by the platform's token service and
// carries no roles claim. This package resolves such a token into the
// principal the deployment grants the account, so the standard role gates on
// the admin routes admit it.
//
// The §25.5 Redis-down read path depends on it: the gateway event-buffer
// fan-out `lenny-ops` serves the case-1 fall-back from is one of those admin
// routes, and without an admitted principal every replica refuses the query
// and the fall-back has no data source at all.
package serviceidentity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// SATokenAuthenticator validates a projected ServiceAccount token for this
// deployment's audience and reports the ServiceAccount username the cluster
// authenticated it as. The production implementation submits a Kubernetes
// TokenReview, so the apiserver checks the issuer signature, the expiry, and
// the audience binding and a pod cannot forge or extend the token
// (§10.2 line 227). It is a consumer-side interface: a test substitutes a
// recording double without a cluster.
type SATokenAuthenticator interface {
	VerifyUser(ctx context.Context, token, audience string) (username string, err error)
}

// Config configures a Resolver.
type Config struct {
	// Verifier validates the presented token. A nil verifier resolves
	// nothing, which leaves the admin API reachable by Lenny-minted bearers
	// only.
	Verifier SATokenAuthenticator
	// Audience is the deployment audience the projected token must be minted
	// for. An empty audience resolves nothing: without it, a token minted for
	// any audience by the same cluster issuer would be admitted, so the
	// unconfigured state fails closed rather than open.
	Audience string
	// Roles maps a fully-qualified ServiceAccount username
	// (`system:serviceaccount:<namespace>:<name>`) onto the §10.2 roles the
	// deployment grants it. An account absent from the map is not admitted.
	Roles map[string][]auth.Role
	// TenantID is the tenant the admitted service principal acts in.
	TenantID string
}

// Resolver admits an allowlisted platform service account. It satisfies
// authmw.ServiceIdentityResolver.
type Resolver struct {
	verifier SATokenAuthenticator
	audience string
	roles    map[string][]auth.Role
	tenantID string
}

// New returns a Resolver over cfg. A configuration that cannot admit anything
// (no verifier, no audience, or no granted account) returns a Resolver that
// resolves nothing rather than an error, so a deployment that has not
// configured the service-account grant simply keeps the admin API on
// Lenny-minted bearers.
func New(cfg Config) *Resolver {
	roles := make(map[string][]auth.Role, len(cfg.Roles))
	for user, rs := range cfg.Roles {
		roles[user] = append([]auth.Role(nil), rs...)
	}
	return &Resolver{
		verifier: cfg.Verifier,
		audience: cfg.Audience,
		roles:    roles,
		tenantID: cfg.TenantID,
	}
}

// ResolveService implements authmw.ServiceIdentityResolver. It fails closed:
// an unconfigured resolver, a token the cluster does not authenticate for this
// audience, and an account the deployment grants no role to are all
// not-admitted, and a verification transport failure is returned as an error
// so the caller denies rather than falls through to some weaker path.
//
// The roles come from the deployment's grant keyed by the username the cluster
// authenticated, never from a claim inside the presented token, so holding a
// projected token for some other service account grants nothing.
//
// spec: §25.4 ("Calling the Gateway"), §10.2 line 227 (the gateway validates
// the projected token's signature on every pod→gateway request).
func (r *Resolver) ResolveService(ctx context.Context, token string) (authmw.ServiceIdentity, bool, error) {
	if r.verifier == nil || r.audience == "" || len(r.roles) == 0 {
		return authmw.ServiceIdentity{}, false, nil
	}
	// Every bearer the JWT verifier rejects reaches this point, so a token
	// that does not even claim this deployment's audience is dropped before
	// the apiserver round-trip. The check reads the unverified payload and is
	// a cost guard only; the TokenReview below remains the authority on
	// whether the token is authentic for the audience.
	if !claimsAudience(token, r.audience) {
		return authmw.ServiceIdentity{}, false, nil
	}
	username, err := r.verifier.VerifyUser(ctx, token, r.audience)
	if err != nil {
		return authmw.ServiceIdentity{}, false, fmt.Errorf("verify service-account token for audience %q: %w", r.audience, err)
	}
	roles, granted := r.roles[username]
	if !granted {
		return authmw.ServiceIdentity{}, false, nil
	}
	return authmw.ServiceIdentity{
		Subject:  username,
		TenantID: r.tenantID,
		Roles:    append([]auth.Role(nil), roles...),
	}, true, nil
}

var _ authmw.ServiceIdentityResolver = (*Resolver)(nil)

// claimsAudience reports whether token's unverified `aud` claim names
// audience. It exists so an arbitrary rejected bearer does not cost a
// Kubernetes TokenReview: nothing is authorized on its result, and the
// TokenReview decides whether the token is genuinely valid for the audience.
// The claim may be a JSON string or an array of strings per RFC 7519.
func claimsAudience(token, audience string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Aud json.RawMessage `json:"aud"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || len(claims.Aud) == 0 {
		return false
	}
	var single string
	if err := json.Unmarshal(claims.Aud, &single); err == nil {
		return single == audience
	}
	var many []string
	if err := json.Unmarshal(claims.Aud, &many); err != nil {
		return false
	}
	for _, a := range many {
		if a == audience {
			return true
		}
	}
	return false
}
