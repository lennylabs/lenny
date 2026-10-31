// SPDX-License-Identifier: MIT

//go:build security

package tier9_security_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// spec: §10.2 (Session ownership) rule 3, §15 (Open Responses conversation sessions), §15.1 (UNAUTHORIZED)
// diagnosis: a POST /v1/responses with no authenticated principal subject
// was admitted, or was refused with a code that depended on its body. A
// first turn reached the binder and created a session with no owner, or a
// continuation naming alice's completed response failed the ownership read
// and answered the native 404, which is the ownership refusal rather than
// the unauthenticated one. handleCreate in
// pkg/gateway/environment/translator/open_responses.go must call
// requireSubject before resolving previous_response_id, so every request
// with no subject answers 401 UNAUTHORIZED with no bind, store write,
// dispatch, or forward.
func TestOpenResponsesNoSubjectRefusedUnauthorized_spec_10_2(t *testing.T) {
	f, binder, fwd := newHeldORFixture(t)
	head := f.post(t, "acme", "alice's turn", "")
	held := orSessionOf(t, head)

	callers := []struct {
		name   string
		attach func(*http.Request) *http.Request
	}{
		{name: "no principal", attach: func(r *http.Request) *http.Request { return r }},
		{name: "empty subject", attach: func(r *http.Request) *http.Request {
			return r.WithContext(authmw.WithPrincipal(r.Context(), authmw.Principal{TenantID: "acme"}))
		}},
	}
	for _, c := range callers {
		for _, prev := range []string{"", head} {
			name := c.name + "/first turn"
			if prev != "" {
				name = c.name + "/continuation of alice's response"
			}
			t.Run(name, func(t *testing.T) {
				binds, sends := binder.binds(), f.exec.sends()
				body := map[string]any{"model": "echo", "input": "anonymous turn"}
				if prev != "" {
					body["previous_response_id"] = prev
				}
				buf, _ := json.Marshal(body)
				req := c.attach(httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(buf)))
				req.Header.Set("X-Lenny-Tenant-ID", "acme")
				rr := httptest.NewRecorder()
				f.handler.ServeHTTP(rr, req)

				var env struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				_ = json.Unmarshal(rr.Body.Bytes(), &env)
				if rr.Code != http.StatusUnauthorized || env.Error.Code != "UNAUTHORIZED" {
					t.Fatalf("answer = %d %s, want 401 UNAUTHORIZED", rr.Code, rr.Body.String())
				}
				if binder.binds() != binds || f.exec.sends() != sends || fwd.forwards.Load() != 0 {
					t.Errorf("binds +%d, sends +%d, forwards %d; want no bind, dispatch, or forward",
						binder.binds()-binds, f.exec.sends()-sends, fwd.forwards.Load())
				}
				row, err := f.sessions.Get(req.Context(), "acme", held)
				if err != nil {
					t.Fatal(err)
				}
				if len(row.Responses) != 1 {
					t.Errorf("alice's held session has %d responses, want 1", len(row.Responses))
				}
			})
		}
	}
}
