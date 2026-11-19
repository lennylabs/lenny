// SPDX-License-Identifier: MIT

// Package satoken verifies a projected ServiceAccount token through a
// Kubernetes TokenReview. The platform tools seam's lease-control server
// and the API front's service identity check both use it, so it is a
// domain package rather than a part of either.
package satoken

import (
	"context"
	"fmt"

	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
// forge or extend the token (§10.3). Passing the deployment
// audience in the TokenReview spec also binds the check to this
// deployment's audience, so a token minted for another Lenny gateway is
// rejected even though both are signed by the same cluster issuer.
//
// Every call performs a TokenReview. Pod→gateway control-plane traffic
// (intra-pod platform-tool forwarding, connector-tool and scrub-report
// operations) is agent-paced rather than a hot loop, so the per-request
// apiserver round-trip is acceptable; a short-TTL positive cache is a
// future optimization if call volume grows.
// spec: §10.3 (Projected SA token).
type TokenReviewVerifier struct {
	Reviews TokenReviewer
}

// Verify reports whether token is a valid projected SA token for
// audience. It fails closed: any transport error
// reaching the apiserver, an unauthenticated verdict, or an audience that
// the apiserver did not grant all reject the call.
func (v TokenReviewVerifier) Verify(ctx context.Context, token, audience string) error {
	_, err := v.VerifyUser(ctx, token, audience)
	return err
}

// VerifyUser performs the same fail-closed TokenReview as Verify and returns
// the ServiceAccount username the apiserver authenticated the token as
// (`system:serviceaccount:<namespace>:<name>`). A caller that authorizes a
// specific service account, rather than only proving the token is authentic
// for this deployment, needs that username; Verify is the outcome-only form.
// spec: §10.3 (Projected SA token).
func (v TokenReviewVerifier) VerifyUser(ctx context.Context, token, audience string) (string, error) {
	review := &authnv1.TokenReview{
		Spec: authnv1.TokenReviewSpec{
			Token:     token,
			Audiences: []string{audience},
		},
	}
	res, err := v.Reviews.Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSATokenReviewFailed, err)
	}
	if !res.Status.Authenticated {
		if msg := res.Status.Error; msg != "" {
			return "", fmt.Errorf("%w: %s", ErrSATokenUnauthenticated, msg)
		}
		return "", ErrSATokenUnauthenticated
	}
	// TokenReview returns the granted audiences: the intersection of the
	// requested set and the audiences the token was actually minted for. A
	// token signed by the cluster issuer but minted for a different
	// audience authenticates with Authenticated=true yet does not list this
	// deployment's audience, so the membership check below is load-bearing.
	for _, granted := range res.Status.Audiences {
		if granted == audience {
			return res.Status.User.Username, nil
		}
	}
	return "", ErrSATokenAudienceMismatch
}

const (
	// ErrSATokenReviewFailed is the fail-closed sentinel for a TokenReview
	// that could not reach a verdict (apiserver unreachable, RBAC denied,
	// timeout). The interceptor rejects the call rather than admitting it.
	ErrSATokenReviewFailed = Error("tokenreview request failed")
	// ErrSATokenUnauthenticated is returned when the apiserver reports the
	// token is not valid (bad signature, expired, revoked SA).
	ErrSATokenUnauthenticated = Error("projected SA token failed signature or expiry validation")
	// ErrSATokenAudienceMismatch is returned when the token is authentic
	// but was not minted for this deployment's audience.
	ErrSATokenAudienceMismatch = Error("projected SA token is not valid for this deployment audience")
)

// Error is the type of the verifier's sentinel errors.
type Error string

func (e Error) Error() string { return string(e) }
