// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
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
