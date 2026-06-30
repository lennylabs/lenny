// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// finalizeTestServer builds a minimal session server for the finalize
// plan-binding tests.
func finalizeTestServer(t *testing.T) (*Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	clock := func() time.Time { return time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC) }
	return New(store, Options{Clock: clock}), store
}

func seedCreated(t *testing.T, store sessionstore.Store, id string, plan string) {
	t.Helper()
	row := sessionstore.Session{
		ID: id, TenantID: "default", UserID: "alice",
		RuntimeRef: "claude-code", State: session.StateCreated,
		CreatedAt: time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC),
	}
	row.UpdatedAt = row.CreatedAt
	if plan != "" {
		row.WorkspacePlan = []byte(plan)
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func sessionScopedRef(t *testing.T, sessionID string) string {
	t.Helper()
	return blobstore.URI{
		TenantID:   "default",
		ObjectType: blobstore.ObjectTypeUpload,
		SessionID:  sessionID,
		PartID:     "p1",
		TTL:        time.Hour,
	}.String()
}

func postFinalize(t *testing.T, srv *Server, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/finalize", nil)
	} else {
		r = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/finalize", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("X-Lenny-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, r)
	return rr
}

// TestFinalizeBindsUploadArchivePlan_spec_26_2 confirms POST
// /v1/sessions/{id}/finalize accepts a §14 plan referencing this session's
// staged uploadArchive blob, stores it on the row, and transitions to
// ready. This closes the §26.2↔§15.1 upload-binding ordering gap.
// spec: §7.1 step 11; §26.2 lines 95-114. F-24.17.4 / F-26.2.4.
func TestFinalizeBindsUploadArchivePlan_spec_26_2(t *testing.T) {
	srv, store := finalizeTestServer(t)
	seedCreated(t, store, "sess_ws", "")
	ref := sessionScopedRef(t, "sess_ws")
	body := `{"workspacePlan":{"schemaVersion":1,"sources":[{"type":"uploadArchive","pathPrefix":".","uploadRef":"` + ref + `","format":"tar.gz"}]}}`

	rr := postFinalize(t, srv, "sess_ws", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "default", "sess_ws")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.State != session.StateReady {
		t.Errorf("state = %q, want ready", row.State)
	}
	if !strings.Contains(string(row.WorkspacePlan), "uploadArchive") ||
		!strings.Contains(string(row.WorkspacePlan), ref) {
		t.Errorf("stored plan = %s", row.WorkspacePlan)
	}
}

// TestFinalizeRejectsForeignUploadRef_spec_12_5 confirms a plan whose
// uploadRef points at another session's blob prefix is rejected, so
// finalize cannot bind a plan to a blob the caller did not stage for this
// session. spec: §12.5 line 295; §13.4.
func TestFinalizeRejectsForeignUploadRef_spec_12_5(t *testing.T) {
	srv, store := finalizeTestServer(t)
	seedCreated(t, store, "sess_a", "")
	foreign := sessionScopedRef(t, "sess_other")
	body := `{"workspacePlan":{"schemaVersion":1,"sources":[{"type":"uploadArchive","pathPrefix":".","uploadRef":"` + foreign + `","format":"tar.gz"}]}}`

	rr := postFinalize(t, srv, "sess_a", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "upload_ref_foreign_session") {
		t.Errorf("body = %s, want upload_ref_foreign_session", rr.Body.String())
	}
	// The row must remain created (no transition on rejection).
	row, _ := store.Get(context.Background(), "default", "sess_a")
	if row.State != session.StateCreated {
		t.Errorf("state = %q, want created (unchanged)", row.State)
	}
}

// TestFinalizeRejectsForeignTenantRef confirms a cross-tenant uploadRef is
// rejected even when the session segment matches. spec: §12.5 line 295.
func TestFinalizeRejectsForeignTenantRef(t *testing.T) {
	srv, store := finalizeTestServer(t)
	seedCreated(t, store, "sess_t", "")
	foreign := blobstore.URI{
		TenantID:   "globex",
		ObjectType: blobstore.ObjectTypeUpload,
		SessionID:  "sess_t",
		PartID:     "p1",
		TTL:        time.Hour,
	}.String()
	body := `{"workspacePlan":{"schemaVersion":1,"sources":[{"type":"uploadArchive","pathPrefix":".","uploadRef":"` + foreign + `","format":"tar.gz"}]}}`

	rr := postFinalize(t, srv, "sess_t", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "upload_ref_foreign_session") {
		t.Errorf("body = %s", rr.Body.String())
	}
}

// TestFinalizeRejectsPlanWhenAlreadySet confirms finalize cannot replace a
// create-time plan. spec: §14.
func TestFinalizeRejectsPlanWhenAlreadySet(t *testing.T) {
	srv, store := finalizeTestServer(t)
	seedCreated(t, store, "sess_dup", `{"schemaVersion":1,"sources":[]}`)
	ref := sessionScopedRef(t, "sess_dup")
	body := `{"workspacePlan":{"schemaVersion":1,"sources":[{"type":"uploadArchive","pathPrefix":".","uploadRef":"` + ref + `","format":"tar.gz"}]}}`

	rr := postFinalize(t, srv, "sess_dup", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "plan_already_set") {
		t.Errorf("body = %s, want plan_already_set", rr.Body.String())
	}
}

// TestFinalizeNoBodyKeepsEmptyPlan confirms a no-body finalize still
// transitions to ready without binding a plan (regression for the
// pre-existing single-shot path). spec: §7.1 step 11.
func TestFinalizeNoBodyKeepsEmptyPlan(t *testing.T) {
	srv, store := finalizeTestServer(t)
	seedCreated(t, store, "sess_nb", "")

	rr := postFinalize(t, srv, "sess_nb", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "default", "sess_nb")
	if row.State != session.StateReady {
		t.Errorf("state = %q, want ready", row.State)
	}
	if len(row.WorkspacePlan) != 0 && !isJSONNull(row.WorkspacePlan) {
		t.Errorf("plan should stay empty, got %s", row.WorkspacePlan)
	}
}

// TestFinalizeRejectsMalformedBody confirms a non-JSON finalize body is a
// 400 rather than a silent no-plan finalize. spec: §15.1.
func TestFinalizeRejectsMalformedBody(t *testing.T) {
	srv, store := finalizeTestServer(t)
	seedCreated(t, store, "sess_bad", "")

	rr := postFinalize(t, srv, "sess_bad", `{bad json`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "default", "sess_bad")
	if row.State != session.StateCreated {
		t.Errorf("state = %q, want created (unchanged after rejection)", row.State)
	}
}

// TestMapFinalizeCredentialMismatch confirms that the §4.9 check-to-assignment
// mismatch surfaced at /finalize remaps the create-time credential sentinels
// (ErrUserCredentialNotFound, ErrNoCredentialAvailable) to the
// CredentialAssignmentError envelope so writePodClaimError reports
// CREDENTIAL_POOL_EXHAUSTED rather than the create-only USER_CREDENTIAL_NOT_FOUND
// or pre-claim envelope. Per §7.6 the user-credential lookup and the
// without-fallback not-found rejection stay a POST /v1/sessions error; a source
// that vanishes across the upload window is the finalize-time mismatch. An
// unrelated error passes through unchanged so it keeps its own envelope.
// spec: §4.9 line 1220; §7.3 line 138, §7.6 line 153 (proposal).
func TestMapFinalizeCredentialMismatch(t *testing.T) {
	otherErr := errors.New("kube-api read failure")
	cases := []struct {
		name       string
		in         error
		wantCredAs bool
	}{
		{"user-credential-not-found becomes mismatch", credrouter.ErrUserCredentialNotFound, true},
		{"no-credential-available becomes mismatch", credrouter.ErrNoCredentialAvailable, true},
		{"unrelated error passes through", otherErr, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapFinalizeCredentialMismatch(tc.in)
			var credAs *podsession.CredentialAssignmentError
			if errors.As(got, &credAs) != tc.wantCredAs {
				t.Fatalf("mapFinalizeCredentialMismatch(%v) credAs=%v, want %v", tc.in, !tc.wantCredAs, tc.wantCredAs)
			}
			if !tc.wantCredAs && !errors.Is(got, tc.in) {
				t.Errorf("unrelated error not preserved: got %v, want %v", got, tc.in)
			}
		})
	}
}

// TestFinalizeCredentialMismatchSurfacesPoolExhausted confirms that the
// remapped finalize-time credential mismatch routes through writePodClaimError
// to the §4.9 CREDENTIAL_POOL_EXHAUSTED envelope with the assignment_race
// reason and emits the preclaim-mismatch metric, rather than the create-only
// USER_CREDENTIAL_NOT_FOUND 404. This pins the §7.3 line 138 / §7.6 line 153
// rule that USER_CREDENTIAL_NOT_FOUND is not a finalize trigger.
// spec: §4.9 line 1220; §7.3 line 138; §7.6 line 153 (proposal).
func TestFinalizeCredentialMismatchSurfacesPoolExhausted(t *testing.T) {
	var gotPool, gotProvider string
	mismatchCalled := false
	s := &Server{preclaimMismatch: func(pool, provider string) {
		gotPool, gotProvider, mismatchCalled = pool, provider, true
	}}
	rr := httptest.NewRecorder()
	// A user-only miss observed at finalize is the check-to-assignment mismatch,
	// not a create-time not-found.
	s.writePodClaimError(rr, mapFinalizeCredentialMismatch(credrouter.ErrUserCredentialNotFound),
		"SESSION_CREATION_FAILED", "workspace finalization failed")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (CREDENTIAL_POOL_EXHAUSTED), not 404 USER_CREDENTIAL_NOT_FOUND; body=%s",
			rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "CREDENTIAL_POOL_EXHAUSTED" {
		t.Errorf("code = %q, want CREDENTIAL_POOL_EXHAUSTED", env.Error.Code)
	}
	if env.Error.Details["reason"] != "assignment_race" {
		t.Errorf("reason = %v, want assignment_race", env.Error.Details["reason"])
	}
	// The mismatch metric is emitted, counting the same pre-check-passes-then-
	// assignment-fails event now observed at finalize (§7.6 line 153).
	if !mismatchCalled {
		t.Errorf("preclaim-mismatch metric not emitted for the finalize credential mismatch")
	}
	_ = gotPool
	_ = gotProvider
}

// TestFinalizeBindsUploadFilePlan confirms a uploadFile source is also
// scope-validated and bound. spec: §14 uploadFile; §26.2 line 213.
func TestFinalizeBindsUploadFilePlan(t *testing.T) {
	srv, store := finalizeTestServer(t)
	seedCreated(t, store, "sess_uf", "")
	ref := sessionScopedRef(t, "sess_uf")
	body := `{"workspacePlan":{"schemaVersion":1,"sources":[{"type":"uploadFile","path":"config.yaml","uploadRef":"` + ref + `"}]}}`

	rr := postFinalize(t, srv, "sess_uf", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "default", "sess_uf")
	if !strings.Contains(string(row.WorkspacePlan), "uploadFile") {
		t.Errorf("stored plan = %s", row.WorkspacePlan)
	}
}
