// SPDX-License-Identifier: MIT

package admin_test

import (
	"net/http"
	"strings"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/common/scopes"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
)

// TestImpersonationStartPassesTheCallerScopeToTheBearer covers the handler
// half of the bearer-scope rule: a platform-admin whose bearer carries a
// scope claim starts a session whose recorded bearer carries the same
// scope, and a platform-admin without a scope claim starts one whose
// bearer carries none.
//
// spec: §13.3 (Credential Flow)
func TestImpersonationStartPassesTheCallerScopeToTheBearer(t *testing.T) {
	for _, claim := range []string{"tools:impersonation:write tools:me:read", ""} {
		t.Run("claim "+claim, func(t *testing.T) {
			f := newImpersonationFixture(t, &stubImpAppender{})
			set, err := scopes.Parse(claim)
			if err != nil {
				t.Fatalf("parse scope claim %q: %v", claim, err)
			}
			as := func(req *http.Request) *http.Request {
				return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
					Subject:  "admin@acme.com",
					TenantID: "platform",
					Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
					Scopes:   set,
				}))
			}
			rr := startImpersonation(t, f.router.Handler(), admin.StartImpersonationRequest{
				TargetTenantID: "acme", TargetUserID: "alice@acme.com", Reason: "support", TicketRef: "SUP-9", DurationSeconds: 600,
			}, as)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
			}
			recs := f.tokens.Records()
			if len(recs) != 1 {
				t.Fatalf("recorded rows = %d, want 1", len(recs))
			}
			if got := strings.Join(recs[0].Token.Scope, " "); got != claim {
				t.Errorf("bearer scope = %q, want the caller's %q", got, claim)
			}
		})
	}
}
