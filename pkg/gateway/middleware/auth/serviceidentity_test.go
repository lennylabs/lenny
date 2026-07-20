// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// stubServiceIdentity resolves one fixed token into one fixed identity.
type stubServiceIdentity struct {
	token string
	id    ServiceIdentity
	err   error
	calls int
}

func (s *stubServiceIdentity) ResolveService(_ context.Context, token string) (ServiceIdentity, bool, error) {
	s.calls++
	if s.err != nil {
		return ServiceIdentity{}, false, s.err
	}
	if token != s.token {
		return ServiceIdentity{}, false, nil
	}
	return s.id, true, nil
}

// rejectingVerifier stands in for the JWT verifier a projected ServiceAccount
// token always fails: it is signed by the cluster's service-account issuer
// rather than by the platform token service.
type rejectingVerifier struct{}

func (rejectingVerifier) Verify(string) (jwt.Claims, error) {
	return jwt.Claims{}, &jwt.VerifyError{Reason: "signature"}
}

// serviceRequest drives one Bearer request through the middleware.
func serviceRequest(h http.Handler, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/buffer", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// spec: §25.4 ("`lenny-ops` calls the gateway's admin API as a regular
// authenticated HTTPS client. It uses a dedicated service account
// (`lenny-ops-sa`) with `platform-admin` role... All calls go through the
// gateway's standard RBAC, validation, and audit").
//
// The credential lenny-ops presents is a projected Kubernetes ServiceAccount
// token, which the JWT verifier rejects. Without a resolved service principal
// the standard admin role gates refuse every call it makes, so the §25.5
// Redis-down gateway-buffer fall-back has no data source in a real cluster.
func TestServiceAccountBearerResolvesToTheGrantedPrincipal_spec_25_4(t *testing.T) {
	const saToken = "projected-sa-token"
	const opsSA = "system:serviceaccount:lenny-system:lenny-ops-sa"

	inner, got := captureHandler()
	resolver := &stubServiceIdentity{
		token: saToken,
		id: ServiceIdentity{
			Subject:  opsSA,
			TenantID: "platform",
			Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
		},
	}
	h := Wrap(inner, Options{
		Verifier:        rejectingVerifier{},
		MultiTenant:     true,
		ServiceIdentity: resolver,
	})

	rec := serviceRequest(h, saToken)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: the service-account credential was refused, so every "+
			"admin call lenny-ops makes 403s; body=%s", rec.Code, rec.Body.String())
	}
	if !got.HasRole(pkgauth.RolePlatformAdmin) {
		t.Errorf("principal = %+v, want the §25.4 platform-admin grant", *got)
	}
	if got.Subject != opsSA || got.TenantID != "platform" {
		t.Errorf("principal = %+v, want subject %q in tenant platform", *got, opsSA)
	}
	if got.CallerType != "service" {
		t.Errorf("callerType = %q, want \"service\" for audit-trail distinction", got.CallerType)
	}
}

// spec: §25.4 (no backdoor, no loopback shortcut), §10.2 (the admin role gate)
func TestServiceIdentityFailsClosed_spec_25_4(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the handler ran for a credential that must be refused")
		w.WriteHeader(http.StatusNoContent)
	})

	t.Run("an unrecognised token is refused", func(t *testing.T) {
		h := Wrap(inner, Options{
			Verifier:        rejectingVerifier{},
			MultiTenant:     true,
			ServiceIdentity: &stubServiceIdentity{token: "the-granted-token"},
		})
		if rec := serviceRequest(h, "some-other-token"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("a resolver failure is refused rather than admitted", func(t *testing.T) {
		h := Wrap(inner, Options{
			Verifier:        rejectingVerifier{},
			MultiTenant:     true,
			ServiceIdentity: &stubServiceIdentity{err: errors.New("tokenreview unavailable")},
		})
		if rec := serviceRequest(h, "projected-sa-token"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401: an unresolvable service identity must deny", rec.Code)
		}
	})

	t.Run("no resolver leaves the bearer path unchanged", func(t *testing.T) {
		h := Wrap(inner, Options{Verifier: rejectingVerifier{}, MultiTenant: true})
		if rec := serviceRequest(h, "projected-sa-token"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
}

// spec: §10.2 (the JWT bearer path is authoritative for a Lenny-minted token)
//
// The resolver is consulted only after JWT verification fails, so an ordinary
// bearer can never be re-resolved into a service principal with wider roles.
func TestServiceIdentityIsNotConsultedForAValidJWT_spec_10_2(t *testing.T) {
	secret := []byte("test-secret")
	verifier := jwt.NewHMACSigner("test", secret)
	token := signJWTWithExtras(t, secret, "", map[string]any{
		"sub":       "alice@acme.com",
		"tenant_id": "acme",
		"exp":       time.Now().Add(time.Hour).Unix(),
	})

	inner, got := captureHandler()
	resolver := &stubServiceIdentity{
		token: token,
		id:    ServiceIdentity{Subject: "escalated", Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin}},
	}
	h := Wrap(inner, Options{
		Verifier:        verifier,
		MultiTenant:     true,
		Registry:        permissiveRegistry{},
		ServiceIdentity: resolver,
	})

	if rec := serviceRequest(h, token); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.calls != 0 {
		t.Errorf("the service-identity resolver was consulted %d times for a valid JWT, want 0", resolver.calls)
	}
	if got.HasRole(pkgauth.RolePlatformAdmin) || got.Subject != "alice@acme.com" {
		t.Errorf("principal = %+v, want the JWT's own subject with no service grant", *got)
	}
}
