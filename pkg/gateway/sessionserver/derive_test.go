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

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/blobstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// recordingSink captures the §7.1 derive rule 5 audit emissions so tests
// can verify the gateway emitted exactly one event with the correct
// payload.
type recordingSink struct {
	events []sessionserver.DeriveIsolationDowngradeEvent
}

func (r *recordingSink) EmitDeriveIsolationDowngrade(_ context.Context, ev sessionserver.DeriveIsolationDowngradeEvent) {
	r.events = append(r.events, ev)
}

func newSourceSession(t *testing.T, store sessionstore.Store, mods ...func(*sessionstore.Session)) sessionstore.Session {
	t.Helper()
	row := sessionstore.Session{
		ID:               "sess_source",
		TenantID:         "acme",
		UserID:           "alice@acme.com",
		State:            session.StateCompleted,
		CreatedAt:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		RuntimeRef:       "claude-code",
		PoolRef:          "default-pool",
		IsolationProfile: isolation.ProfileSandboxed,
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{
			Ref:         "/acme/workspace/sess_source/snap.tar.zst",
			Source:      sessionstore.WorkspaceSnapshotSealed,
			Timestamp:   time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC),
			ContentHash: "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
		},
	}
	for _, m := range mods {
		m(&row)
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed source session: %v", err)
	}
	return row
}

// snapshotBlobs returns a blob store holding a workspace object for the
// acme session sessionID under partID, which the path-form snapshot ref
// /acme/workspace/{sessionID}/{partID} names. Derive and replay copy that
// object and fail closed when it is absent.
func snapshotBlobs(t *testing.T, sessionID, partID string) *blobstore.MemoryStore {
	t.Helper()
	blobs := blobstore.NewMemoryStore(nil)
	uri := blobstore.URI{
		TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID: sessionID, PartID: partID, TTL: time.Hour,
	}
	if _, err := blobs.Put(uri, "application/x-tar", strings.NewReader("workspace-bytes")); err != nil {
		t.Fatalf("seed source snapshot object: %v", err)
	}
	return blobs
}

// sourceSnapshotBlobs is snapshotBlobs for the ref newSourceSession seeds.
func sourceSnapshotBlobs(t *testing.T) *blobstore.MemoryStore {
	t.Helper()
	return snapshotBlobs(t, "sess_source", "snap.tar.zst")
}

func deriveRequest(t *testing.T, h http.Handler, body sessionserver.DeriveRequest, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_source/derive", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	for _, m := range mods {
		m(req)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeError(t *testing.T, rr *httptest.ResponseRecorder) (code, msg string, details map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rr.Body.String())
	}
	return env.Error.Code, env.Error.Message, env.Error.Details
}

func TestDeriveHappyPathFromTerminal(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store)
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		Blobs:           sourceSnapshotBlobs(t),
		IDFunc:          func() string { return "sess_derived" },
		Clock:           func() time.Time { return time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC) },
	})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.DeriveResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode derive response: %v; body=%s", err, rr.Body.String())
	}
	if resp.ID != "sess_derived" {
		t.Errorf("derived id: got %q, want sess_derived", resp.ID)
	}
	if resp.State != string(session.StateCreated) {
		t.Errorf("derived state: got %q, want created", resp.State)
	}
	if resp.RuntimeRef != "claude-code" {
		t.Errorf("derived runtimeRef: got %q, want claude-code (inherited)", resp.RuntimeRef)
	}
	if resp.WorkspaceSnapshotSource != "sealed" {
		t.Errorf("workspaceSnapshotSource: got %q, want sealed", resp.WorkspaceSnapshotSource)
	}
	// spec: §4.5 — the derive response surfaces the
	// content-addressed identity so a client can verify the
	// derived session owns the inherited workspace bytes.
	if resp.WorkspaceSnapshotContentHash !=
		"b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9" {
		t.Errorf("workspaceSnapshotContentHash: got %q, want the parent's SHA-256 (spec §4.5)",
			resp.WorkspaceSnapshotContentHash)
	}
	if resp.ParentSessionID != "sess_source" {
		t.Errorf("parentSessionId: got %q, want sess_source", resp.ParentSessionID)
	}

	// Source session is untouched per §7.1 derive copy semantics.
	src, _ := store.Get(context.Background(), "acme", "sess_source")
	if src.State != session.StateCompleted {
		t.Errorf("source state mutated: got %q", src.State)
	}
}

func TestDeriveNonTerminalWithoutAllowStaleRejects(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.State = session.StateRunning
	})
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	code, _, _ := decodeError(t, rr)
	if code != "DERIVE_ON_LIVE_SESSION" {
		t.Errorf("error code: got %q, want DERIVE_ON_LIVE_SESSION", code)
	}
}

func TestDeriveNonTerminalWithAllowStaleSucceeds(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.State = session.StateRunning
		s.WorkspaceSnapshot.Source = sessionstore.WorkspaceSnapshotCheckpoint
	})
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		Blobs:           sourceSnapshotBlobs(t),
		IDFunc:          func() string { return "sess_d2" },
	})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{AllowStale: true})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.DeriveResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.WorkspaceSnapshotSource != "checkpoint" {
		t.Errorf("workspaceSnapshotSource: got %q, want checkpoint", resp.WorkspaceSnapshotSource)
	}
}

// TestDeriveEarlyStateWithAllowStaleRejected_spec_15_1 asserts the §15.1 precondition table is consulted via the central
// pkg/api/v1/session validator: even when the caller passes allowStale,
// `created/finalizing/ready/starting/input_required` are not derivable
// and the gateway must return INVALID_STATE_TRANSITION rather than fall
// through to the workspace-snapshot validator. F-15.1.29.
func TestDeriveEarlyStateWithAllowStaleRejected_spec_15_1(t *testing.T) {
	earlyStates := []session.State{
		session.StateCreated,
		session.StateFinalizing,
		session.StateReady,
		session.StateStarting,
		session.StateInputRequired,
	}
	for _, st := range earlyStates {
		t.Run(string(st), func(t *testing.T) {
			store := memstore.New()
			newSourceSession(t, store, func(s *sessionstore.Session) {
				s.State = st
			})
			srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
			rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{AllowStale: true})
			if rr.Code != http.StatusConflict {
				t.Fatalf("status: got %d, want 409; body=%s", rr.Code, rr.Body.String())
			}
			code, _, details := decodeError(t, rr)
			if code != "INVALID_STATE_TRANSITION" {
				t.Errorf("error code: got %q, want INVALID_STATE_TRANSITION", code)
			}
			if got, _ := details["currentState"].(string); got != string(st) {
				t.Errorf("details.currentState: got %q, want %q", got, st)
			}
			allowed, _ := details["allowedStates"].([]any)
			if len(allowed) == 0 {
				t.Errorf("expected details.allowedStates populated; got %v", details)
			}
		})
	}
}

func TestDeriveNoSnapshotReturns400(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.WorkspaceSnapshot = nil
	})
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	code, _, details := decodeError(t, rr)
	if code != "VALIDATION_ERROR" {
		t.Errorf("error code: got %q, want VALIDATION_ERROR", code)
	}
	fields, _ := details["fields"].([]any)
	if len(fields) == 0 {
		t.Fatalf("expected details.fields[]; details=%v", details)
	}
	first, _ := fields[0].(map[string]any)
	if first["field"] != "sourceSessionId" {
		t.Errorf("details.fields[0].field: got %v, want sourceSessionId", first["field"])
	}
}

func TestDeriveIsolationDowngradeWithoutFlagReturns422(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.IsolationProfile = isolation.ProfileMicrovm
	})
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{
		TargetIsolationProfile: isolation.ProfileSandboxed,
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422; body=%s", rr.Code, rr.Body.String())
	}
	code, _, details := decodeError(t, rr)
	if code != "ISOLATION_MONOTONICITY_VIOLATED" {
		t.Errorf("error code: got %q, want ISOLATION_MONOTONICITY_VIOLATED", code)
	}
	if details["sourceIsolationProfile"] != "microvm" || details["targetIsolationProfile"] != "sandboxed" {
		t.Errorf("details: got %v", details)
	}
}

func TestDeriveIsolationDowngradeWithoutAdminRoleReturns403(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.IsolationProfile = isolation.ProfileMicrovm
	})
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})

	// Attach a non-admin Principal via context.
	rr := deriveRequest(
		t, srv.Handler(),
		sessionserver.DeriveRequest{
			TargetIsolationProfile:  isolation.ProfileSandboxed,
			AllowIsolationDowngrade: true,
		},
		func(req *http.Request) {
			ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
				Subject:  "alice@acme.com",
				TenantID: "acme",
				Roles:    []pkgauth.Role{pkgauth.RoleTenantAdmin},
			})
			*req = *req.WithContext(ctx)
		},
	)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	code, _, _ := decodeError(t, rr)
	if code != "FORBIDDEN" {
		t.Errorf("error code: got %q, want FORBIDDEN", code)
	}
}

func TestDeriveIsolationDowngradeWithAdminEmitsAuditAndSucceeds(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.IsolationProfile = isolation.ProfileMicrovm
	})
	sink := &recordingSink{}
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		Blobs:           sourceSnapshotBlobs(t),
		IDFunc:          func() string { return "sess_downgrade" },
		DeriveAuditSink: sink,
	})

	rr := deriveRequest(
		t, srv.Handler(),
		sessionserver.DeriveRequest{
			TargetIsolationProfile:  isolation.ProfileSandboxed,
			TargetPool:              "low-iso-pool",
			AllowIsolationDowngrade: true,
			TicketID:                "JIRA-123",
		},
		func(req *http.Request) {
			ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
				Subject:  "ops@acme.com",
				TenantID: "acme",
				Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
			})
			*req = *req.WithContext(ctx)
		},
	)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if got, want := len(sink.events), 1; got != want {
		t.Fatalf("audit events: got %d, want %d", got, want)
	}
	ev := sink.events[0]
	if ev.SourceIsolationProfile != isolation.ProfileMicrovm ||
		ev.TargetIsolationProfile != isolation.ProfileSandboxed ||
		ev.AuthorizingUserSubject != "ops@acme.com" ||
		ev.TicketID != "JIRA-123" ||
		ev.TargetPool != "low-iso-pool" ||
		ev.TenantID != "acme" {
		t.Errorf("audit event mismatch: %+v", ev)
	}
}

func TestDeriveInvalidTargetProfileReturns400(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store)
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{
		TargetIsolationProfile: isolation.Profile("ferrous"),
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	code, _, _ := decodeError(t, rr)
	if code != "VALIDATION_ERROR" {
		t.Errorf("error code: got %q, want VALIDATION_ERROR", code)
	}
}

func TestDeriveSourceNotFoundReturns404(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeriveCrossTenantReturns404(t *testing.T) {
	// §4.2 tenant isolation: deriving a foreign-tenant session yields
	// 404 (existence is never leaked).
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) { s.TenantID = "globex" })
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeriveInheritsRuntimeAndUserAndPool(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store)
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		Blobs:           sourceSnapshotBlobs(t),
		IDFunc:          func() string { return "sess_inherit" },
	})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	derived, err := store.Get(context.Background(), "acme", "sess_inherit")
	if err != nil {
		t.Fatalf("fetch derived: %v", err)
	}
	if derived.RuntimeRef != "claude-code" {
		t.Errorf("RuntimeRef inheritance: got %q, want claude-code", derived.RuntimeRef)
	}
	if derived.UserID != "alice@acme.com" {
		t.Errorf("UserID inheritance: got %q, want alice@acme.com", derived.UserID)
	}
	if derived.PoolRef != "default-pool" {
		t.Errorf("PoolRef inheritance: got %q, want default-pool", derived.PoolRef)
	}
	if derived.ParentSessionID != "sess_source" {
		t.Errorf("ParentSessionID: got %q, want sess_source", derived.ParentSessionID)
	}
	if derived.ParentWorkspaceRef != "/acme/workspace/sess_source/snap.tar.zst" {
		t.Errorf("ParentWorkspaceRef: got %q", derived.ParentWorkspaceRef)
	}
	// The derived snapshot is rewritten to the derived session's own path.
	if derived.WorkspaceSnapshot == nil ||
		derived.WorkspaceSnapshot.Ref == "/acme/workspace/sess_source/snap.tar.zst" {
		t.Errorf("derived snapshot ref must differ from source's; got %+v", derived.WorkspaceSnapshot)
	}
}

func TestDeriveOverrideRuntime(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store)
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		Blobs:           sourceSnapshotBlobs(t),
		IDFunc:          func() string { return "sess_override" },
	})
	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{
		RuntimeRef: "gemini-cli",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	derived, _ := store.Get(context.Background(), "acme", "sess_override")
	if derived.RuntimeRef != "gemini-cli" {
		t.Errorf("RuntimeRef override: got %q, want gemini-cli", derived.RuntimeRef)
	}
}

func TestDeriveEqualIsolationSucceedsWithoutOverride(t *testing.T) {
	// SEC-001 is satisfied when target == source (no downgrade).
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.IsolationProfile = isolation.ProfileSandboxed
	})
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		Blobs:           sourceSnapshotBlobs(t),
		IDFunc:          func() string { return "sess_eq" },
	})
	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{
		TargetIsolationProfile: isolation.ProfileSandboxed,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeriveStrictlyStricterIsolationSucceeds(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.IsolationProfile = isolation.ProfileSandboxed
	})
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		Blobs:           sourceSnapshotBlobs(t),
		IDFunc:          func() string { return "sess_stricter" },
	})
	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{
		TargetIsolationProfile: isolation.ProfileMicrovm,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
}

// TestDeriveCopiesParentBytesIntoDerivedURI asserts the §4.5 ll. 311
// byte-copy semantics: when the gateway's blob store implements
// blobstore.Copier (production MinIO/S3/GCS), derive uses Copy to
// duplicate the parent's workspace snapshot into a new object
// scoped to the derived session's prefix, and then the derived
// session's blob survives the parent's session-scoped erasure.
//
// spec: §4.5 — "the gateway copies the parent's workspace
// snapshot bytes into a new MinIO object scoped to the derived
// session's own path. The derived session owns its artifact
// independently; GC on the parent's artifacts has no effect on the
// derived session's workspace".
func TestDeriveCopiesParentBytesIntoDerivedURI(t *testing.T) {
	store := memstore.New()
	blobs := blobstore.NewMemoryStore(nil)
	// Seed a parent session + a parent workspace blob.
	const (
		parentTenant  = "acme"
		parentSession = "sess_source"
	)
	parentURI := blobstore.URI{
		TenantID:   parentTenant,
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  parentSession,
		PartID:     "snap",
		TTL:        time.Hour,
	}
	if _, err := blobs.Put(parentURI, "application/x-tar", strings.NewReader("workspace-bytes")); err != nil {
		t.Fatalf("seed parent blob: %v", err)
	}
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.WorkspaceSnapshot.Ref = parentURI.String()
	})
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		IDFunc:          func() string { return "sess_derived" },
		Blobs:           blobs,
	})
	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusCreated {
		t.Fatalf("derive: %d; body=%s", rr.Code, rr.Body.String())
	}
	// The derived session's bytes must exist independent of the
	// parent: erase the parent and read the derived URI.
	if _, err := blobs.DeleteBySession(context.Background(), parentTenant, parentSession); err != nil {
		t.Fatalf("erase parent: %v", err)
	}
	derived, err := store.Get(context.Background(), parentTenant, "sess_derived")
	if err != nil {
		t.Fatalf("fetch derived: %v", err)
	}
	if derived.WorkspaceSnapshot == nil {
		t.Fatal("derived has no workspace snapshot")
	}
	derivedURI := blobstore.URI{
		TenantID:   parentTenant,
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "sess_derived",
		PartID:     "derived-from-" + parentSession,
		TTL:        time.Hour,
	}
	if _, _, err := blobs.Get(derivedURI); err != nil {
		t.Errorf("derived blob missing after parent erasure: %v (spec §4.5 ll. 311)", err)
	}
}

// deriveSnapshotSource derives from the seeded sess_source row and returns
// the workspaceSnapshotSource the 201 response reports.
func deriveSnapshotSource(t *testing.T, srv *sessionserver.Server, newID string) string {
	t.Helper()
	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusCreated {
		t.Fatalf("derive %s: status %d, want 201; body=%s", newID, rr.Code, rr.Body.String())
	}
	var resp sessionserver.DeriveResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode derive response: %v; body=%s", err, rr.Body.String())
	}
	return resp.WorkspaceSnapshotSource
}

// TestDeriveTerminalSourceReportsRecordedSnapshotSource_spec_7_1 pins
// derive rule 1 for a terminal source: every terminal state derives from
// the snapshot its row records, the sealed workspace when the seal
// succeeded and the latest checkpoint otherwise, and the response reports
// that source. A `failed` row records `checkpoint` after a seal-timeout
// relabel and `sealed` after a seal that succeeded before a later failure.
// The code already derives this way; the case set pins it against the
// merged rule.
//
// spec: §7.1 (Normal Flow)
func TestDeriveTerminalSourceReportsRecordedSnapshotSource_spec_7_1(t *testing.T) {
	sources := []sessionstore.WorkspaceSnapshotSource{
		sessionstore.WorkspaceSnapshotSealed,
		sessionstore.WorkspaceSnapshotCheckpoint,
	}
	for _, state := range session.TerminalStates() {
		for _, src := range sources {
			t.Run(string(state)+"/"+string(src), func(t *testing.T) {
				store := memstore.New()
				newSourceSession(t, store, func(s *sessionstore.Session) {
					s.State = state
					s.WorkspaceSnapshot.Source = src
				})
				srv := sessionserver.New(store, sessionserver.Options{
					DevTenantHeader: true,
					Blobs:           sourceSnapshotBlobs(t),
					IDFunc:          func() string { return "sess_derived" },
				})
				if got := deriveSnapshotSource(t, srv, "sess_derived"); got != string(src) {
					t.Errorf("workspaceSnapshotSource for %s source: got %q, want %q", state, got, src)
				}
			})
		}
	}
}

// TestDeriveCompletedSourceDuringSealWindow_spec_7_1 pins derive during the
// seal window of a `completed` session: before the seal records its
// snapshot the row holds the latest checkpoint, the derive copies it and
// reports `checkpoint`, and once the seal records the sealed snapshot a
// second derive reports `sealed`. No derive waits for the seal.
//
// spec: §7.1 (Normal Flow)
func TestDeriveCompletedSourceDuringSealWindow_spec_7_1(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store, func(s *sessionstore.Session) {
		s.WorkspaceSnapshot.Source = sessionstore.WorkspaceSnapshotCheckpoint
		s.WorkspaceSnapshot.Ref = "/acme/workspace/sess_source/ckpt.tar.zst"
	})
	ids := []string{"sess_derived_1", "sess_derived_2"}
	next := 0
	// Both the checkpoint and the sealed snapshot exist, so each derive
	// copies the object its row names at the time.
	blobs := sourceSnapshotBlobs(t)
	ckpt := blobstore.URI{
		TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID: "sess_source", PartID: "ckpt.tar.zst", TTL: time.Hour,
	}
	if _, err := blobs.Put(ckpt, "application/x-tar", strings.NewReader("checkpoint-bytes")); err != nil {
		t.Fatalf("seed checkpoint object: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		Blobs:           blobs,
		IDFunc: func() string {
			id := ids[next]
			next++
			return id
		},
	})

	if got := deriveSnapshotSource(t, srv, ids[0]); got != string(sessionstore.WorkspaceSnapshotCheckpoint) {
		t.Fatalf("derive before the seal: workspaceSnapshotSource %q, want checkpoint", got)
	}

	ctx := context.Background()
	if _, err := store.Update(ctx, "acme", "sess_source", func(s *sessionstore.Session) error {
		s.WorkspaceSnapshot = &sessionstore.WorkspaceSnapshot{
			Ref:         "/acme/workspace/sess_source/snap.tar.zst",
			Source:      sessionstore.WorkspaceSnapshotSealed,
			Timestamp:   time.Date(2026, 1, 1, 0, 6, 0, 0, time.UTC),
			ContentHash: "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9",
		}
		return nil
	}); err != nil {
		t.Fatalf("record sealed snapshot: %v", err)
	}

	if got := deriveSnapshotSource(t, srv, ids[1]); got != string(sessionstore.WorkspaceSnapshotSealed) {
		t.Errorf("derive after the seal: workspaceSnapshotSource %q, want sealed", got)
	}
}
