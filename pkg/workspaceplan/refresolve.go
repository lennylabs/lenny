// SPDX-License-Identifier: MIT

package workspaceplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// LeaseScopeMode is the access half of a §14 gitClone.auth.leaseScope:
// a read-only clone or a read-write clone.
type LeaseScopeMode string

const (
	LeaseScopeRead  LeaseScopeMode = "read"
	LeaseScopeWrite LeaseScopeMode = "write"
)

// ParseLeaseScope extracts the VCS provider and access mode from a §14
// gitClone.auth.leaseScope of the form `vcs.<provider>.read` or
// `vcs.<provider>.write`. ok is false when scope does not match that
// form. The provider feeds the §14 host-to-pool binding
// (credentialpoolstore.ResolveVCSPool).
func ParseLeaseScope(scope string) (provider string, mode LeaseScopeMode, ok bool) {
	if !leaseScopeRE.MatchString(scope) {
		return "", "", false
	}
	// scope is "vcs." + provider + "." + mode; the regex restricts the
	// provider to [a-z0-9_-]+, so it carries no dots and the final dot
	// separates the provider from the mode.
	inner := strings.TrimPrefix(scope, "vcs.")
	dot := strings.LastIndex(inner, ".")
	return inner[:dot], LeaseScopeMode(inner[dot+1:]), true
}

// GitCloneHost returns the lowercased host of a gitClone source's URL —
// the §14 authority component used for VCS-pool host matching. ok is
// false when the URL does not parse or carries no host.
func GitCloneHost(gc GitClone) (host string, ok bool) {
	u, err := url.Parse(gc.URL)
	if err != nil || u.Host == "" {
		return "", false
	}
	return strings.ToLower(u.Host), true
}

// Marshal serializes a parsed Plan back to §14 WorkspacePlan JSON. It
// is the inverse of Parse: each source is emitted from the Raw object
// Parse preserved, so unknown source types and any fields the typed
// model does not carry round-trip unchanged. A gitClone source whose
// ResolvedCommitSha was set by PinCommitSHAs is emitted with the
// resolvedCommitSha field — the canonical form §14 persists at session
// creation. Marshal operates on a Plan produced by Parse.
func Marshal(plan Plan) ([]byte, error) {
	sources := make([]map[string]any, 0, len(plan.Sources))
	for _, src := range plan.Sources {
		obj := make(map[string]any, len(src.Raw)+1)
		for k, v := range src.Raw {
			obj[k] = v
		}
		if _, ok := obj["type"]; !ok && src.Type != "" {
			obj["type"] = src.Type
		}
		if gc, ok := src.Variant.(GitClone); ok && gc.ResolvedCommitSha != "" {
			obj["resolvedCommitSha"] = gc.ResolvedCommitSha
		}
		sources = append(sources, obj)
	}
	doc := map[string]any{
		"schemaVersion": plan.SchemaVersion,
		"sources":       sources,
	}
	if len(plan.SetupCommands) > 0 {
		doc["setupCommands"] = plan.SetupCommands
	}
	return json.Marshal(doc)
}

// IsCommitSHA reports whether ref is a full 40-character lowercase
// hexadecimal Git commit SHA — the §14 form that the gateway pins
// directly without a git ls-remote round-trip.
func IsCommitSHA(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	for _, c := range ref {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// VCSCredential is the §14 credential the gateway injects into the
// ls-remote and clone of a private gitClone source. A zero value (empty
// Token) resolves a public repository unauthenticated. It is a
// dependency-free value type so workspaceplan does not import the
// gateway credential packages; the gateway maps its own credential into
// this shape at the resolver boundary.
type VCSCredential struct {
	// Username is the HTTP Basic username paired with Token.
	Username string
	// Token is the short-lived VCS token. Empty for a public clone.
	Token string
}

// IsZero reports whether the credential carries no token.
func (c VCSCredential) IsZero() bool { return c.Token == "" }

// VCSCredentialFunc materializes the §14 VCS credential for one gitClone
// source at ref-resolution time. The gateway supplies it so the
// ls-remote that pins a private repo's ref uses the same credential the
// clone will. It returns a zero VCSCredential for a public source. A
// non-nil error aborts pinning for that source; PinCommitSHAs wraps it
// as a ResolveError with the auth_failed reason.
type VCSCredentialFunc func(ctx context.Context, src GitClone) (VCSCredential, error)

// RefResolver resolves a gitClone source's ref to an immutable commit
// SHA — the §14 git ls-remote step. A production implementation runs
// git ls-remote against the remote with cred (the source's VCS
// credential, zero for a public repo); tests supply a fake. A resolver
// should return a *ResolveError to classify the failure mode; an
// unclassified error is treated as a transient network failure.
type RefResolver interface {
	Resolve(ctx context.Context, src GitClone, cred VCSCredential) (sha string, err error)
}

// ResolveErrorReason classifies a §14 ref-resolution failure. It maps
// to the §15.1 error code: a transient reason is the retryable
// GIT_CLONE_REF_RESOLVE_TRANSIENT (503), the others are the
// non-retryable GIT_CLONE_REF_UNRESOLVABLE (422).
type ResolveErrorReason string

const (
	// ResolveNetworkError is a transient network-level failure (DNS,
	// connection reset, TLS handshake, remote-host timeout). Retryable.
	ResolveNetworkError ResolveErrorReason = "network_error"
	// ResolveAuthFailed: the credential lease was rejected by the
	// remote. Not retryable without source-definition changes.
	ResolveAuthFailed ResolveErrorReason = "auth_failed"
	// ResolveRefNotFound: the ref does not exist on the remote. Not
	// retryable without source-definition changes.
	ResolveRefNotFound ResolveErrorReason = "ref_not_found"
)

// Transient reports whether the reason is the retryable
// GIT_CLONE_REF_RESOLVE_TRANSIENT class. The other reasons are the
// non-retryable GIT_CLONE_REF_UNRESOLVABLE class.
func (r ResolveErrorReason) Transient() bool { return r == ResolveNetworkError }

// ResolveError is a ref-resolution failure for one gitClone source.
// The session handler maps it to the §15.1 GIT_CLONE_REF_RESOLVE_TRANSIENT
// (503) or GIT_CLONE_REF_UNRESOLVABLE (422) response per Reason; its
// fields supply the response details (url, ref, sourceIndex, reason).
type ResolveError struct {
	SourceIndex int
	URL         string
	Ref         string
	Reason      ResolveErrorReason
	Err         error
}

func (e *ResolveError) Error() string {
	return fmt.Sprintf("workspaceplan: resolve sources[%d] ref %q (%s): %v",
		e.SourceIndex, e.Ref, e.Reason, e.Err)
}

func (e *ResolveError) Unwrap() error { return e.Err }

// PinCommitSHAs resolves every gitClone source's ref to an immutable
// commit SHA and writes it to the source's ResolvedCommitSha — the §14
// per-session immutability guarantee. A ref already in 40-character
// lowercase hex form is pinned directly without a resolver call. Any
// other ref is resolved through resolver, using the VCS credential creds
// materializes for the source (nil creds, or a zero credential, resolves
// a public repo unauthenticated — §14 line 102 "the same credential-lease
// as the clone itself, or unauthenticated for public repos"). A source
// whose ResolvedCommitSha is already set is left untouched, so
// PinCommitSHAs is idempotent across re-materialization. The plan is
// mutated in place. A non-nil return is a *ResolveError naming the first
// source that failed; resolution stops at that source.
func PinCommitSHAs(ctx context.Context, plan *Plan, resolver RefResolver, creds VCSCredentialFunc) error {
	if plan == nil {
		return nil
	}
	for i := range plan.Sources {
		gc, ok := plan.Sources[i].Variant.(GitClone)
		if !ok {
			continue
		}
		if gc.ResolvedCommitSha != "" {
			continue
		}
		sha, err := resolveRef(ctx, gc, i, resolver, creds)
		if err != nil {
			return err
		}
		gc.ResolvedCommitSha = sha
		plan.Sources[i].Variant = gc
	}
	return nil
}

// resolveRef resolves one gitClone source's ref to a commit SHA.
func resolveRef(ctx context.Context, gc GitClone, idx int, resolver RefResolver, creds VCSCredentialFunc) (string, error) {
	// §14 SHA fast-path: a ref already in commit-SHA form is its own
	// resolution and needs no ls-remote round-trip, so no credential is
	// materialized either.
	if IsCommitSHA(gc.Ref) {
		return gc.Ref, nil
	}
	if resolver == nil {
		return "", &ResolveError{
			SourceIndex: idx, URL: gc.URL, Ref: gc.Ref,
			Reason: ResolveNetworkError,
			Err:    errors.New("no ref resolver configured"),
		}
	}
	var cred VCSCredential
	if creds != nil {
		c, err := creds(ctx, gc)
		if err != nil {
			// A credential-materialization failure (no usable pool
			// credential, an unreadable Secret) is a non-retryable
			// configuration failure, surfaced here so it fails session
			// creation rather than the later clone — the §14 boundary the
			// gateway must catch at create time.
			return "", &ResolveError{
				SourceIndex: idx, URL: gc.URL, Ref: gc.Ref,
				Reason: ResolveAuthFailed, Err: err,
			}
		}
		cred = c
	}
	sha, err := resolver.Resolve(ctx, gc, cred)
	if err != nil {
		var re *ResolveError
		if errors.As(err, &re) {
			re.SourceIndex = idx
			if re.URL == "" {
				re.URL = gc.URL
			}
			if re.Ref == "" {
				re.Ref = gc.Ref
			}
			return "", re
		}
		// A resolver that did not classify the failure is treated as a
		// transient network error — the retryable default.
		return "", &ResolveError{
			SourceIndex: idx, URL: gc.URL, Ref: gc.Ref,
			Reason: ResolveNetworkError, Err: err,
		}
	}
	if !IsCommitSHA(sha) {
		return "", &ResolveError{
			SourceIndex: idx, URL: gc.URL, Ref: gc.Ref,
			Reason: ResolveRefNotFound,
			Err:    fmt.Errorf("resolver returned %q, which is not a commit SHA", sha),
		}
	}
	return sha, nil
}
