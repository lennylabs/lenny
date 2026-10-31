// SPDX-License-Identifier: MIT

package translator_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// TestBuiltInAdaptersBindOwnerFromPrincipal_spec_10_2 pins the default
// binder behind both built-in adapters: a request with no authenticated
// principal subject answers the native 401 UNAUTHORIZED envelope and writes
// no session row, and a request under a principal creates a row owned by
// that principal's subject whatever the request's `user` field names. Before
// the owner was bound from the principal, the first request created an
// ownerless row and the second a row owned by the `user` field.
//
// spec: §10.2 (Session ownership) rules 1 and 3; §15 (built-in adapter
// single-shot compute model).
func TestBuiltInAdaptersBindOwnerFromPrincipal_spec_10_2(t *testing.T) {
	adapters := []struct {
		name  string
		path  string
		id    string
		build func(t *testing.T) (http.Handler, sessionstore.Store)
		body  map[string]any
	}{
		{
			name: "chat completions", path: "/v1/chat/completions", id: "sess_oa_1",
			build: func(t *testing.T) (http.Handler, sessionstore.Store) {
				h, s := newOpenAIHandler(t)
				return h.Handler(), s
			},
			body: map[string]any{"model": "echo", "user": "bob", "messages": []map[string]string{{"role": "user", "content": "hi"}}},
		},
		{
			name: "responses", path: "/v1/responses", id: "resp_1",
			build: func(t *testing.T) (http.Handler, sessionstore.Store) {
				h, s := newResponsesHandler(t)
				return h.Handler(), s
			},
			body: map[string]any{"model": "echo", "user": "bob", "input": "hi"},
		},
	}
	principals := []struct {
		name      string
		ctx       func(context.Context) context.Context
		wantCode  int
		wantOwner string
	}{
		{name: "no principal", ctx: func(c context.Context) context.Context { return c }, wantCode: http.StatusUnauthorized},
		{name: "empty subject", ctx: func(c context.Context) context.Context { return ownerprincipal.Context(c, "") }, wantCode: http.StatusUnauthorized},
		{name: "principal subject", ctx: func(c context.Context) context.Context { return ownerprincipal.Context(c, ownerprincipal.Alice) }, wantCode: http.StatusOK, wantOwner: ownerprincipal.Alice},
	}
	for _, a := range adapters {
		for _, p := range principals {
			t.Run(a.name+"/"+p.name, func(t *testing.T) {
				h, store := a.build(t)
				buf, _ := json.Marshal(a.body)
				req := httptest.NewRequest(http.MethodPost, a.path, bytes.NewReader(buf))
				req = req.WithContext(p.ctx(req.Context()))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Lenny-Tenant-ID", "acme")
				rr := httptest.NewRecorder()
				h.ServeHTTP(rr, req)
				if rr.Code != p.wantCode {
					t.Fatalf("status = %d, want %d; body=%s", rr.Code, p.wantCode, rr.Body.String())
				}
				row, err := store.Get(context.Background(), "acme", a.id)
				if p.wantCode != http.StatusOK {
					var env struct {
						Error struct {
							Code string `json:"code"`
						} `json:"error"`
					}
					_ = json.Unmarshal(rr.Body.Bytes(), &env)
					if env.Error.Code != "UNAUTHORIZED" {
						t.Errorf("error code = %q, want UNAUTHORIZED", env.Error.Code)
					}
					if !errors.Is(err, sessionstore.ErrNotFound) {
						t.Errorf("store.Get after refusal: row %+v, err %v; want ErrNotFound", row, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("store.Get: %v", err)
				}
				if row.UserID != p.wantOwner {
					t.Errorf("row owner = %q, want the principal subject %q", row.UserID, p.wantOwner)
				}
			})
		}
	}
}

// userPrincipal returns a context carrying a principal of tenant acme with
// the built-in user role and the given subject.
func userPrincipal(subject string) context.Context {
	return authmw.WithPrincipal(context.Background(), authmw.Principal{
		Subject: subject, TenantID: "acme", Roles: []auth.Role{auth.RoleUser},
	})
}

// getAs sends GET /v1/responses/{id} on ctx as tenant acme.
func (f *convFixture) getAs(ctx context.Context, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/responses/"+id, nil).WithContext(ctx)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	f.h.Handler().ServeHTTP(rr, req)
	return rr
}

// sideEffects is the count of every write and dispatch a continuation can
// cause on a fixture: binds, store Updates, executor Sends, and forwards to
// the held session's coordinator.
type sideEffects struct{ binds, updates, sends, forwards int }

func (f *convFixture) sideEffects() sideEffects {
	f.exec.mu.Lock()
	sends := len(f.exec.calls)
	f.exec.mu.Unlock()
	return sideEffects{f.binder.count(), f.store.updateCount(), sends, f.forwarder.count()}
}

// assertNative404 fails unless rr is the native OpenAI 404 not_found_error
// envelope and its body equals the body for an id that names no session, so
// a refused id is indistinguishable from an unknown one.
func assertNative404(t *testing.T, rr, unknown *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
	var env translator.OpenAIError
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("404 body is not the OpenAI error envelope: %v; body=%s", err, rr.Body.String())
	}
	if env.Error.Type != "not_found_error" {
		t.Errorf("error.type = %q, want not_found_error", env.Error.Type)
	}
	if unknown.Code != http.StatusNotFound || rr.Body.String() != unknown.Body.String() {
		t.Errorf("refusal body %q differs from the unknown-id answer (%d) %q",
			rr.Body.String(), unknown.Code, unknown.Body.String())
	}
}

// TestOpenResponsesOtherUsersResponseIsNative404_spec_10_2 pins the resolve
// step's ownership check on the wire: alice, holding only the user role,
// asks GET /v1/responses/{id} for bob's held response and continues it
// through previous_response_id, sending `user=bob`. Both answer the native
// 404 that an unknown id answers, and the continuation binds no session,
// writes nothing to the store, dispatches nothing, and forwards nothing to
// bob's held session. Before the resolve step checked ownership, alice read
// bob's response and continued his conversation cold with his transcript.
//
// spec: §10.2 (Session ownership) rules 4 and 7; §15 (Open Responses
// conversation sessions)
func TestOpenResponsesOtherUsersResponseIsNative404_spec_10_2(t *testing.T) {
	f := newConvFixture(t, convOptions{hold: true})
	bob, alice := userPrincipal("bob"), userPrincipal("alice")
	rr := f.post(t, bob, map[string]any{"model": "echo", "input": "bob's secret"})
	if rr.Code != http.StatusOK {
		t.Fatalf("bob's root turn: status %d: %s", rr.Code, rr.Body.String())
	}
	const head = "sess_1.1"
	if got := f.getAs(bob, head); got.Code != http.StatusOK {
		t.Fatalf("owner GET %s = %d, want 200; body=%s", head, got.Code, got.Body.String())
	}
	before := f.sideEffects()

	assertNative404(t, f.getAs(alice, head), f.getAs(alice, "sess_9.1"))

	cont := func(prev string) *httptest.ResponseRecorder {
		return f.post(t, alice, map[string]any{
			"model": "echo", "input": "two", "user": "bob", "previous_response_id": prev,
		})
	}
	unknown := cont("sess_9.1")
	assertNative404(t, cont(head), unknown)

	if after := f.sideEffects(); after != before {
		t.Errorf("refused requests caused side effects: before %+v, after %+v", before, after)
	}
	if _, err := f.inner.Get(context.Background(), "acme", "sess_2"); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("store.Get(sess_2) = %v, want ErrNotFound (no session written)", err)
	}
	if row := f.row(t, "sess_1"); len(row.Responses) != 1 || row.UserID != "bob" {
		t.Errorf("bob's session changed: owner %q, %d responses", row.UserID, len(row.Responses))
	}
}

// TestOpenResponsesTenantAdminContinuationGoesCold_spec_10_2 pins the warm
// owner condition for a principal that may read another user's session: a
// tenant-admin reads alice's held response, and its continuation of that
// response, sent with `user=alice`, goes cold on a new session the
// tenant-admin owns. The continuation carries alice's rehydrated
// conversation, alice's held pod receives no dispatch and no forward, and
// alice's session records no new response. Before the owner condition read
// the principal, the `user` field matched and the tenant-admin's turn ran
// warm on alice's held pod.
//
// spec: §10.2 (Session ownership) rules 4 and 5; §15 (Open Responses
// conversation sessions)
func TestOpenResponsesTenantAdminContinuationGoesCold_spec_10_2(t *testing.T) {
	f := newConvFixture(t, convOptions{hold: true})
	head := f.mustTurn(t, "", "one")
	admin := authmw.WithPrincipal(context.Background(), tenantAdminBob)

	if got := f.getAs(admin, head); got.Code != http.StatusOK {
		t.Fatalf("tenant-admin GET %s = %d, want 200; body=%s", head, got.Code, got.Body.String())
	}
	rr := f.post(t, admin, map[string]any{
		"model": "echo", "input": "two", "user": convOwner, "previous_response_id": head,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("tenant-admin continuation: status %d: %s", rr.Code, rr.Body.String())
	}
	if binds := f.binder.count(); binds != 2 {
		t.Fatalf("binds = %d, want 2 (cold)", binds)
	}
	cold := f.row(t, "sess_2")
	if cold.UserID != tenantAdminBob.Subject || cold.ContinuationParentID != head {
		t.Errorf("cold session owner %q parent %q, want %q and %q",
			cold.UserID, cold.ContinuationParentID, tenantAdminBob.Subject, head)
	}
	if n := f.forwarder.count(); n != 0 {
		t.Errorf("forwards to alice's held session = %d, want 0", n)
	}
	f.exec.mu.Lock()
	calls := append([]sendCall(nil), f.exec.calls...)
	f.exec.mu.Unlock()
	if len(calls) != 2 || calls[1].sessionID != "sess_2" {
		t.Fatalf("executor calls = %+v, want the root turn and one dispatch to sess_2", calls)
	}
	if held := f.row(t, "sess_1"); len(held.Responses) != 1 || held.UserID != convOwner {
		t.Errorf("alice's held session changed: owner %q, %d responses", held.UserID, len(held.Responses))
	}
}

// TestOpenResponsesNilSessionAccessIsNative404_spec_10_2 pins the fail-closed
// default: a handler wired with no SessionAccess answers the native 404 to
// the owner's own GET /v1/responses/{id} and previous_response_id, and the
// continuation binds no session.
//
// spec: §10.2 (Session ownership) rule 7; §15 (Open Responses conversation
// sessions)
func TestOpenResponsesNilSessionAccessIsNative404_spec_10_2(t *testing.T) {
	f := newConvFixture(t, convOptions{noSessionAccess: true})
	head := f.mustTurn(t, "", "one")
	owner := userPrincipal(convOwner)
	assertNative404(t, f.getAs(owner, head), f.getAs(owner, "sess_9.1"))

	before := f.sideEffects()
	cont := func(prev string) *httptest.ResponseRecorder {
		return f.post(t, owner, map[string]any{"model": "echo", "input": "two", "previous_response_id": prev})
	}
	assertNative404(t, cont(head), cont("sess_9.1"))
	if after := f.sideEffects(); after != before {
		t.Errorf("refused continuation caused side effects: before %+v, after %+v", before, after)
	}
}
