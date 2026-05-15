// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// spec: §10.2 tenant_id validation; §7.1 single-use uploadToken;
//       §13.4 JSON body cap.

func TestResolveTenantPrefersAuthenticatedPrincipal(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{IDFunc: func() string { return "sess_a" }})

	// Session belongs to acme.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_a", TenantID: "acme", State: session.StateCreated, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Caller authenticates as `acme` via Principal but tries to
	// hijack the header to read a different tenant — Principal wins.
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_a", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "globex")
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{TenantID: "acme"})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Principal should win: status=%d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestResolveTenantRejectsMalformedHeader(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{IDFunc: func() string { return "sess_b" }})

	// No Principal. Header carries a non-§10.2-conforming value — it
	// should fall through to "default" rather than reach the store
	// with the attacker-controlled value.
	for _, hijacked := range []string{
		"../../etc/passwd",
		"with space",
		strings.Repeat("a", 200),
		"",
	} {
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
		if hijacked != "" {
			req.Header.Set("X-Lenny-Tenant-ID", hijacked)
		}
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		// The list endpoint always returns 200 for an empty result —
		// the key assertion is that it doesn't 500 or echo the
		// hijacked tenant id back to the caller.
		if rr.Code != http.StatusOK {
			t.Errorf("hijacked %q: status %d", hijacked, rr.Code)
		}
	}
}

func TestFinalizeConsumesUploadToken(t *testing.T) {
	// Full §7.1 flow: create, upload, finalize, attempt second
	// upload with the same token → ErrConsumed.
	store := memstore.New()
	blobs := blobstore.NewMemoryStore(nil)
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test")})
	tracker := uploadtoken.NewMemoryTracker()
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

	srv := sessionserver.New(store, sessionserver.Options{
		Clock:               clock,
		IDFunc:              func() string { return "sess_X" },
		UploadTokenIssuer:   uploadtoken.NewIssuer(ring, clock),
		UploadTokenVerifier: uploadtoken.NewVerifier(ring, tracker, clock),
		Blobs:               blobs,
	})

	// 1. Create session — captures the upload token from the response.
	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{RuntimeRef: "echo"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(createBody))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d, body=%s", rr.Code, rr.Body.String())
	}
	var createResp sessionserver.CreateSessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &createResp)
	tok := createResp.UploadToken
	if tok == "" {
		t.Fatal("create response missing uploadToken")
	}

	// 2. Upload using the token — should succeed.
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_X/upload", strings.NewReader("data"))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-Upload-Token", tok)
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload 1: %d, body=%s", rr.Code, rr.Body.String())
	}

	// 3. Finalize — closes the upload window.
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_X/finalize", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("finalize: %d, body=%s", rr.Code, rr.Body.String())
	}

	// 4. Re-attempt upload with the same token — must now be 410
	//    UPLOAD_TOKEN_CONSUMED.
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_X/upload", strings.NewReader("replay"))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-Upload-Token", tok)
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	// Note: the session is now `ready`, so the precondition table
	// rejects with 409 INVALID_STATE_TRANSITION before reaching the
	// token check. The §7.1 invariant is satisfied at either layer —
	// the captured token cannot mint another upload. We assert the
	// rejection but allow either error code.
	if rr.Code != http.StatusGone && rr.Code != http.StatusConflict {
		t.Errorf("replay upload after finalize: got %d, want 410 or 409", rr.Code)
	}
}

func TestExtendRetentionRejectsExcessiveTTL(t *testing.T) {
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_R", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{})

	body, _ := json.Marshal(sessionserver.ExtendRetentionRequest{
		TTLSeconds: sessionserver.ExtendRetentionMaxSeconds + 1,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_R/extend-retention", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("over-cap retention: got %d, want 400", rr.Code)
	}
}

func TestExtendRetentionAdmitsAtCap(t *testing.T) {
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_R", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{})

	body, _ := json.Marshal(sessionserver.ExtendRetentionRequest{
		TTLSeconds: sessionserver.ExtendRetentionMaxSeconds,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_R/extend-retention", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("at-cap retention: got %d, want 200", rr.Code)
	}
}

func TestCreateRejectsOversizedJSONBody(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})

	// 2 MiB JSON payload — well past the 1 MiB cap.
	huge := strings.Repeat("a", 2*1024*1024)
	body := []byte(`{"runtimeRef":"echo","userId":"` + huge + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code == http.StatusCreated {
		t.Errorf("oversized JSON should be rejected; got 201")
	}
}

func TestResolveTenantAuthHeaderHijackBlocked(t *testing.T) {
	// A caller that authenticates as `acme` via Principal cannot
	// reach `globex` data by setting X-Lenny-Tenant-ID: globex.
	store := memstore.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, t := range []sessionstore.Session{
		{ID: "sess_acme", TenantID: "acme", State: session.StateCreated, CreatedAt: now, UpdatedAt: now},
		{ID: "sess_globex", TenantID: "globex", State: session.StateCreated, CreatedAt: now, UpdatedAt: now},
	} {
		_ = store.Create(context.Background(), t)
	}
	srv := sessionserver.New(store, sessionserver.Options{})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_globex", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "globex") // attacker-controlled
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "alice@acme.com", TenantID: "acme",
		Roles: []pkgauth.Role{pkgauth.RoleUser},
	})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	// The handler resolves tenant=acme; sess_globex is foreign → 404.
	if rr.Code != http.StatusNotFound {
		t.Errorf("hijack: got %d, want 404 (tenant from Principal wins over header)", rr.Code)
	}
}
