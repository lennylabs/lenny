// SPDX-License-Identifier: MIT

package credentialserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

func asUser(req *http.Request, tenant, user string) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: user, TenantID: tenant,
		Roles: []pkgauth.Role{pkgauth.RoleUser},
	})
	return req.WithContext(ctx)
}

func newCredServer(t *testing.T) (*credentialserver.Server, *credentialstore.Memory) {
	t.Helper()
	store := credentialstore.NewMemory(nil)
	return credentialserver.New(store), store
}

func TestRegisterAndList(t *testing.T) {
	srv, _ := newCredServer(t)
	body, _ := json.Marshal(credentialserver.RegisterRequest{
		Provider: string(credential.ProviderAnthropicDirect),
		Secret:   "sk-secret-value",
	})
	req := asUser(httptest.NewRequest(http.MethodPost, "/v1/credentials", bytes.NewReader(body)), "acme", "alice")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register: %d, body=%s", rr.Code, rr.Body.String())
	}
	// The response MUST NOT carry the secret.
	if bytes.Contains(rr.Body.Bytes(), []byte("sk-secret-value")) {
		t.Fatal("SECURITY: register response leaked the secret material")
	}
	var resp credentialserver.CredentialPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Ref == "" || resp.Provider != "anthropic_direct" {
		t.Errorf("payload: %+v", resp)
	}

	// List.
	lreq := asUser(httptest.NewRequest(http.MethodGet, "/v1/credentials", nil), "acme", "alice")
	lrr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(lrr, lreq)
	if lrr.Code != http.StatusOK {
		t.Fatalf("list: %d", lrr.Code)
	}
	if bytes.Contains(lrr.Body.Bytes(), []byte("sk-secret-value")) {
		t.Fatal("SECURITY: list response leaked the secret material")
	}
	var list struct {
		Credentials []credentialserver.CredentialPayload `json:"credentials"`
	}
	_ = json.Unmarshal(lrr.Body.Bytes(), &list)
	if len(list.Credentials) != 1 {
		t.Errorf("list count: %d", len(list.Credentials))
	}
}

func TestRegisterRejectsAnonymous(t *testing.T) {
	srv, _ := newCredServer(t)
	body, _ := json.Marshal(credentialserver.RegisterRequest{Provider: "github", Secret: "x"})
	req := httptest.NewRequest(http.MethodPost, "/v1/credentials", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("anonymous register: %d, want 401", rr.Code)
	}
}

func TestRegisterRejectsBadProvider(t *testing.T) {
	srv, _ := newCredServer(t)
	body, _ := json.Marshal(credentialserver.RegisterRequest{Provider: "bogus", Secret: "x"})
	req := asUser(httptest.NewRequest(http.MethodPost, "/v1/credentials", bytes.NewReader(body)), "acme", "alice")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad provider: %d", rr.Code)
	}
}

func TestRotate(t *testing.T) {
	srv, store := newCredServer(t)
	c, _ := store.Register(nil, "acme", "alice", credential.ProviderGitHub, "", "old")
	body, _ := json.Marshal(credentialserver.RotateRequest{Secret: "new-secret"})
	req := asUser(httptest.NewRequest(http.MethodPut, "/v1/credentials/"+c.Ref, bytes.NewReader(body)), "acme", "alice")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("rotate: %d, body=%s", rr.Code, rr.Body.String())
	}
	got, _ := store.Get(nil, "acme", c.Ref)
	if got.Secret != "new-secret" {
		t.Errorf("rotate did not replace secret")
	}
}

func TestRevoke(t *testing.T) {
	srv, store := newCredServer(t)
	c, _ := store.Register(nil, "acme", "alice", credential.ProviderGitHub, "", "x")
	req := asUser(httptest.NewRequest(http.MethodPost, "/v1/credentials/"+c.Ref+"/revoke", nil), "acme", "alice")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke: %d", rr.Code)
	}
	got, _ := store.Get(nil, "acme", c.Ref)
	if got.Status != credentialstore.StatusRevoked {
		t.Errorf("not revoked: %+v", got)
	}
}

func TestDelete(t *testing.T) {
	srv, store := newCredServer(t)
	c, _ := store.Register(nil, "acme", "alice", credential.ProviderGitHub, "", "x")
	req := asUser(httptest.NewRequest(http.MethodDelete, "/v1/credentials/"+c.Ref, nil), "acme", "alice")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rr.Code)
	}
}

func TestUserCannotTouchAnotherUsersCredential(t *testing.T) {
	srv, store := newCredServer(t)
	// alice registers a credential.
	c, _ := store.Register(nil, "acme", "alice", credential.ProviderGitHub, "", "alice-secret")
	// bob (same tenant) tries to rotate / revoke / delete it.
	for _, op := range []struct {
		method, path string
		body         []byte
	}{
		{http.MethodPut, "/v1/credentials/" + c.Ref, []byte(`{"secret":"hijack"}`)},
		{http.MethodPost, "/v1/credentials/" + c.Ref + "/revoke", nil},
		{http.MethodDelete, "/v1/credentials/" + c.Ref, nil},
	} {
		req := asUser(httptest.NewRequest(op.method, op.path, bytes.NewReader(op.body)), "acme", "bob")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s %s by other user: got %d, want 404 (not leaked)", op.method, op.path, rr.Code)
		}
	}
	// alice's credential is untouched.
	got, _ := store.Get(nil, "acme", c.Ref)
	if got.Secret != "alice-secret" || got.Status != credentialstore.StatusActive {
		t.Errorf("alice's credential was modified by bob: %+v", got)
	}
}

func TestRotateMissingCredential(t *testing.T) {
	srv, _ := newCredServer(t)
	req := asUser(httptest.NewRequest(http.MethodPut, "/v1/credentials/cred_missing",
		bytes.NewReader([]byte(`{"secret":"x"}`))), "acme", "alice")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("rotate missing: %d", rr.Code)
	}
}
