// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"fmt"

	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TokenVerifier validates a projected ServiceAccount token's signature,
// expiry, and audience. The §10.2 line 227 contract — "Pods cannot forge
// or extend this token. The gateway validates the signature on every
// pod→gateway request" — is satisfied by the production implementation
// (TokenReviewVerifier), which delegates the cryptographic check to the
// kube-apiserver. A nil verifier degrades RequireSATokenInterceptor to the
// audience-only decode (the local-development path with no cluster client).
// spec: §10.2 line 227.
type TokenVerifier interface {
	// Verify returns nil when token is a kube-apiserver-issued projected
	// SA token that is currently valid for audience, and a non-nil error
	// (one of the sentinels below, or a wrapped transport error) otherwise.
	Verify(ctx context.Context, token, audience string) error
}

// TokenReviewer is the subset of the Kubernetes authentication client the
// SA-token verifier needs. It is satisfied by a clientset's
// AuthenticationV1().TokenReviews() and by client-go's fake clientset, so
// the verifier is unit-testable without a live API server.
type TokenReviewer interface {
	Create(ctx context.Context, tokenReview *authnv1.TokenReview, opts metav1.CreateOptions) (*authnv1.TokenReview, error)
}

// TokenReviewVerifier validates a projected SA token by submitting a
// Kubernetes TokenReview. The kube-apiserver verifies the token signature
// and expiry against the cluster's service-account issuer, so a pod cannot
// forge or extend the token (§10.2 line 227). Passing the deployment
// audience in the TokenReview spec also binds the check to this
// deployment's audience, so a token minted for another Lenny gateway is
// rejected even though both are signed by the same cluster issuer.
//
// Every call performs a TokenReview. Pod→gateway control-plane traffic
// (intra-pod platform-tool forwarding, connector-tool and scrub-report
// operations) is agent-paced rather than a hot loop, so the per-request
// apiserver round-trip is acceptable; a short-TTL positive cache is a
// future optimization if call volume grows.
// spec: §10.2 line 227.
type TokenReviewVerifier struct {
	Reviews TokenReviewer
}

// Verify implements TokenVerifier. It fails closed: any transport error
// reaching the apiserver, an unauthenticated verdict, or an audience that
// the apiserver did not grant all reject the call.
func (v TokenReviewVerifier) Verify(ctx context.Context, token, audience string) error {
	review := &authnv1.TokenReview{
		Spec: authnv1.TokenReviewSpec{
			Token:     token,
			Audiences: []string{audience},
		},
	}
	res, err := v.Reviews.Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSATokenReviewFailed, err)
	}
	if !res.Status.Authenticated {
		if msg := res.Status.Error; msg != "" {
			return fmt.Errorf("%w: %s", ErrSATokenUnauthenticated, msg)
		}
		return ErrSATokenUnauthenticated
	}
	// TokenReview returns the granted audiences: the intersection of the
	// requested set and the audiences the token was actually minted for. A
	// token signed by the cluster issuer but minted for a different
	// audience authenticates with Authenticated=true yet does not list this
	// deployment's audience, so the membership check below is load-bearing.
	for _, granted := range res.Status.Audiences {
		if granted == audience {
			return nil
		}
	}
	return ErrSATokenAudienceMismatch
}

const (
	// ErrSATokenReviewFailed is the fail-closed sentinel for a TokenReview
	// that could not reach a verdict (apiserver unreachable, RBAC denied,
	// timeout). The interceptor rejects the call rather than admitting it.
	ErrSATokenReviewFailed = satokenError("tokenreview request failed")
	// ErrSATokenUnauthenticated is returned when the apiserver reports the
	// token is not valid (bad signature, expired, revoked SA).
	ErrSATokenUnauthenticated = satokenError("projected SA token failed signature or expiry validation")
	// ErrSATokenAudienceMismatch is returned when the token is authentic
	// but was not minted for this deployment's audience.
	ErrSATokenAudienceMismatch = satokenError("projected SA token is not valid for this deployment audience")
)

var _ TokenVerifier = TokenReviewVerifier{}
