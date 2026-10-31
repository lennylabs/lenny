// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 session owner binding. A session's owner is the subject of the
// authenticated principal that created it, and no request field sets it. A
// create through the dev-header transport that names no X-Lenny-User-ID has
// no subject to own the session, so it is refused rather than creating a
// session owned by whichever user its body names.

package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// ownerBindingGateway serves the session API behind the auth middleware's
// dev-header transport over store.
func ownerBindingGateway(store sessionstore.Store) http.Handler {
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
	return authmw.Wrap(srv.Handler(), authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true})
}

// postOwnerBindingCreate posts a create to path whose body names bob as the
// owner, with user as X-Lenny-User-ID when non-empty.
func postOwnerBindingCreate(t *testing.T, h http.Handler, path, user string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{"runtimeRef":"claude-code","userId":"bob@acme.com"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	if user != "" {
		req.Header.Set("X-Lenny-User-ID", user)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// diagnosis: a create request chose its session's owner. Either a dev-header
// create with no X-Lenny-User-ID was admitted and wrote a session owned by
// the user its body named, or an authenticated alice created a session owned
// by bob. The owner must be the principal's subject (ownerFromPrincipal in
// pkg/gateway/sessionserver/request_identity.go), and a create with no
// subject must answer 401 UNAUTHORIZED and write no row. Without this, any
// caller places sessions in another user's quota and erasure scope.
// spec: 10.2 (Session ownership), 12.8 (Lifecycle fence and drain), 15.1 (UNAUTHORIZED)
func TestCreateCannotChooseItsOwner_spec_10_2(t *testing.T) {
	for _, path := range []string{"/v1/sessions", "/v1/sessions/start"} {
		t.Run(path+"/no subject", func(t *testing.T) {
			store := memstore.New()
			rr := postOwnerBindingCreate(t, ownerBindingGateway(store), path, "")
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body %s", rr.Code, rr.Body.String())
			}
			var env struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(rr.Body.Bytes(), &env)
			if env.Error.Code != "UNAUTHORIZED" {
				t.Errorf("code = %q, want UNAUTHORIZED", env.Error.Code)
			}
			rows, err := store.List(context.Background(), "acme", sessionstore.ListFilter{})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(rows) != 0 {
				t.Errorf("a create with no subject wrote %d rows, first owned by %q", len(rows), rows[0].UserID)
			}
		})
		t.Run(path+"/alice names bob", func(t *testing.T) {
			store := memstore.New()
			rr := postOwnerBindingCreate(t, ownerBindingGateway(store), path, "alice@acme.com")
			if rr.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
			}
			rows, err := store.List(context.Background(), "acme", sessionstore.ListFilter{})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			for _, row := range rows {
				if row.UserID != "alice@acme.com" {
					t.Errorf("session %s owner = %q, want the principal alice@acme.com", row.ID, row.UserID)
				}
			}
		})
	}
}
