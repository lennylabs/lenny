// SPDX-License-Identifier: MIT

//go:build contract

package rest_openai_responses_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/responsesstub"
)

// Test-only headers that name the authenticated principal a request
// carries. A request without testSubjectHeader carries no principal.
const (
	testSubjectHeader = "X-Test-Subject"
	testRoleHeader    = "X-Test-Role"
)

// withHeaderPrincipal stands in for the gateway's auth middleware: it gives a
// request the tenant-acme principal its test headers name, and leaves a
// request without testSubjectHeader with no principal.
func withHeaderPrincipal(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subject := r.Header.Get(testSubjectHeader); subject != "" {
			p := authmw.Principal{Subject: subject, TenantID: "acme", Roles: []auth.Role{auth.Role(r.Header.Get(testRoleHeader))}}
			r = r.WithContext(authmw.WithPrincipal(r.Context(), p))
		}
		h.ServeHTTP(w, r)
	})
}

// caller names the principal a request is sent as; the zero caller sends
// no principal.
type caller struct{ subject, role string }

var (
	aliceUser  = caller{"alice", string(auth.RoleUser)}
	bobUser    = caller{"bob", string(auth.RoleUser)}
	carolAdmin = caller{"carol", string(auth.RoleTenantAdmin)}
)

// send issues method on url as c in tenant acme and returns the status and
// body.
func send(t *testing.T, c caller, method, url, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.subject != "" {
		req.Header.Set(testSubjectHeader, c.subject)
		req.Header.Set(testRoleHeader, c.role)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := readAll(resp)
	return resp, raw
}

// spec: §10.2 (Session ownership) rules 1 and 3; §15 (built-in adapter
// single-shot compute model)
// diagnosis: a row owned by the `user` request field rather than the
// principal's subject, or a no-principal request that is not the native 401
// with error.code UNAUTHORIZED and no row written, means a built-in adapter
// in pkg/gateway/environment/translator (openai_chat.go, open_responses.go)
// copies the request's `user` into the session spec again, or the default
// binder in singleshot.go stopped refusing a request with no subject.
func TestOpenAIAdaptersBindOwnerFromPrincipal_spec_10_2(t *testing.T) {
	adapters := []struct {
		name, path, body, id string
		build                func(sessionstore.Store, string) http.Handler
	}{
		{
			name: "chat completions", path: "/v1/chat/completions", id: "chat_owner_1",
			body: `{"model":"echo","user":"bob","messages":[{"role":"user","content":"hi"}]}`,
			build: func(store sessionstore.Store, id string) http.Handler {
				return translator.NewOpenAIChatHandler(store, executor.NewEchoExecutor(), translator.OpenAIChatOptions{
					DevTenantHeader: true, IDFunc: func() string { return id },
				}).Handler()
			},
		},
		{
			name: "responses", path: "/v1/responses", id: "resp_owner_1",
			body: `{"model":"echo","user":"bob","input":"hi"}`,
			build: func(store sessionstore.Store, id string) http.Handler {
				exec := executor.NewEchoExecutor()
				return translator.NewOpenResponsesHandler(store, exec, translator.OpenResponsesOptions{
					HolderForwarder: responsesstub.LocalForwarder{},
					Terminal:        responsesstub.ReleasingTerminal{Exec: exec, Store: store},
					SessionAccess:   sessionserver.New(store, sessionserver.Options{}),
					DevTenantHeader: true,
					IDFunc:          func() string { return id },
				}).Handler()
			},
		},
	}
	for _, a := range adapters {
		t.Run(a.name, func(t *testing.T) {
			store := memstore.New()
			srv := httptest.NewServer(withHeaderPrincipal(a.build(store, a.id)))
			t.Cleanup(srv.Close)

			resp, raw := send(t, caller{}, http.MethodPost, srv.URL+a.path, a.body)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("no-principal POST: %d, want 401; body=%s", resp.StatusCode, raw)
			}
			var env struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(raw, &env); err != nil || env.Error.Code != "UNAUTHORIZED" {
				t.Errorf("no-principal body = %s, want the native envelope with code UNAUTHORIZED", raw)
			}
			if _, err := store.Get(context.Background(), "acme", a.id); !errors.Is(err, sessionstore.ErrNotFound) {
				t.Fatalf("no-principal POST wrote a row: err=%v", err)
			}

			resp, raw = send(t, aliceUser, http.MethodPost, srv.URL+a.path, a.body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("alice POST: %d, body=%s", resp.StatusCode, raw)
			}
			row, err := store.Get(context.Background(), "acme", a.id)
			if err != nil {
				t.Fatalf("read %s: %v", a.id, err)
			}
			if row.UserID != aliceUser.subject {
				t.Errorf("row owner = %q, want the principal %q (the request sent user=bob)", row.UserID, aliceUser.subject)
			}
		})
	}
}

// writeCountingStore counts the Create and Update calls that reach the
// session store.
type writeCountingStore struct {
	sessionstore.Store
	mu     sync.Mutex
	writes int
}

func (s *writeCountingStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

func (s *writeCountingStore) Create(ctx context.Context, row sessionstore.Session) error {
	s.mu.Lock()
	s.writes++
	s.mu.Unlock()
	return s.Store.Create(ctx, row)
}

func (s *writeCountingStore) Update(ctx context.Context, tenantID, id string, m func(*sessionstore.Session) error) (sessionstore.Session, error) {
	s.mu.Lock()
	s.writes++
	s.mu.Unlock()
	return s.Store.Update(ctx, tenantID, id, m)
}

// GetPrimary reads the wrapped store's primary.
func (s *writeCountingStore) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	return sessionstore.ReadPrimary(ctx, s.Store, 0, tenantID, id)
}

// recordingExecutor is the echo executor that records the session of every
// Send.
type recordingExecutor struct {
	executor.Executor
	mu    sync.Mutex
	sends []string
}

func (e *recordingExecutor) Send(ctx context.Context, sessionID string, msgs []executor.Message) (executor.Response, error) {
	e.mu.Lock()
	e.sends = append(e.sends, sessionID)
	e.mu.Unlock()
	return e.Executor.Send(ctx, sessionID, msgs)
}

func (e *recordingExecutor) sent() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.sends...)
}

// ownershipServer is the Open Responses translator with a holding binder, a
// write-counting store, and a recording executor, behind
// withHeaderPrincipal. When wireAccess is true its SessionAccess is the
// session server's ownership predicate over the same store; otherwise it is
// nil.
type ownershipServer struct {
	url   string
	store *writeCountingStore
	exec  *recordingExecutor
}

func newOwnershipServer(t *testing.T, wireAccess bool) *ownershipServer {
	t.Helper()
	inner := memstore.New()
	var access translator.SessionAccess
	if wireAccess {
		access = sessionserver.New(inner, sessionserver.Options{})
	}
	s := &ownershipServer{
		store: &writeCountingStore{Store: inner},
		exec:  &recordingExecutor{Executor: executor.NewEchoExecutor()},
	}
	h := translator.NewOpenResponsesHandler(s.store, s.exec, translator.OpenResponsesOptions{
		Terminal:         responsesstub.ReleasingTerminal{Exec: s.exec, Store: s.store},
		DevTenantHeader:  true,
		SingleShotBinder: holdingBinder{store: s.store},
		Transcripts:      transcriptstore.NewMemory(),
		HolderForwarder:  responsesstub.LocalForwarder{},
		SessionAccess:    access,
	})
	srv := httptest.NewServer(withHeaderPrincipal(h.Handler()))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// root posts a root turn as c and returns its response id.
func (s *ownershipServer) root(t *testing.T, c caller) string {
	t.Helper()
	resp, raw := send(t, c, http.MethodPost, s.url+"/v1/responses", `{"model":"echo","input":"one"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("root POST as %s: %d, body=%s", c.subject, resp.StatusCode, raw)
	}
	var out translator.OpenResponsesResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out.ID
}

func (s *ownershipServer) get(t *testing.T, c caller, id string) (*http.Response, []byte) {
	t.Helper()
	return send(t, c, http.MethodGet, s.url+"/v1/responses/"+id, "")
}

func (s *ownershipServer) continueAs(t *testing.T, c caller, prev, user string) (*http.Response, []byte) {
	t.Helper()
	return send(t, c, http.MethodPost, s.url+"/v1/responses",
		`{"model":"echo","input":"two","user":"`+user+`","previous_response_id":"`+prev+`"}`)
}

// unknownResponseID names a response of a session that does not exist.
const unknownResponseID = "0190a1b2-c3d4-8e5f-9a0b-1c2d3e4f5a6b.1"

// assertSameNative404 fails unless resp is the native not_found_error 404
// and its body is byte-identical to the unknown id's answer.
func assertSameNative404(t *testing.T, what string, resp *http.Response, raw []byte, unknown []byte) {
	t.Helper()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("%s: %d, want 404; body=%s", what, resp.StatusCode, raw)
		return
	}
	if env := decodeOpenAIError(t, resp, raw); env.Error.Type != "not_found_error" {
		t.Errorf("%s error.type = %q, want not_found_error", what, env.Error.Type)
	}
	if string(raw) != string(unknown) {
		t.Errorf("%s body %s differs from the unknown id's %s", what, raw, unknown)
	}
}

// spec: §10.2 (Session ownership) rules 4 and 7; §15 (Open Responses
// conversation sessions)
// diagnosis: alice/user reading or continuing bob's response with anything
// other than the unknown id's native 404, or the continuation binding a
// session, writing the store, or dispatching, means the resolve step in
// pkg/gateway/environment/translator/continuity.go (resolve, readable) no
// longer asks SessionAccess, or the gateway wiring of
// OpenResponsesOptions.SessionAccess in cmd/lenny-gateway/credentialsurface.go
// was dropped.
func TestOpenResponsesOtherUsersResponseIsNative404_spec_10_2(t *testing.T) {
	s := newOwnershipServer(t, true)
	head := s.root(t, bobUser)
	if resp, raw := s.get(t, bobUser, head); resp.StatusCode != http.StatusOK {
		t.Fatalf("owner GET: %d, body=%s", resp.StatusCode, raw)
	}
	writes, sends := s.store.count(), len(s.exec.sent())

	_, unknownGet := s.get(t, aliceUser, unknownResponseID)
	resp, raw := s.get(t, aliceUser, head)
	assertSameNative404(t, "alice GET of bob's response", resp, raw, unknownGet)

	_, unknownCont := s.continueAs(t, aliceUser, unknownResponseID, "bob")
	resp, raw = s.continueAs(t, aliceUser, head, "bob")
	assertSameNative404(t, "alice continuation of bob's response", resp, raw, unknownCont)

	if got := s.store.count(); got != writes {
		t.Errorf("store writes = %d after the refused requests, want %d", got, writes)
	}
	if got := s.exec.sent(); len(got) != sends {
		t.Errorf("executor sends = %v after the refused requests, want %d", got, sends)
	}
}

// spec: §10.2 (Session ownership) rules 4 and 5; §15 (Open Responses
// conversation sessions)
// diagnosis: a tenant-admin continuation of alice's held response that runs
// on alice's session, or creates a session it does not own, means warm
// condition 4 in pkg/gateway/environment/translator/open_responses.go
// (warmEligible, reserve) compares the `user` request field again instead of
// the principal's subject.
func TestOpenResponsesTenantAdminContinuationGoesCold_spec_10_2(t *testing.T) {
	s := newOwnershipServer(t, true)
	head := s.root(t, aliceUser)
	aliceSession, _ := splitResponseID(t, head)

	if resp, raw := s.get(t, carolAdmin, head); resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant-admin GET of alice's response: %d, want 200; body=%s", resp.StatusCode, raw)
	}
	resp, raw := s.continueAs(t, carolAdmin, head, aliceUser.subject)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant-admin continuation: %d, body=%s", resp.StatusCode, raw)
	}
	var cont translator.OpenResponsesResponse
	if err := json.Unmarshal(raw, &cont); err != nil {
		t.Fatal(err)
	}
	coldSession, n := splitResponseID(t, cont.ID)
	if coldSession == aliceSession || n != "1" || cont.PreviousResponseID != head {
		t.Fatalf("continuation id %q previous %q, want ordinal 1 of a new session continuing %q", cont.ID, cont.PreviousResponseID, head)
	}
	row, err := s.store.Get(context.Background(), "acme", coldSession)
	if err != nil {
		t.Fatal(err)
	}
	if row.UserID != carolAdmin.subject {
		t.Errorf("cold session owner = %q, want the tenant-admin %q", row.UserID, carolAdmin.subject)
	}
	if sent := s.exec.sent(); len(sent) != 2 || sent[1] != coldSession {
		t.Errorf("executor sends = %v, want alice's root turn then one dispatch to %s", sent, coldSession)
	}
	held, err := s.store.Get(context.Background(), "acme", aliceSession)
	if err != nil {
		t.Fatal(err)
	}
	if len(held.Responses) != 1 {
		t.Errorf("alice's held session has %d responses, want 1", len(held.Responses))
	}
}

// spec: §10.2 (Session ownership) rule 7; §15 (Open Responses conversation
// sessions)
// diagnosis: an owner's GET or continuation answered with anything other
// than the native 404 on a handler wired with no SessionAccess means
// continuity.readable in pkg/gateway/environment/translator/continuity.go
// stopped failing closed on a nil predicate.
func TestOpenResponsesNilSessionAccessIsNative404_spec_10_2(t *testing.T) {
	s := newOwnershipServer(t, false)
	head := s.root(t, aliceUser)
	_, unknownGet := s.get(t, aliceUser, unknownResponseID)
	resp, raw := s.get(t, aliceUser, head)
	assertSameNative404(t, "owner GET with no SessionAccess", resp, raw, unknownGet)

	sends := len(s.exec.sent())
	_, unknownCont := s.continueAs(t, aliceUser, unknownResponseID, "alice")
	resp, raw = s.continueAs(t, aliceUser, head, "alice")
	assertSameNative404(t, "owner continuation with no SessionAccess", resp, raw, unknownCont)
	if got := s.exec.sent(); len(got) != sends {
		t.Errorf("executor sends = %v, want %d (no dispatch)", got, sends)
	}
}
