// SPDX-License-Identifier: MIT

package translator

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/responsesstub"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// TestResolveTenantByPosture covers resolveTenant in both postures. The
// authenticated principal always wins. Outside dev mode a request with no
// principal resolves no tenant, even when it names one through
// X-Lenny-Tenant-ID; in dev mode the well-formed header names the tenant
// and `default` is the fallback.
//
// spec: §10.2 (Authentication), §17.4 (dev mode).
func TestResolveTenantByPosture(t *testing.T) {
	cases := []struct {
		name              string
		dev               bool
		principal, header string
		want              string
		wantOK            bool
	}{
		{name: "strict principal over header", principal: "globex", header: "acme", want: "globex", wantOK: true},
		{name: "strict header only", header: "acme"},
		{name: "strict nothing"},
		{name: "dev principal over header", dev: true, principal: "globex", header: "acme", want: "globex", wantOK: true},
		{name: "dev header", dev: true, header: "acme", want: "acme", wantOK: true},
		{name: "dev malformed header", dev: true, header: "acme/../globex", want: "default", wantOK: true},
		{name: "dev nothing", dev: true, want: "default", wantOK: true},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		if tc.header != "" {
			r.Header.Set("X-Lenny-Tenant-ID", tc.header)
		}
		if tc.principal != "" {
			r = r.WithContext(authmw.WithPrincipal(r.Context(), authmw.Principal{TenantID: tc.principal}))
		}
		got, ok := resolveTenant(r, tc.dev)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("%s: resolveTenant = (%q, %t), want (%q, %t)", tc.name, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestAdaptersRefuseUnauthenticatedOutsideDevMode asserts that, outside
// dev mode, both OpenAI-dialect adapters refuse a request with no
// principal with 401 before creating or reading a session, even when it
// names a tenant through X-Lenny-Tenant-ID.
//
// spec: §10.2 (Authentication), §15.1 (UNAUTHORIZED error code).
func TestAdaptersRefuseUnauthenticatedOutsideDevMode(t *testing.T) {
	store := memstore.New()
	exec := executor.NewEchoExecutor()
	chat := NewOpenAIChatHandler(store, exec, OpenAIChatOptions{}).Handler()
	responses := NewOpenResponsesHandler(store, exec, OpenResponsesOptions{
		HolderForwarder: responsesstub.LocalForwarder{},
		SessionAccess:   ownerAccess{},
		Terminal:        responsesstub.ReleasingTerminal{Exec: exec, Store: store},
	}).Handler()
	for _, c := range []struct {
		name string
		h    http.Handler
		req  *http.Request
	}{
		{"chat create", chat, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"echo","messages":[{"role":"user","content":"hi"}]}`))},
		{"responses create", responses, httptest.NewRequest(http.MethodPost, "/v1/responses",
			strings.NewReader(`{"model":"echo","input":"hi"}`))},
		{"responses get", responses, httptest.NewRequest(http.MethodGet, "/v1/responses/resp-1", nil)},
	} {
		c.req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rr := httptest.NewRecorder()
		c.h.ServeHTTP(rr, c.req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s with header only: status %d, want 401 (%s)", c.name, rr.Code, rr.Body.String())
		}
	}
	rows, err := store.List(t.Context(), "acme", sessionstore.ListFilter{})
	if err == nil && len(rows) != 0 {
		t.Fatalf("a refused request created %d session rows for acme", len(rows))
	}
}
