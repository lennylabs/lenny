// SPDX-License-Identifier: MIT

package workspaceplan

import (
	"context"
	"errors"
	"fmt"
)

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

// RefResolver resolves a gitClone source's ref to an immutable commit
// SHA — the §14 git ls-remote step. A production implementation runs
// git ls-remote against the remote with the source's credential lease;
// tests supply a fake. A resolver should return a *ResolveError to
// classify the failure mode; an unclassified error is treated as a
// transient network failure.
type RefResolver interface {
	Resolve(ctx context.Context, src GitClone) (sha string, err error)
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
// other ref is resolved through resolver. A source whose
// ResolvedCommitSha is already set is left untouched, so PinCommitSHAs
// is idempotent across re-materialization. The plan is mutated in
// place. A non-nil return is a *ResolveError naming the first source
// that failed; resolution stops at that source.
func PinCommitSHAs(ctx context.Context, plan *Plan, resolver RefResolver) error {
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
		sha, err := resolveRef(ctx, gc, i, resolver)
		if err != nil {
			return err
		}
		gc.ResolvedCommitSha = sha
		plan.Sources[i].Variant = gc
	}
	return nil
}

// resolveRef resolves one gitClone source's ref to a commit SHA.
func resolveRef(ctx context.Context, gc GitClone, idx int, resolver RefResolver) (string, error) {
	// §14 SHA fast-path: a ref already in commit-SHA form is its own
	// resolution and needs no ls-remote round-trip.
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
	sha, err := resolver.Resolve(ctx, gc)
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
