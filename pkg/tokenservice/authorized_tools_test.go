// SPDX-License-Identifier: MIT

package tokenservice

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// toExchangeToken must carry the §13.3 authorized_tools claim into the
// exchange so the validator can preserve or narrow it. Before F-13.3.11
// the claim was dropped at this boundary, leaving operability-scope tokens
// with nothing to enforce against.
//
// spec: §13.3 line 580.
func TestToExchangeTokenCarriesAuthorizedTools_spec_13_3_580(t *testing.T) {
	c := jwt.Claims{
		TenantID:        "acme",
		Subject:         "alice@acme.com",
		Scope:           "sessions:read",
		Typ:             "user_bearer",
		AuthorizedTools: []string{"tools:sessions:read", "tools:sessions:write"},
	}
	tok := toExchangeToken(c)
	if len(tok.AuthorizedTools) != 2 ||
		tok.AuthorizedTools[0] != "tools:sessions:read" ||
		tok.AuthorizedTools[1] != "tools:sessions:write" {
		t.Errorf("toExchangeToken dropped authorized_tools; got %v", tok.AuthorizedTools)
	}
}
