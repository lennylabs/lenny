// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

func getSubresource(t *testing.T, h http.Handler, path, tenant string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	h.ServeHTTP(rr, req)
	return rr
}

// TestSetupOutput_spec_15_1_674 verifies GET /v1/sessions/{id}/setup-output
// surfaces the §7.5 captured command output (executed and rejected
// entries), returns an empty list for a session that ran no setup, and
// 404s a missing or cross-tenant session. spec: §15.1 line 674; §7.5
// lines 475, 488. F-15.1.3 / F-7.5.4 / F-7.5.11.
func TestSetupOutput_spec_15_1_674(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	_ = store.Create(ctx, sessionstore.Session{
		ID: "s1", TenantID: "acme", State: session.StateCompleted, CreatedAt: time.Unix(1, 0),
		SetupOutput: []sessionstore.SetupCommandOutput{
			{Cmd: "npm ci", ExitCode: 0, Stdout: "added 12 packages", DurationMs: 940, Truncated: true},
			{Cmd: "rm -rf /", Rejected: true, RejectionReason: "setup_command_policy_violation"},
		},
	})
	_ = store.Create(ctx, sessionstore.Session{ID: "bare", TenantID: "acme", State: session.StateCompleted, CreatedAt: time.Unix(2, 0)})
	srv := sessionserver.New(store, sessionserver.Options{})
	h := srv.Handler()

	// Happy path: both the executed and the synthetic rejection entry surface.
	rr := getSubresource(t, h, "/v1/sessions/s1/setup-output", "acme")
	if rr.Code != http.StatusOK {
		t.Fatalf("setup-output status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Items []sessionserver.SetupOutputEntry `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	if len(env.Items) != 2 {
		t.Fatalf("items = %d, want 2; body=%s", len(env.Items), rr.Body.String())
	}
	if env.Items[0].Cmd != "npm ci" || env.Items[0].Stdout != "added 12 packages" || !env.Items[0].Truncated {
		t.Errorf("executed entry = %+v", env.Items[0])
	}
	if !env.Items[1].Rejected || env.Items[1].RejectionReason != "setup_command_policy_violation" {
		t.Errorf("rejected entry = %+v", env.Items[1])
	}

	// A session that ran no setup commands returns an empty list, not 404.
	rr = getSubresource(t, h, "/v1/sessions/bare/setup-output", "acme")
	if rr.Code != http.StatusOK {
		t.Fatalf("bare setup-output status = %d, want 200", rr.Code)
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode bare: %v", err)
	}
	if len(env.Items) != 0 {
		t.Errorf("bare items = %d, want 0", len(env.Items))
	}

	// Missing session → 404.
	if rr := getSubresource(t, h, "/v1/sessions/nope/setup-output", "acme"); rr.Code != http.StatusNotFound {
		t.Errorf("missing setup-output status = %d, want 404", rr.Code)
	}
	// Cross-tenant probe → 404 (the store never leaks a foreign session).
	if rr := getSubresource(t, h, "/v1/sessions/s1/setup-output", "globex"); rr.Code != http.StatusNotFound {
		t.Errorf("cross-tenant setup-output status = %d, want 404", rr.Code)
	}
}

// TestWorkspaceDownload_spec_15_1_671 verifies GET
// /v1/sessions/{id}/workspace streams the §4.5 workspace snapshot,
// resolves both the lenny-blob:// URI and path-style ref forms, fails a
// cross-tenant ref closed, and 404s a session with no snapshot. spec:
// §15.1 lines 671, 661; §4.5 line 311. F-15.1.3.
func TestWorkspaceDownload_spec_15_1_671(t *testing.T) {
	ctx := context.Background()
	blobs := blobstore.NewMemoryStore(nil)
	put := func(part string) string {
		u := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace, SessionID: "s1", PartID: part, TTL: time.Hour}
		if _, err := blobs.Put(u, "application/gzip", strings.NewReader("TARBALL:"+part)); err != nil {
			t.Fatalf("put %s: %v", part, err)
		}
		return u.String()
	}
	uriRef := put("snap-uri")
	put("snap-path")

	store := memstore.New()
	_ = store.Create(ctx, sessionstore.Session{
		ID: "s1", TenantID: "acme", State: session.StateCompleted, CreatedAt: time.Unix(1, 0),
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: uriRef, ContentHash: "abc123"},
	})
	_ = store.Create(ctx, sessionstore.Session{
		ID: "s_path", TenantID: "acme", State: session.StateCompleted, CreatedAt: time.Unix(2, 0),
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: "/acme/workspace/s1/snap-path"},
	})
	_ = store.Create(ctx, sessionstore.Session{ID: "nosnap", TenantID: "acme", State: session.StateCompleted, CreatedAt: time.Unix(3, 0)})
	_ = store.Create(ctx, sessionstore.Session{
		ID: "foreign", TenantID: "acme", State: session.StateCompleted, CreatedAt: time.Unix(4, 0),
		// Ref names another tenant; parseSnapshotRef must reject it.
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: "/globex/workspace/s1/snap-uri"},
	})
	_ = store.Create(ctx, sessionstore.Session{
		ID: "gone", TenantID: "acme", State: session.StateCompleted, CreatedAt: time.Unix(5, 0),
		// Ref is well-formed but the object is absent from the store.
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: "lenny-blob://acme/workspace/s1/missing"},
	})

	srv := sessionserver.New(store, sessionserver.Options{Blobs: blobs})
	h := srv.Handler()

	// Happy path (URI ref): streams the bytes with MIME, disposition, hash.
	rr := getSubresource(t, h, "/v1/sessions/s1/workspace", "acme")
	if rr.Code != http.StatusOK {
		t.Fatalf("workspace status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if body, _ := io.ReadAll(rr.Body); string(body) != "TARBALL:snap-uri" {
		t.Errorf("body = %q, want TARBALL:snap-uri", body)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q, want application/gzip", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "s1-workspace.tar.gz") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if h := rr.Header().Get("X-Lenny-Workspace-Content-Hash"); h != "abc123" {
		t.Errorf("content-hash header = %q, want abc123", h)
	}

	// Path-style ref resolves the same object.
	if rr := getSubresource(t, h, "/v1/sessions/s_path/workspace", "acme"); rr.Code != http.StatusOK {
		t.Errorf("path-ref workspace status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	// No snapshot → 404 (spec line 661).
	if rr := getSubresource(t, h, "/v1/sessions/nosnap/workspace", "acme"); rr.Code != http.StatusNotFound {
		t.Errorf("no-snapshot status = %d, want 404", rr.Code)
	}
	// Cross-tenant ref → 404 (fail closed before touching the store).
	if rr := getSubresource(t, h, "/v1/sessions/foreign/workspace", "acme"); rr.Code != http.StatusNotFound {
		t.Errorf("cross-tenant-ref status = %d, want 404", rr.Code)
	}
	// Ref present but object missing/expired → 404.
	if rr := getSubresource(t, h, "/v1/sessions/gone/workspace", "acme"); rr.Code != http.StatusNotFound {
		t.Errorf("missing-object status = %d, want 404", rr.Code)
	}
	// Missing session → 404.
	if rr := getSubresource(t, h, "/v1/sessions/nope/workspace", "acme"); rr.Code != http.StatusNotFound {
		t.Errorf("missing-session status = %d, want 404", rr.Code)
	}
}

// TestWorkspaceNoBlobStore_spec_15_1_671 verifies the download reports
// 503 when the gateway has a snapshot ref but no blob store wired.
// spec: §15.1 line 671. F-15.1.3.
func TestWorkspaceNoBlobStore_spec_15_1_671(t *testing.T) {
	store := memstore.New()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", State: session.StateCompleted, CreatedAt: time.Unix(1, 0),
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: "lenny-blob://acme/workspace/s1/snap"},
	})
	srv := sessionserver.New(store, sessionserver.Options{}) // no Blobs
	if rr := getSubresource(t, srv.Handler(), "/v1/sessions/s1/workspace", "acme"); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("no-blobstore status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
}

// TestSubresourcesDeriveFailure404_spec_15_1_661 verifies the §15.1 line
// 661 bounded-reachability contract: a derive_failure audit row returns
// 200 from GET /v1/sessions/{id} but 404 from every subresource GET
// because no workspace, setup output, artifacts, or usage ever existed.
// spec: §15.1 line 661; §7.1 derive rule 2. F-15.1.3.
func TestSubresourcesDeriveFailure404_spec_15_1_661(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	_ = store.Create(ctx, sessionstore.Session{
		ID: "df", TenantID: "acme", State: session.StateFailed,
		FailureClass: session.FailureClassDeriveFailure, CreatedAt: time.Unix(1, 0),
	})
	// Wire a blob store and billing so the 404 cannot be attributed to a
	// missing dependency — only the derive_failure class drives it.
	blobs := blobstore.NewMemoryStore(nil)
	billing := billingstore.NewMemory()
	srv := sessionserver.New(store, sessionserver.Options{Blobs: blobs, Billing: billing})
	h := srv.Handler()

	// The session row itself is reachable (line 651).
	if rr := getSubresource(t, h, "/v1/sessions/df", "acme"); rr.Code != http.StatusOK {
		t.Fatalf("GET session df = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	for _, sub := range []string{"workspace", "setup-output", "artifacts", "usage"} {
		rr := getSubresource(t, h, "/v1/sessions/df/"+sub, "acme")
		if rr.Code != http.StatusNotFound {
			t.Errorf("GET /%s on derive_failure row = %d, want 404; body=%s", sub, rr.Code, rr.Body.String())
		}
	}
}
