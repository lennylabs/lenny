// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: 14.1 (WorkspacePlan Schema Versioning), 14 (Workspace Plan Schema)
// diagnosis: an uploadRef returned by POST /v1/sessions/{id}/upload belongs
// to that session alone. A POST /v1/sessions or POST /v1/sessions/start plan
// naming it must fail with 400 VALIDATION_ERROR (upload_ref_foreign_session)
// on the fully wired gateway, issue no adapter RPC, and leave the owning
// session `created`. A 2xx means a create-time plan bound another session's
// staged blob, which lets one session of a tenant materialize content staged
// for another.
func TestCreateTimePlanCannotNameAnotherSessionsUpload_spec_14_1(t *testing.T) {
	f := newAdmissionFixture(t)
	ref := f.upload(t)
	plan := json.RawMessage(`{"schemaVersion":1,"sources":[{"type":"uploadFile","path":"CLAUDE.md","uploadRef":"` + ref + `"}]}`)

	createBody, _ := json.Marshal(sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: plan,
	})
	startBody, _ := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "echo", WorkspacePlan: plan})
	for _, tc := range []struct {
		path string
		body []byte
	}{
		{"/v1/sessions", createBody},
		{"/v1/sessions/start", startBody},
	} {
		before := f.rpcs.total()
		rr := f.do(http.MethodPost, tc.path, tc.body, map[string]string{"Content-Type": "application/json"})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400; body=%s", tc.path, rr.Code, rr.Body.String())
		}
		var env struct {
			Error struct {
				Code    string         `json:"code"`
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
			t.Fatalf("%s: decode error envelope: %v", tc.path, err)
		}
		if env.Error.Code != "VALIDATION_ERROR" || env.Error.Details["reason"] != "upload_ref_foreign_session" {
			t.Errorf("%s: error = %s %v, want VALIDATION_ERROR upload_ref_foreign_session", tc.path, env.Error.Code, env.Error.Details)
		}
		if after := f.rpcs.total(); after != before {
			t.Errorf("%s: adapter RPCs rose from %d to %d for a rejected plan", tc.path, before, after)
		}
	}
	if got := f.storedState(t); got != session.StateCreated {
		t.Errorf("owning session state = %q, want created", got)
	}
}
