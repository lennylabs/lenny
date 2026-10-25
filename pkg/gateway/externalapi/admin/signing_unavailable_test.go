// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken"
	"github.com/lennylabs/lenny/pkg/kms"
)

// failingImpersonation is an admin.ImpersonationService whose Issue
// fails with issueErr.
type failingImpersonation struct{ issueErr error }

func (f failingImpersonation) Issue(context.Context, impersonation.IssueRequest) (impersonation.Ticket, string, error) {
	return impersonation.Ticket{}, "", f.issueErr
}

func (failingImpersonation) End(context.Context, string, string) (impersonation.Ticket, error) {
	return impersonation.Ticket{}, impersonation.ErrNotFound
}

func (failingImpersonation) ListActive(context.Context) ([]impersonation.Ticket, error) {
	return nil, nil
}

// failingRotator is an admin.AdminTokenProvisioner whose Rotate fails
// with rotateErr.
type failingRotator struct {
	fakeProvisioner
	rotateErr error
}

func (f *failingRotator) Rotate(context.Context) (admintoken.Result, error) {
	return admintoken.Result{}, f.rotateErr
}

// signingErrorCases are the Sign errors a gateway mint can see, wrapped
// the way the impersonation issuer and the admin-token provisioner wrap
// them, with the Retry-After each must produce.
var signingErrorCases = []struct {
	name           string
	err            error
	wantRetryAfter string
}{
	{"throttled", fmt.Errorf("mint: %w", &jwt.SigningError{RetryAfter: time.Second, Err: kms.ErrThrottled}), "1"},
	{"hard failure", fmt.Errorf("mint: %w", &jwt.SigningError{RetryAfter: time.Second, Err: errors.New("kms down")}), "1"},
	{"breaker open", fmt.Errorf("mint: %w", &jwt.SigningError{RetryAfter: 17500 * time.Millisecond, Err: jwt.ErrSigningUnavailable}), "18"},
}

func assertSigningUnavailable(t *testing.T, rr *httptest.ResponseRecorder, wantRetryAfter string) {
	t.Helper()
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Retry-After"); got != wantRetryAfter {
		t.Errorf("Retry-After = %q, want %q", got, wantRetryAfter)
	}
	var env struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if env.Error.Code != "KMS_SIGNING_UNAVAILABLE" || !env.Error.Retryable {
		t.Errorf("error = %+v, want KMS_SIGNING_UNAVAILABLE retryable", env.Error)
	}
}

// spec: 10.2 (Authentication) "KMS signing failure mode", 15.1 (REST API)
// diagnosis: an impersonation mint whose Sign failed did not return 503
// KMS_SIGNING_UNAVAILABLE with the breaker's Retry-After. Before every
// gateway mint path mapped a signing error, the handler answered it with
// 500 INTERNAL_ERROR.
func TestImpersonationSigningErrorReturns503RetryAfter_spec_10_2(t *testing.T) {
	for _, tc := range signingErrorCases {
		t.Run(tc.name, func(t *testing.T) {
			tenants := tenantstore.NewMemory()
			if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"}); err != nil {
				t.Fatalf("create tenant: %v", err)
			}
			router := admin.NewRouter(tenants, admin.Options{}).
				WithImpersonation(failingImpersonation{issueErr: tc.err})
			rr := startImpersonation(t, router.Handler(), admin.StartImpersonationRequest{
				TargetTenantID: "acme", TargetUserID: "alice@acme.com", Reason: "support", TicketRef: "SUP-1", DurationSeconds: 600,
			}, withAdminPrincipal)
			assertSigningUnavailable(t, rr, tc.wantRetryAfter)
		})
	}
}

// spec: 10.2 (Authentication) "KMS signing failure mode", 17.6 (Packaging and Installation)
// diagnosis: an admin-token rotation whose Sign failed did not return 503
// KMS_SIGNING_UNAVAILABLE with the breaker's Retry-After. Before every
// gateway mint path mapped a signing error, the route answered it with
// 500 INTERNAL_ERROR.
func TestRotateTokenSigningErrorReturns503RetryAfter_spec_10_2(t *testing.T) {
	for _, tc := range signingErrorCases {
		t.Run(tc.name, func(t *testing.T) {
			router := newAdminTokenRouter(t, &failingRotator{rotateErr: tc.err})
			req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/users/lenny-admin/rotate-token", nil))
			rr := httptest.NewRecorder()
			router.Handler().ServeHTTP(rr, req)
			assertSigningUnavailable(t, rr, tc.wantRetryAfter)
		})
	}
}

// spec: 17.6 (Packaging and Installation)
// diagnosis: a rotation failure unrelated to signing stopped mapping to
// 500 INTERNAL_ERROR, so the signing-error branch is catching errors that
// carry no *jwt.SigningError.
func TestRotateTokenNonSigningErrorStays500_spec_17_6(t *testing.T) {
	router := newAdminTokenRouter(t, &failingRotator{rotateErr: errors.New("patch secret: forbidden")})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/users/lenny-admin/rotate-token", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body %s", rr.Code, rr.Body.String())
	}
}
