// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/treearchive"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// listCatalog is a minimal artifactcatalog.Store exposing only
// ListBySession; any other call panics on the embedded nil interface.
type listCatalog struct {
	artifactcatalog.Store
	rows []artifactcatalog.Record
}

func (c listCatalog) ListBySession(_ context.Context, _, _ string) ([]artifactcatalog.Record, error) {
	return c.rows, nil
}

func withPrincipal(p authmw.Principal) func(*http.Request) {
	return func(req *http.Request) {
		*req = *req.WithContext(authmw.WithPrincipal(req.Context(), p))
	}
}

// TestServiceCall_PassthroughAndError_spec_15_2_3 verifies the §15.2.1
// rule-1 in-process dispatcher returns the REST 2xx body verbatim and
// projects a non-2xx onto a typed ServiceError carrying the §16.3 code.
// spec: §15.2.1 rule 1 line 1380. F-15.2.3.
func TestServiceCall_PassthroughAndError_spec_15_2_3(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	_ = store.Create(ctx, sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateRunning, CreatedAt: time.Unix(1, 0)})
	srv := sessionserver.New(store, sessionserver.Options{})

	res, svcErr := srv.ServiceCall(ctx, "acme", http.MethodGet, "/v1/sessions/s1", nil, "", nil)
	if svcErr != nil {
		t.Fatalf("ServiceCall(existing) error: %+v", svcErr)
	}
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Status)
	}
	var got map[string]any
	if err := json.Unmarshal(res.Body, &got); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, res.Body)
	}
	if got["id"] != "s1" {
		t.Fatalf("body id = %v, want s1; body=%s", got["id"], res.Body)
	}

	_, svcErr = srv.ServiceCall(ctx, "acme", http.MethodGet, "/v1/sessions/missing", nil, "", nil)
	if svcErr == nil {
		t.Fatal("ServiceCall(missing) expected a ServiceError")
	}
	if svcErr.HTTPStatus != http.StatusNotFound || svcErr.Code != "RESOURCE_NOT_FOUND" {
		t.Fatalf("missing-session error = %d/%s, want 404/RESOURCE_NOT_FOUND", svcErr.HTTPStatus, svcErr.Code)
	}
}

// TestListArtifacts_spec_15_2_3 verifies GET /v1/sessions/{id}/artifacts
// lists only live catalog rows, 404s a missing session, and returns an
// empty envelope when no catalog is wired. spec: §15.1 line 598; F-15.2.3.
func TestListArtifacts_spec_15_2_3(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	_ = store.Create(ctx, sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateCompleted, CreatedAt: time.Unix(1, 0)})
	cat := listCatalog{rows: []artifactcatalog.Record{
		{URI: "lenny-blob://acme/workspace/s1/p1", SessionID: "s1", State: artifactcatalog.StateLive, SizeBytes: 12, ArtifactType: artifactcatalog.ArtifactTypeWorkspace},
		{URI: "lenny-blob://acme/workspace/s1/gone", SessionID: "s1", State: artifactcatalog.StateSoftDeleted, ArtifactType: artifactcatalog.ArtifactTypeWorkspace},
	}}
	srv := sessionserver.New(store, sessionserver.Options{Artifacts: cat})
	h := srv.Handler()

	// Happy path: only the live row is surfaced.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/s1/artifacts", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("artifacts status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Items []struct {
			Ref       string `json:"ref"`
			Type      string `json:"type"`
			SizeBytes int64  `json:"sizeBytes"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	if len(env.Items) != 1 || env.Items[0].Ref != "lenny-blob://acme/workspace/s1/p1" || env.Items[0].Type != "workspace" || env.Items[0].SizeBytes != 12 {
		t.Fatalf("artifacts items = %+v, want one live workspace row", env.Items)
	}

	// Missing session → 404.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/nope/artifacts", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing-session artifacts status = %d, want 404", rr.Code)
	}

	// No catalog wired → empty list, not an error.
	srvNoCat := sessionserver.New(store, sessionserver.Options{})
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/s1/artifacts", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	srvNoCat.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("no-catalog artifacts status = %d, want 200", rr.Code)
	}
}

// TestSessionUsage_spec_15_2_3 verifies GET /v1/sessions/{id}/usage sums
// the session's reconciled billing usage, enforces view_usage for a
// roled principal, and 404s a missing session. spec: §15.1; §11.2.1;
// §10.2 view_usage. F-15.2.3.
func TestSessionUsage_spec_15_2_3(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	_ = store.Create(ctx, sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateCompleted, CreatedAt: time.Unix(1, 0)})
	billing := billingstore.NewMemory()
	_, _ = billing.Append(ctx, billingstore.Event{TenantID: "acme", SessionID: "s1", EventType: billingstore.EventSessionCreated, TokensInput: 30, TokensOutput: 7})
	_, _ = billing.Append(ctx, billingstore.Event{TenantID: "acme", SessionID: "s1", EventType: billingstore.EventSessionCompleted, TokensInput: 20, TokensOutput: 3})
	srv := sessionserver.New(store, sessionserver.Options{Billing: billing})
	h := srv.Handler()

	// view_usage principal: totals returned.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/s1/usage", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	withPrincipal(authmw.Principal{Subject: "bob@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleBillingViewer}})(req)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("usage status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var rep struct {
		SessionID    string `json:"sessionId"`
		TokensInput  uint64 `json:"tokensInput"`
		TokensOutput uint64 `json:"tokensOutput"`
		EventCount   int    `json:"eventCount"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode usage: %v; body=%s", err, rr.Body.String())
	}
	if rep.SessionID != "s1" || rep.TokensInput != 50 || rep.TokensOutput != 10 || rep.EventCount != 2 {
		t.Fatalf("usage report = %+v, want s1/50/10/2", rep)
	}

	// A principal without view_usage is rejected with 403.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/s1/usage", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	withPrincipal(authmw.Principal{Subject: "u@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}})(req)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("no-view_usage status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}

	// Missing session → 404 (with a view_usage principal so the gate passes).
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/nope/usage", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	withPrincipal(authmw.Principal{Subject: "bob@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleBillingViewer}})(req)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing-session usage status = %d, want 404", rr.Code)
	}
}

// TestSessionUsageTreeAggregated_spec_15_1_676 covers §15.1 line 676:
// GET /v1/sessions/{id}/usage "Returns tree-aggregated usage (including
// all descendant tasks) when the session has a delegation tree". The
// subtree mixes a live grandchild (resident in the session store) with a
// settled descendant that exists only in the §8.10 tree archive, so the
// total must draw on both sources; a leaf session reports only its own
// total with treeAggregated=false. F-15.1.31.
func TestSessionUsageTreeAggregated_spec_15_1_676(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	// root → c1 → {c2 (live), c3 (archived-only)}.
	_ = store.Create(ctx, sessionstore.Session{ID: "root", TenantID: "acme", State: session.StateCompleted, CreatedAt: time.Unix(1, 0)})
	_ = store.Create(ctx, sessionstore.Session{ID: "c1", TenantID: "acme", ParentSessionID: "root", RootSessionID: "root", State: session.StateCompleted, CreatedAt: time.Unix(2, 0)})
	_ = store.Create(ctx, sessionstore.Session{ID: "c2", TenantID: "acme", ParentSessionID: "c1", RootSessionID: "root", State: session.StateCompleted, CreatedAt: time.Unix(3, 0)})

	archive := treearchive.NewMemory()
	// c3 has settled and its live row is gone; only the archive remembers it.
	_ = archive.Archive(ctx, treearchive.ArchivedNode{TenantID: "acme", RootSessionID: "root", NodeSessionID: "c3", ParentSessionID: "c1", State: string(session.StateCompleted)})

	billing := billingstore.NewMemory()
	for _, e := range []billingstore.Event{
		{TenantID: "acme", SessionID: "root", EventType: billingstore.EventSessionCompleted, TokensInput: 10, TokensOutput: 1},
		{TenantID: "acme", SessionID: "c1", EventType: billingstore.EventSessionCompleted, TokensInput: 20, TokensOutput: 2},
		{TenantID: "acme", SessionID: "c2", EventType: billingstore.EventSessionCompleted, TokensInput: 30, TokensOutput: 3},
		{TenantID: "acme", SessionID: "c3", EventType: billingstore.EventSessionCompleted, TokensInput: 40, TokensOutput: 4},
	} {
		_, _ = billing.Append(ctx, e)
	}

	srv := sessionserver.New(store, sessionserver.Options{Billing: billing, TreeArchive: archive})
	h := srv.Handler()

	type usageRep struct {
		SessionID      string `json:"sessionId"`
		TokensInput    uint64 `json:"tokensInput"`
		TokensOutput   uint64 `json:"tokensOutput"`
		EventCount     int    `json:"eventCount"`
		TreeAggregated bool   `json:"treeAggregated"`
	}
	get := func(id string) usageRep {
		t.Helper()
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+id+"/usage", nil)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		withPrincipal(authmw.Principal{Subject: "bob@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleBillingViewer}})(req)
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("usage(%s) status = %d, want 200; body=%s", id, rr.Code, rr.Body.String())
		}
		var rep usageRep
		if err := json.Unmarshal(rr.Body.Bytes(), &rep); err != nil {
			t.Fatalf("decode usage(%s): %v", id, err)
		}
		return rep
	}

	// Root folds in c1 (live), c2 (live), and c3 (archive-only): 100/10/4.
	root := get("root")
	if root.TokensInput != 100 || root.TokensOutput != 10 || root.EventCount != 4 || !root.TreeAggregated {
		t.Fatalf("root usage = %+v, want 100/10/4 treeAggregated=true", root)
	}

	// c1 folds in c2 and c3 but not root: 90/9/3.
	c1 := get("c1")
	if c1.TokensInput != 90 || c1.TokensOutput != 9 || c1.EventCount != 3 || !c1.TreeAggregated {
		t.Fatalf("c1 usage = %+v, want 90/9/3 treeAggregated=true", c1)
	}

	// c2 is a leaf — only its own total, not tree-aggregated.
	c2 := get("c2")
	if c2.TokensInput != 30 || c2.TokensOutput != 3 || c2.EventCount != 1 || c2.TreeAggregated {
		t.Fatalf("c2 usage = %+v, want 30/3/1 treeAggregated=false", c2)
	}
}
