// SPDX-License-Identifier: MIT

// Package vcscred materializes a §14 gitClone source's VCS credential at
// session creation and at workspace staging. It binds the gitClone URL
// host to one of the tenant's §4.9 VCS credential pools
// (credentialpoolstore.ResolveVCSPool), selects an active credential,
// and reads the live token from the credential's Kubernetes Secret. The
// gateway uses the returned token to run `git ls-remote` (ref pinning)
// and the clone on its own network path, so the runtime pod never sees
// the raw credential, per §14 line 95 and §4.9.
//
// v1 ships `github` as the built-in VCS provider. The token is a GitHub
// App installation token or a personal access token the operator stored
// in the referenced Secret; the gateway sends it as an HTTP Basic
// Authorization header with the conventional `x-access-token` username.
package vcscred

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// GitHubTokenUsername is the HTTP Basic username the gateway pairs with a
// GitHub token. GitHub accepts any non-empty username for a personal
// access token and requires `x-access-token` for a GitHub App
// installation token, so the gateway uses the App-compatible value for
// both.
const GitHubTokenUsername = "x-access-token"

// Credential is a materialized §14 VCS credential: the token the gateway
// injects into a git invocation as an HTTP Basic Authorization header,
// paired with the username git sends alongside it. A zero Credential
// (empty Token) is an unauthenticated (public) clone.
type Credential struct {
	// Username is the HTTP Basic username sent with Token.
	Username string
	// Token is the short-lived VCS token. Empty for a public clone.
	Token string
}

// IsZero reports whether the credential carries no token — a public
// clone.
func (c Credential) IsZero() bool { return c.Token == "" }

// Resolver materializes a §14 gitClone source's VCS credential. A source
// whose leaseScope is empty (no auth block) resolves to a zero
// Credential, so a caller can resolve every gitClone source uniformly
// and inject the token only when one is present.
type Resolver interface {
	Resolve(ctx context.Context, tenantID, gitURL, leaseScope string) (Credential, error)
}

// PoolLister is the subset of credentialpoolstore.Store the resolver
// reads: the tenant's credential pools, from which the VCS pool is
// selected by host pattern.
type PoolLister interface {
	List(ctx context.Context, tenantID string, filter credentialpoolstore.ListFilter) ([]credentialpoolstore.CredentialPool, error)
}

// SecretReader resolves a credential's Kubernetes Secret reference to its
// live token value. connectorsecret.KubeResolver satisfies it
// structurally (its Resolve reads a `namespace/name[/key]` Secret), so
// the gateway reuses the same Secret-reading seam it wires for connector
// client secrets.
type SecretReader interface {
	Resolve(ctx context.Context, ref string) (string, error)
}

// ErrNoUsableCredential reports that the bound VCS pool carries no
// active (non-revoked) credential with a Secret reference. The §15.1
// surface treats it as a non-retryable configuration failure.
var ErrNoUsableCredential = errors.New("vcscred: VCS credential pool has no usable credential")

// StoreResolver is the production Resolver: it lists the tenant's pools,
// binds the gitClone host to one VCS pool, and reads the token from the
// selected credential's Secret.
type StoreResolver struct {
	// Pools lists the tenant's §4.9 credential pools.
	Pools PoolLister
	// Secrets reads a credential's token from its Kubernetes Secret.
	Secrets SecretReader
}

// Resolve binds gitURL's host to the tenant's VCS pool matching the
// leaseScope provider and returns the token from one of the pool's
// active credentials. An empty leaseScope resolves to a zero Credential
// (public clone). A binding failure is a *credentialpoolstore.VCSResolveError
// (host unsupported or ambiguous); an exhausted pool is
// ErrNoUsableCredential; a Secret read failure is wrapped verbatim.
func (r *StoreResolver) Resolve(ctx context.Context, tenantID, gitURL, leaseScope string) (Credential, error) {
	if strings.TrimSpace(leaseScope) == "" {
		return Credential{}, nil
	}
	provider, _, ok := workspaceplan.ParseLeaseScope(leaseScope)
	if !ok {
		return Credential{}, fmt.Errorf("vcscred: malformed leaseScope %q", leaseScope)
	}
	host, ok := hostOf(gitURL)
	if !ok {
		return Credential{}, fmt.Errorf("vcscred: gitClone URL %q has no host", gitURL)
	}
	if r == nil || r.Pools == nil || r.Secrets == nil {
		return Credential{}, errors.New("vcscred: resolver is not wired with a pool store and secret reader")
	}
	pools, err := r.Pools.List(ctx, tenantID, credentialpoolstore.ListFilter{})
	if err != nil {
		return Credential{}, fmt.Errorf("vcscred: list credential pools: %w", err)
	}
	pool, err := credentialpoolstore.ResolveVCSPool(pools, provider, host)
	if err != nil {
		return Credential{}, err
	}
	cred, ok := pickActiveCredential(pool)
	if !ok {
		return Credential{}, fmt.Errorf("%w: pool %q", ErrNoUsableCredential, pool.Name)
	}
	token, err := r.Secrets.Resolve(ctx, cred.SecretRef)
	if err != nil {
		return Credential{}, fmt.Errorf("vcscred: read token for pool %q credential %q: %w", pool.Name, cred.ID, err)
	}
	if strings.TrimSpace(token) == "" {
		return Credential{}, fmt.Errorf("%w: pool %q credential %q secret is empty", ErrNoUsableCredential, pool.Name, cred.ID)
	}
	return Credential{Username: GitHubTokenUsername, Token: token}, nil
}

// pickActiveCredential returns the first non-revoked credential in the
// pool that carries a Secret reference. VCS clones do not consume a
// concurrency slot, so the §4.9 assignment strategy (least-loaded,
// round-robin) does not apply; first-active selection is deterministic
// and sufficient.
func pickActiveCredential(pool credentialpoolstore.CredentialPool) (credentialpoolstore.Credential, bool) {
	for _, c := range pool.Credentials {
		if c.IsRevoked() || c.SecretRef == "" {
			continue
		}
		return c, true
	}
	return credentialpoolstore.Credential{}, false
}

// hostOf returns the lowercased host of a gitClone URL — the authority
// component credentialpoolstore matches against a pool's host patterns.
func hostOf(gitURL string) (string, bool) {
	u, err := url.Parse(gitURL)
	if err != nil || u.Host == "" {
		return "", false
	}
	return strings.ToLower(u.Host), true
}
