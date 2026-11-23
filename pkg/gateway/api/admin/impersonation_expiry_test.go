// SPDX-License-Identifier: MIT

package admin_test

import (
	"net/http"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
)

// TestImpersonationStartBoundsTheBearerByTheCallerBearer covers the handler
// half of the impersonation bearer's expiry bound and impersonator link: a
// platform-admin whose bearer expires before the requested duration ends
// starts a session whose recorded bearer expires with the caller's bearer,
// and the row names the caller's tenant and jti as its impersonator.
//
// spec: §13.3 (Credential Flow)
func TestImpersonationStartBoundsTheBearerByTheCallerBearer(t *testing.T) {
	f := newImpersonationFixture(t, &stubImpAppender{})
	callerExp := f.clock.Add(5 * time.Minute).Truncate(time.Second)
	as := func(req *http.Request) *http.Request {
		return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
			Subject:  "admin@acme.com",
			TenantID: "platform",
			Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
			JTI:      "admin-bearer",
			Expiry:   callerExp,
		}))
	}
	rr := startImpersonation(t, f.router.Handler(), admin.StartImpersonationRequest{
		TargetTenantID: "acme", TargetUserID: "alice@acme.com", Reason: "support", TicketRef: "SUP-9", DurationSeconds: 1800,
	}, as)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	recs := f.tokens.Records()
	if len(recs) != 1 {
		t.Fatalf("recorded rows = %d, want 1", len(recs))
	}
	row := recs[0].Token
	if !row.ExpiresAt.Equal(callerExp) {
		t.Errorf("bearer exp = %v, want the caller bearer's %v", row.ExpiresAt, callerExp)
	}
	if row.ImpersonatorTenantID != "platform" || row.ImpersonatorJTI != "admin-bearer" {
		t.Errorf("impersonator link = (%q, %q), want (platform, admin-bearer)", row.ImpersonatorTenantID, row.ImpersonatorJTI)
	}
}
