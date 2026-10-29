// SPDX-License-Identifier: MIT

package sessionserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// bindTestSessionID is the ID every create-time binding case mints, so a
// case can assert that no row was persisted under it.
const bindTestSessionID = "sess_bind"

// countingRefResolver counts Resolve calls so an ordering case can assert
// that no gitClone ls-remote ran.
type countingRefResolver struct{ calls atomic.Int32 }

func (r *countingRefResolver) Resolve(context.Context, workspaceplan.GitClone, workspaceplan.VCSCredential) (string, error) {
	r.calls.Add(1)
	return strings.Repeat("a", 40), nil
}

// bindTestServer builds a session server whose ID function mints
// bindTestSessionID, with the given RefResolver and runtime store.
func bindTestServer(t *testing.T, resolver workspaceplan.RefResolver, runtimes runtimestore.Store) (*Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	clock := func() time.Time { return time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC) }
	opts := Options{
		DevTenantHeader: true,
		Clock:           clock,
		IDFunc:          func() string { return bindTestSessionID },
		RefResolver:     resolver,
	}
	if runtimes != nil {
		opts.Runtimes = runtimes
	}
	return New(store, opts), store
}

// postBindRequest posts a JSON body carrying the plan to path for tenant
// "default".
func postBindRequest(t *testing.T, srv *Server, path string, plan json.RawMessage) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	var err error
	if path == "/v1/sessions/start" {
		body, err = json.Marshal(CreateAndStartRequest{RuntimeRef: "claude-code", WorkspacePlan: plan})
	} else {
		body, err = json.Marshal(CreateSessionRequest{RuntimeRef: "claude-code", WorkspacePlan: plan})
	}
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Lenny-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, r)
	return rr
}

// bindErrorEnvelope decodes the error code and details of a rejection.
func bindErrorEnvelope(t *testing.T, rr *httptest.ResponseRecorder) (string, map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, rr.Body.String())
	}
	return env.Error.Code, env.Error.Details
}

// assertNoRowPersisted fails when the create-time rejection left a row.
func assertNoRowPersisted(t *testing.T, store sessionstore.Store) {
	t.Helper()
	if _, err := store.Get(context.Background(), "default", bindTestSessionID); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("store.Get(%s) err = %v, want ErrNotFound: a rejected create persisted a row", bindTestSessionID, err)
	}
}

// uploadSourcePlan returns a plan with one upload source of the given type
// naming ref.
func uploadSourcePlan(sourceType, ref string) json.RawMessage {
	if sourceType == workspaceplan.TypeUploadArchive {
		return json.RawMessage(`{"schemaVersion":1,"sources":[{"type":"uploadArchive","pathPrefix":".","uploadRef":"` +
			ref + `","format":"tar.gz"}]}`)
	}
	return json.RawMessage(`{"schemaVersion":1,"sources":[{"type":"uploadFile","path":"a.txt","uploadRef":"` + ref + `"}]}`)
}

// spec: 14.1 (WorkspacePlan Schema Versioning), 14 (Workspace Plan Schema)
//
// A plan bound at creation names no upload of the session it creates,
// because the session ID is minted after the plan is validated. Every
// uploadFile and uploadArchive source in a POST /v1/sessions or POST
// /v1/sessions/start plan is rejected under 400 VALIDATION_ERROR, and no
// row is persisted. Before the scope check moved into the shared resolver,
// these creates were admitted with another session's staged blob.
func TestCreateTimePlanRejectsUploadSources_spec_14_1(t *testing.T) {
	wellFormed := blobstore.URI{
		TenantID:   "default",
		ObjectType: blobstore.ObjectTypeUpload,
		SessionID:  "sess_other",
		PartID:     "p1",
		TTL:        time.Hour,
	}.String()
	cases := []struct {
		name       string
		sourceType string
		ref        string
		wantReason string
	}{
		{"uploadFile naming another session", workspaceplan.TypeUploadFile, wellFormed, "upload_ref_foreign_session"},
		{"uploadArchive naming another session", workspaceplan.TypeUploadArchive, wellFormed, "upload_ref_foreign_session"},
		{"uploadFile with a non-URI ref", workspaceplan.TypeUploadFile, "upload_abc123", "invalid_upload_ref"},
		{"uploadArchive with a non-URI ref", workspaceplan.TypeUploadArchive, "upload_abc123", "invalid_upload_ref"},
	}
	for _, path := range []string{"/v1/sessions", "/v1/sessions/start"} {
		for _, tc := range cases {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				srv, store := bindTestServer(t, nil, nil)
				rr := postBindRequest(t, srv, path, uploadSourcePlan(tc.sourceType, tc.ref))
				if rr.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
				}
				code, details := bindErrorEnvelope(t, rr)
				if code != "VALIDATION_ERROR" {
					t.Errorf("code = %q, want VALIDATION_ERROR", code)
				}
				if details["reason"] != tc.wantReason {
					t.Errorf("details.reason = %v, want %q", details["reason"], tc.wantReason)
				}
				if details["field"] != "sources[0].uploadRef" {
					t.Errorf("details.field = %v, want sources[0].uploadRef", details["field"])
				}
				assertNoRowPersisted(t, store)
			})
		}
	}
}

// orderingPlan names a foreign uploadFile ahead of a gitClone source with a
// branch ref, and carries more setup commands than the runtime's cap.
func orderingPlan(foreignRef string) json.RawMessage {
	return json.RawMessage(`{"schemaVersion":1,"sources":[` +
		`{"type":"uploadFile","path":"a.txt","uploadRef":"` + foreignRef + `"},` +
		`{"type":"gitClone","url":"https://example.com/r.git","ref":"main"}],` +
		`"setupCommands":[{"cmd":"echo a"},{"cmd":"echo b"}]}`)
}

// cappedRuntimes registers the runtime the binding cases use with a
// setupCommandPolicy cap of one command.
func cappedRuntimes(t *testing.T) runtimestore.Store {
	t.Helper()
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "claude-code",
		Type: runtimestore.TypeAgent,
		SetupCommandPolicy: &runtimestore.SetupCommandPolicy{
			Mode:        runtimestore.SetupCommandModeAllowlist,
			MaxCommands: 1,
		},
	}); err != nil {
		t.Fatalf("register runtime: %v", err)
	}
	return runtimes
}

// spec: 14.1 (WorkspacePlan Schema Versioning), 14 (Workspace Plan Schema)
//
// The uploadRef scope check runs ahead of the gitClone checks and the
// setupCommandPolicy check at every bind point, so a foreign ref fails
// before any ls-remote runs. The zero Resolve count discriminates: a
// resolver call means a gitClone pin ran for a plan the scope check
// rejects.
func TestUploadRefScopeRunsBeforeGitClonePin_spec_14_1(t *testing.T) {
	foreign := sessionScopedRef(t, "sess_other")
	t.Run("POST /v1/sessions", func(t *testing.T) {
		resolver := &countingRefResolver{}
		srv, store := bindTestServer(t, resolver, cappedRuntimes(t))
		rr := postBindRequest(t, srv, "/v1/sessions", orderingPlan(foreign))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
		}
		if _, details := bindErrorEnvelope(t, rr); details["reason"] != "upload_ref_foreign_session" {
			t.Errorf("details.reason = %v, want upload_ref_foreign_session", details["reason"])
		}
		if got := resolver.calls.Load(); got != 0 {
			t.Errorf("Resolve calls = %d, want 0", got)
		}
		assertNoRowPersisted(t, store)
	})
	t.Run("finalize", func(t *testing.T) {
		resolver := &countingRefResolver{}
		srv, store := bindTestServer(t, resolver, cappedRuntimes(t))
		seedCreated(t, store, "sess_order", "")
		body, err := json.Marshal(finalizeRequest{WorkspacePlan: orderingPlan(foreign)})
		if err != nil {
			t.Fatalf("marshal finalize body: %v", err)
		}
		rr := postFinalize(t, srv, "sess_order", string(body))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
		}
		if _, details := bindErrorEnvelope(t, rr); details["reason"] != "upload_ref_foreign_session" {
			t.Errorf("details.reason = %v, want upload_ref_foreign_session", details["reason"])
		}
		if got := resolver.calls.Load(); got != 0 {
			t.Errorf("Resolve calls = %d, want 0", got)
		}
		row, err := store.Get(context.Background(), "default", "sess_order")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if len(row.WorkspacePlan) != 0 {
			t.Errorf("stored plan = %s, want none after the rejected finalize", row.WorkspacePlan)
		}
	})
}

// spec: 14 (Workspace Plan Schema), 14.1 (WorkspacePlan Schema Versioning), 12.5
//
// validatePlanUploadRefs admits only an upload source stored for the
// binding session of the request's tenant. An empty bindSessionID, the
// create-time case, rejects every upload source whatever its session
// segment holds. Sources that carry no uploadRef are not checked.
func TestValidatePlanUploadRefs_spec_14(t *testing.T) {
	ref := func(tenant, sessionID string) string {
		return blobstore.URI{
			TenantID: tenant, ObjectType: blobstore.ObjectTypeUpload,
			SessionID: sessionID, PartID: "p1", TTL: time.Hour,
		}.String()
	}
	source := func(sourceType, uploadRef string) workspaceplan.Source {
		if sourceType == workspaceplan.TypeUploadArchive {
			return workspaceplan.Source{Type: sourceType, Variant: workspaceplan.UploadArchive{UploadRef: uploadRef}}
		}
		return workspaceplan.Source{Type: sourceType, Variant: workspaceplan.UploadFile{UploadRef: uploadRef}}
	}
	inline := workspaceplan.Source{Type: workspaceplan.TypeInlineFile, Variant: workspaceplan.InlineFile{PathField: "a", Content: "b"}}
	cases := []struct {
		name          string
		bindSessionID string
		sources       []workspaceplan.Source
		wantOK        bool
		wantReason    string
		wantField     string
	}{
		{"own session uploadFile", "sess_a", []workspaceplan.Source{source(workspaceplan.TypeUploadFile, ref("acme", "sess_a"))}, true, "", ""},
		{"own session uploadArchive", "sess_a", []workspaceplan.Source{source(workspaceplan.TypeUploadArchive, ref("acme", "sess_a"))}, true, "", ""},
		{"no upload sources at creation", "", []workspaceplan.Source{inline}, true, "", ""},
		{"empty bind session rejects own-looking ref", "", []workspaceplan.Source{source(workspaceplan.TypeUploadFile, ref("acme", "sess_a"))}, false, "upload_ref_foreign_session", "sources[0].uploadRef"},
		{"foreign session", "sess_a", []workspaceplan.Source{source(workspaceplan.TypeUploadFile, ref("acme", "sess_b"))}, false, "upload_ref_foreign_session", "sources[0].uploadRef"},
		{"foreign tenant", "sess_a", []workspaceplan.Source{source(workspaceplan.TypeUploadArchive, ref("globex", "sess_a"))}, false, "upload_ref_foreign_session", "sources[0].uploadRef"},
		{"undecodable ref at creation", "", []workspaceplan.Source{source(workspaceplan.TypeUploadFile, "upload_abc123")}, false, "invalid_upload_ref", "sources[0].uploadRef"},
		{"undecodable ref at finalize", "sess_a", []workspaceplan.Source{source(workspaceplan.TypeUploadArchive, "not-a-ref")}, false, "invalid_upload_ref", "sources[0].uploadRef"},
		{"second source rejected", "sess_a", []workspaceplan.Source{inline, source(workspaceplan.TypeUploadFile, ref("acme", "sess_b"))}, false, "upload_ref_foreign_session", "sources[1].uploadRef"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := finalizeTestServer(t)
			rr := httptest.NewRecorder()
			ok := srv.validatePlanUploadRefs(rr, "acme", tc.bindSessionID, workspaceplan.Plan{Sources: tc.sources})
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v; body=%s", ok, tc.wantOK, rr.Body.String())
			}
			if tc.wantOK {
				if rr.Body.Len() != 0 {
					t.Errorf("admitted plan wrote a response: %s", rr.Body.String())
				}
				return
			}
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rr.Code)
			}
			code, details := bindErrorEnvelope(t, rr)
			if code != "VALIDATION_ERROR" {
				t.Errorf("code = %q, want VALIDATION_ERROR", code)
			}
			if details["reason"] != tc.wantReason || details["field"] != tc.wantField {
				t.Errorf("details = %v, want reason %q field %q", details, tc.wantReason, tc.wantField)
			}
		})
	}
}
