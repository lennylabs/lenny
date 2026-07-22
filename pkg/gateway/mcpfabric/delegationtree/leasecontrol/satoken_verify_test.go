// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// fakeReviewer returns a canned TokenReview status for every Create, so the
// verifier's verdict mapping is exercised without a live apiserver.
type fakeReviewer struct {
	status authnv1.TokenReviewStatus
	err    error
	gotAud []string
}

func (f *fakeReviewer) Create(_ context.Context, tr *authnv1.TokenReview, _ metav1.CreateOptions) (*authnv1.TokenReview, error) {
	f.gotAud = tr.Spec.Audiences
	if f.err != nil {
		return nil, f.err
	}
	out := tr.DeepCopy()
	out.Status = f.status
	return out, nil
}

// spec: §10.2 line 227 — an authenticated token whose granted audiences
// include the deployment audience passes.
func TestTokenReviewVerifierAuthenticatedAudienceMatch_spec_10_2_227(t *testing.T) {
	fr := &fakeReviewer{status: authnv1.TokenReviewStatus{
		Authenticated: true,
		Audiences:     []string{testAud},
	}}
	v := TokenReviewVerifier{Reviews: fr}
	if err := v.Verify(context.Background(), "tok", testAud); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fr.gotAud) != 1 || fr.gotAud[0] != testAud {
		t.Fatalf("verifier must pass the deployment audience to TokenReview, got %v", fr.gotAud)
	}
}

// spec: §10.2 line 227 — a forged/expired token (apiserver reports
// Authenticated=false) is rejected with the signature/expiry sentinel.
func TestTokenReviewVerifierUnauthenticatedRejected_spec_10_2_227(t *testing.T) {
	fr := &fakeReviewer{status: authnv1.TokenReviewStatus{
		Authenticated: false,
		Error:         "token expired",
	}}
	v := TokenReviewVerifier{Reviews: fr}
	err := v.Verify(context.Background(), "tok", testAud)
	if !errors.Is(err, ErrSATokenUnauthenticated) {
		t.Fatalf("expected ErrSATokenUnauthenticated, got %v", err)
	}
}

// spec: §10.2 line 227 — a token signed by the cluster issuer but minted
// for another deployment authenticates yet is not granted this audience.
func TestTokenReviewVerifierAudienceMismatchRejected_spec_10_2_227(t *testing.T) {
	fr := &fakeReviewer{status: authnv1.TokenReviewStatus{
		Authenticated: true,
		Audiences:     []string{"lenny-gateway-globex"},
	}}
	v := TokenReviewVerifier{Reviews: fr}
	err := v.Verify(context.Background(), "tok", testAud)
	if !errors.Is(err, ErrSATokenAudienceMismatch) {
		t.Fatalf("expected ErrSATokenAudienceMismatch, got %v", err)
	}
}

// spec: §10.2 line 227 — a TokenReview that cannot reach a verdict
// (apiserver unreachable, RBAC denied) fails closed.
func TestTokenReviewVerifierTransportErrorFailsClosed_spec_10_2_227(t *testing.T) {
	fr := &fakeReviewer{err: errors.New("connection refused")}
	v := TokenReviewVerifier{Reviews: fr}
	err := v.Verify(context.Background(), "tok", testAud)
	if !errors.Is(err, ErrSATokenReviewFailed) {
		t.Fatalf("expected ErrSATokenReviewFailed, got %v", err)
	}
}

// TokenReviewVerifier composes with client-go's fake clientset (the same
// AuthenticationV1().TokenReviews() the production binary wires), so the
// seam is satisfied by the real client type.
// spec: §10.2 line 227.
func TestTokenReviewVerifierWithFakeClientset_spec_10_2_227(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		tr := action.(k8stesting.CreateAction).GetObject().(*authnv1.TokenReview)
		tr.Status = authnv1.TokenReviewStatus{Authenticated: true, Audiences: tr.Spec.Audiences}
		return true, tr, nil
	})
	v := TokenReviewVerifier{Reviews: cs.AuthenticationV1().TokenReviews()}
	if err := v.Verify(context.Background(), "tok", testAud); err != nil {
		t.Fatalf("fake clientset path: unexpected error: %v", err)
	}
}

// stubVerifier lets the interceptor tests drive a deterministic verdict
// without constructing a JWT or a clientset.
type stubVerifier struct {
	gotToken string
	gotAud   string
	err      error
}

func (s *stubVerifier) Verify(_ context.Context, token, audience string) error {
	s.gotToken = token
	s.gotAud = audience
	return s.err
}

// spec: §10.2 line 227 — when a verifier is wired the interceptor delegates
// to it and admits the call on a nil verdict.
func TestSATokenInterceptorVerifierAccepts_spec_10_2_227(t *testing.T) {
	h, called := passHandler()
	sv := &stubVerifier{}
	itc := RequireSATokenInterceptor(testAud, sv)
	ctx := ctxWithBearer("opaque-projected-token")
	if _, err := itc(ctx, nil, &grpc.UnaryServerInfo{}, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !*called {
		t.Fatal("handler must be invoked when the verifier accepts")
	}
	if sv.gotToken != "opaque-projected-token" || sv.gotAud != testAud {
		t.Fatalf("verifier received token=%q aud=%q", sv.gotToken, sv.gotAud)
	}
}

// spec: §10.2 line 227 — a verifier rejection (forged/expired signature)
// fails the call closed; the handler never runs.
func TestSATokenInterceptorVerifierRejects_spec_10_2_227(t *testing.T) {
	h, called := passHandler()
	sv := &stubVerifier{err: ErrSATokenUnauthenticated}
	itc := RequireSATokenInterceptor(testAud, sv)
	ctx := ctxWithBearer("forged")
	_, err := itc(ctx, nil, &grpc.UnaryServerInfo{}, h)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
	if *called {
		t.Fatal("handler must not run when the verifier rejects")
	}
}

// spec: §10.2 line 227 — a TokenReview transport failure fails closed even
// though the token may be syntactically valid.
func TestSATokenInterceptorVerifierTransportFailsClosed_spec_10_2_227(t *testing.T) {
	h, called := passHandler()
	sv := &stubVerifier{err: ErrSATokenReviewFailed}
	itc := RequireSATokenInterceptor(testAud, sv)
	ctx := ctxWithBearer("valid-looking")
	_, err := itc(ctx, nil, &grpc.UnaryServerInfo{}, h)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated on TokenReview failure, got %v", err)
	}
	if *called {
		t.Fatal("handler must not run when TokenReview cannot reach a verdict")
	}
}

// spec: §10.2 line 227 — even with a verifier wired, a request that carries
// no SA token is rejected before the verifier is consulted.
func TestSATokenInterceptorVerifierMissingToken_spec_10_2_227(t *testing.T) {
	h, called := passHandler()
	sv := &stubVerifier{}
	itc := RequireSATokenInterceptor(testAud, sv)
	_, err := itc(context.Background(), nil, &grpc.UnaryServerInfo{}, h)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for a missing token, got %v", err)
	}
	if *called || sv.gotToken != "" {
		t.Fatal("verifier must not be consulted when the token is absent")
	}
}

// VerifyUser reports the ServiceAccount username the apiserver authenticated
// the token as, which is what a caller authorizing a specific service account
// (rather than only proving the token is authentic for this deployment) gates
// on. spec: §10.2 line 227, §25.4 ("Calling the Gateway").
func TestTokenReviewVerifierReportsTheAuthenticatedUser_spec_10_2_227(t *testing.T) {
	const opsSA = "system:serviceaccount:lenny-system:lenny-ops-sa"
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		tr := action.(k8stesting.CreateAction).GetObject().(*authnv1.TokenReview)
		// The apiserver returns the intersection of the requested audiences
		// and the audiences the token was actually minted for, so a request
		// naming another deployment's audience is granted nothing.
		tr.Status = authnv1.TokenReviewStatus{
			Authenticated: true,
			User:          authnv1.UserInfo{Username: opsSA},
		}
		for _, requested := range tr.Spec.Audiences {
			if requested == testAud {
				tr.Status.Audiences = append(tr.Status.Audiences, requested)
			}
		}
		return true, tr, nil
	})
	v := TokenReviewVerifier{Reviews: cs.AuthenticationV1().TokenReviews()}

	user, err := v.VerifyUser(context.Background(), "tok", testAud)
	if err != nil {
		t.Fatalf("VerifyUser: %v", err)
	}
	if user != opsSA {
		t.Fatalf("VerifyUser = %q, want the authenticated ServiceAccount username %q", user, opsSA)
	}

	// An audience the apiserver did not grant is a refusal, and reports no
	// username: a token minted for another deployment must authorize nothing.
	if user, err := v.VerifyUser(context.Background(), "tok", "some-other-deployment"); !errors.Is(err, ErrSATokenAudienceMismatch) || user != "" {
		t.Fatalf("VerifyUser(other audience) = (%q, %v), want an audience mismatch with no username", user, err)
	}
}
