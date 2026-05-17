// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// spec: §14 gitClone ref resolution — the gateway pins each gitClone
// ref to an immutable commit SHA at session creation.

const pinSHA = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"

// pinStubResolver is a workspaceplan.RefResolver for the pinning tests.
type pinStubResolver struct {
	shas map[string]string
	err  error
}

func (r *pinStubResolver) Resolve(_ context.Context, src workspaceplan.GitClone) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return r.shas[src.Ref], nil
}

func gitClonePlanJSON(ref string) json.RawMessage {
	return json.RawMessage(`{"schemaVersion":1,"sources":[` +
		`{"type":"gitClone","url":"https://example.com/r.git","ref":"` + ref + `"}]}`)
}

// storedPlanSource GETs a session and returns its workspacePlan's first
// source object.
func storedPlanSource(t *testing.T, h http.Handler, id string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+id, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/sessions/%s: status %d", id, rr.Code)
	}
	var resp struct {
		WorkspacePlan json.RawMessage `json:"workspacePlan"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	var plan map[string]any
	if err := json.Unmarshal(resp.WorkspacePlan, &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	srcs, ok := plan["sources"].([]any)
	if !ok || len(srcs) == 0 {
		t.Fatalf("stored plan has no sources: %v", plan)
	}
	src, ok := srcs[0].(map[string]any)
	if !ok {
		t.Fatalf("source 0 is not an object: %v", srcs[0])
	}
	return src
}

func TestCreatePinsGitCloneShaRef(t *testing.T) {
	// A ref already in commit-SHA form is pinned directly — the resolver
	// is never consulted.
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:      func() string { return "sess_pin_sha" },
		RefResolver: &pinStubResolver{},
	})
	h := srv.Handler()
	rr := createRequest(t, h, sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitClonePlanJSON(pinSHA),
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	if got := storedPlanSource(t, h, "sess_pin_sha")["resolvedCommitSha"]; got != pinSHA {
		t.Errorf("stored resolvedCommitSha = %v, want %q", got, pinSHA)
	}
}

func TestCreatePinsGitCloneBranchRef(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:      func() string { return "sess_pin_branch" },
		RefResolver: &pinStubResolver{shas: map[string]string{"main": pinSHA}},
	})
	h := srv.Handler()
	rr := createRequest(t, h, sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitClonePlanJSON("main"),
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	if got := storedPlanSource(t, h, "sess_pin_branch")["resolvedCommitSha"]; got != pinSHA {
		t.Errorf("stored resolvedCommitSha = %v, want %q", got, pinSHA)
	}
}

func TestCreateGitCloneTransientResolveError(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc: func() string { return "sess_transient" },
		RefResolver: &pinStubResolver{err: &workspaceplan.ResolveError{
			Reason: workspaceplan.ResolveNetworkError, Err: errors.New("connection reset"),
		}},
	})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitClonePlanJSON("main"),
	})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 for a transient resolve failure; body %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, "GIT_CLONE_REF_RESOLVE_TRANSIENT") {
		t.Errorf("body = %s, want GIT_CLONE_REF_RESOLVE_TRANSIENT", body)
	}
}

func TestCreateGitCloneUnresolvableError(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc: func() string { return "sess_unresolvable" },
		RefResolver: &pinStubResolver{err: &workspaceplan.ResolveError{
			Reason: workspaceplan.ResolveRefNotFound, Err: errors.New("ref not found"),
		}},
	})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitClonePlanJSON("no-such-ref"),
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422 for an unresolvable ref; body %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, "GIT_CLONE_REF_UNRESOLVABLE") {
		t.Errorf("body = %s, want GIT_CLONE_REF_UNRESOLVABLE", body)
	}
}

func TestCreateGitCloneNoResolverStoresVerbatim(t *testing.T) {
	// With no RefResolver wired the gateway stores the submitted plan
	// unchanged — a non-SHA gitClone ref is not pinned and not rejected.
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc: func() string { return "sess_noresolver" },
	})
	h := srv.Handler()
	rr := createRequest(t, h, sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitClonePlanJSON("main"),
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	if _, pinned := storedPlanSource(t, h, "sess_noresolver")["resolvedCommitSha"]; pinned {
		t.Error("a session created without a RefResolver must not carry a pinned resolvedCommitSha")
	}
}
