// SPDX-License-Identifier: MIT

package idempotencymw

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/idempotency"
)

// Test request headers that carry the principal scope in this package's
// unit tests. The gateway resolves the scope from the auth middleware's
// principal; these tests stand in for it with headers so each request
// names its own scope.
const (
	testSubjectHeader = "X-Test-Subject"
	testSessionHeader = "X-Test-Session"
)

// testOptions returns opts with a principal resolver that reads the tenant
// from X-Lenny-Tenant-ID, the subject from X-Test-Subject (alice when the
// header is absent), and the bound session from X-Test-Session.
func testOptions(opts Options) Options {
	opts.PrincipalFromRequest = func(r *http.Request) (string, string, string) {
		subject := r.Header.Get(testSubjectHeader)
		if subject == "" {
			subject = "alice"
		}
		return r.Header.Get("X-Lenny-Tenant-ID"), subject, r.Header.Get(testSessionHeader)
	}
	return opts
}

// aliceKey is the record key a request from alice in acme, bound to no
// session, files key under.
func aliceKey(key string) idempotency.Key {
	return idempotency.Key{TenantID: "acme", Subject: "alice", Value: key}
}

// scopedRequest sends one keyed POST /v1/sessions with body to h as subject
// in acme, bound to session when it is not empty, and returns the recorder.
func scopedRequest(t *testing.T, h http.Handler, subject, session, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(body))
	req.Header.Set(HeaderName, key)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set(testSubjectHeader, subject)
	if session != "" {
		req.Header.Set(testSessionHeader, session)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// countingCreate returns a handler that answers each call with 201 and a
// body naming the call's ordinal and the caller's subject, and the counter
// it increments.
func countingCreate() (http.Handler, *atomic.Int64) {
	var calls atomic.Int64
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"call": n, "owner": r.Header.Get(testSubjectHeader),
		})
	})
	return h, &calls
}

// TestTwoSubjectsOfOneTenantEachRunTheOperationAndReplayTheirOwnResponse
// sends alice's key and body from bob: bob's request runs the operation
// rather than receiving alice's stored response, and each user's retry
// replays that user's own response.
//
// spec: §11.5 (Idempotency)
func TestTwoSubjectsOfOneTenantEachRunTheOperationAndReplayTheirOwnResponse(t *testing.T) {
	inner, calls := countingCreate()
	h := Wrap(inner, NewMemoryStore(), testOptions(Options{}))
	const body = `{"runtime":"echo"}`

	alice1 := scopedRequest(t, h, "alice", "", "k1", body)
	bob1 := scopedRequest(t, h, "bob", "", "k1", body)
	if alice1.Code != http.StatusCreated || bob1.Code != http.StatusCreated {
		t.Fatalf("first requests: alice %d, bob %d, want 201 each", alice1.Code, bob1.Code)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("operation ran %d times for two users, want 2", got)
	}
	if alice1.Body.String() == bob1.Body.String() {
		t.Fatalf("bob received alice's stored response %s", bob1.Body.String())
	}

	alice2 := scopedRequest(t, h, "alice", "", "k1", body)
	bob2 := scopedRequest(t, h, "bob", "", "k1", body)
	if got := calls.Load(); got != 2 {
		t.Fatalf("retries ran the operation again: %d calls, want 2", got)
	}
	if alice2.Body.String() != alice1.Body.String() {
		t.Errorf("alice's retry = %s, want her stored response %s", alice2.Body.String(), alice1.Body.String())
	}
	if bob2.Body.String() != bob1.Body.String() {
		t.Errorf("bob's retry = %s, want his stored response %s", bob2.Body.String(), bob1.Body.String())
	}
}

// TestBearersOfOneSubjectBoundToDifferentSessionsDoNotShareARecord sends
// one key and body from alice bound to s1, bound to s2, and bound to no
// session: each runs the operation once, and each retry replays its own
// scope's response.
//
// spec: §11.5 (Idempotency)
func TestBearersOfOneSubjectBoundToDifferentSessionsDoNotShareARecord(t *testing.T) {
	inner, calls := countingCreate()
	h := Wrap(inner, NewMemoryStore(), testOptions(Options{}))
	const body = `{"task":"child"}`

	first := map[string]string{}
	for _, session := range []string{"s1", "s2", ""} {
		rec := scopedRequest(t, h, "alice", session, "child-1", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("session %q: status %d, want 201", session, rec.Code)
		}
		first[session] = rec.Body.String()
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("operation ran %d times for three scopes, want 3", got)
	}
	for _, session := range []string{"s1", "s2", ""} {
		rec := scopedRequest(t, h, "alice", session, "child-1", body)
		if rec.Body.String() != first[session] {
			t.Errorf("session %q retry = %s, want %s", session, rec.Body.String(), first[session])
		}
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("retries ran the operation again: %d calls, want 3", got)
	}
}

// TestAKeyedRequestWithNoSubjectIsRefusedBeforeAnyClaim checks that a
// request whose principal has a tenant and no subject receives
// 401 UNAUTHORIZED with reason principal_subject_required, and that the
// store sees no claim and the operation does not run.
//
// spec: §11.5 (Idempotency)
func TestAKeyedRequestWithNoSubjectIsRefusedBeforeAnyClaim(t *testing.T) {
	inner, calls := countingCreate()
	store := &countingStore{inner: NewMemoryStore()}
	h := Wrap(inner, store, Options{
		PrincipalFromRequest: func(*http.Request) (string, string, string) { return "acme", "", "s1" },
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
	req.Header.Set(HeaderName, "k1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	code, reason := envelopeCodeAndReason(t, rec.Body)
	if code != "UNAUTHORIZED" || reason != "principal_subject_required" {
		t.Errorf("envelope = %s/%s, want UNAUTHORIZED/principal_subject_required", code, reason)
	}
	if calls.Load() != 0 {
		t.Errorf("operation ran %d times, want 0", calls.Load())
	}
	if store.claims.Load() != 0 {
		t.Errorf("store saw %d claims, want 0", store.claims.Load())
	}
}

// TestARequestWithNoSubjectAndNoKeyPassesThrough checks that the subject
// refusal applies only to a keyed request: a request without an
// Idempotency-Key reaches the handler, which applies its own checks.
//
// spec: §11.5 (Idempotency)
func TestARequestWithNoSubjectAndNoKeyPassesThrough(t *testing.T) {
	inner, calls := countingCreate()
	h := Wrap(inner, NewMemoryStore(), Options{
		PrincipalFromRequest: func(*http.Request) (string, string, string) { return "acme", "", "" },
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`)))
	if rec.Code != http.StatusCreated || calls.Load() != 1 {
		t.Errorf("status %d with %d calls, want 201 with 1", rec.Code, calls.Load())
	}
}

// TestWrapWithoutAResolverRefusesAKeyedRequestEvenWithATenantHeader checks
// that the middleware has no resolver that reads a header by default: with
// no PrincipalFromRequest, a keyed request carrying X-Lenny-Tenant-ID is
// refused with 500, reason tenant_required, and the operation does not run.
//
// spec: §11.5 (Idempotency)
func TestWrapWithoutAResolverRefusesAKeyedRequestEvenWithATenantHeader(t *testing.T) {
	inner, calls := countingCreate()
	h := Wrap(inner, NewMemoryStore(), Options{})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
	req.Header.Set(HeaderName, "k1")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-User-ID", "alice")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if _, reason := envelopeCodeAndReason(t, rec.Body); reason != "tenant_required" {
		t.Errorf("reason = %q, want tenant_required", reason)
	}
	if calls.Load() != 0 {
		t.Errorf("operation ran %d times, want 0", calls.Load())
	}
}

// TestTheCredentialWrapperPassesTheScopeThrough checks that a store wrapped
// by WithCredentials files and finds records under the full scope: a record
// put under alice's scope is found under it and not under bob's or under
// alice bound to a session, and Claim and Release address the same scope.
//
// spec: §11.5 (Idempotency)
func TestTheCredentialWrapperPassesTheScopeThrough(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryStore()
	store := WithCredentials(inner, passthroughRedactor{})
	alice := aliceKey("k1")
	bob := idempotency.Key{TenantID: "acme", Subject: "bob", Value: "k1"}
	aliceS1 := idempotency.Key{TenantID: "acme", Subject: "alice", SessionID: "s1", Value: "k1"}

	if err := store.Put(ctx, idempotency.Record{Key: alice, BodyHash: "h", Response: idempotency.Response{StatusCode: 201, Body: []byte(`{}`)}}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, found, err := store.Get(ctx, alice); err != nil || !found {
		t.Fatalf("Get(alice) found=%v err=%v, want found", found, err)
	}
	for _, other := range []idempotency.Key{bob, aliceS1} {
		if _, found, err := store.Get(ctx, other); err != nil || found {
			t.Errorf("Get(%+v) found=%v err=%v, want not found", other, found, err)
		}
		if _, claimed, err := store.Claim(ctx, other, "h", time.Now().UTC()); err != nil || !claimed {
			t.Errorf("Claim(%+v) claimed=%v err=%v, want claimed", other, claimed, err)
		}
	}
	if _, claimed, err := store.Claim(ctx, alice, "h", time.Now().UTC()); err != nil || claimed {
		t.Errorf("Claim(alice) claimed=%v err=%v, want the stored record", claimed, err)
	}
	if err := store.Release(ctx, bob); err != nil {
		t.Fatalf("Release(bob): %v", err)
	}
	if _, found, _ := inner.Get(ctx, alice); !found {
		t.Error("releasing bob's record removed alice's")
	}
	if _, found, _ := inner.Get(ctx, aliceS1); !found {
		t.Error("releasing bob's record removed alice's session-bound record")
	}
}

// TestTheMemoryStoreRefusesAKeyWithNoSubject checks that the in-memory
// store, like the Postgres store, files no record under an empty subject.
//
// spec: §11.5 (Idempotency)
func TestTheMemoryStoreRefusesAKeyWithNoSubject(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	key := idempotency.Key{TenantID: "acme", Value: "k1"}
	if _, _, err := store.Claim(ctx, key, "h", time.Now().UTC()); err == nil {
		t.Error("Claim with no subject succeeded, want an error")
	}
	if err := store.Put(ctx, idempotency.Record{Key: key, BodyHash: "h"}); err == nil {
		t.Error("Put with no subject succeeded, want an error")
	}
}

// TestTheMemoryStoreDeletesOneUsersRecordsUnderEverySession checks that
// DeleteByUser removes every record of one subject in one tenant, under
// every bound session, and leaves the records of other subjects and other
// tenants; that DeleteByTenant removes one tenant's records; and that both
// refuse an empty scope.
//
// spec: §12.8 (Compliance Interfaces)
func TestTheMemoryStoreDeletesOneUsersRecordsUnderEverySession(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	alice := aliceKey("k1")
	aliceS1 := idempotency.Key{TenantID: "acme", Subject: "alice", SessionID: "s1", Value: "k1"}
	aliceS2 := idempotency.Key{TenantID: "acme", Subject: "alice", SessionID: "s2", Value: "k2"}
	bob := idempotency.Key{TenantID: "acme", Subject: "bob", Value: "k1"}
	globexAlice := idempotency.Key{TenantID: "globex", Subject: "alice", Value: "k1"}
	for _, k := range []idempotency.Key{alice, aliceS1, aliceS2, bob, globexAlice} {
		if err := store.Put(ctx, idempotency.Record{Key: k, BodyHash: "h", Response: idempotency.Response{StatusCode: 201}}); err != nil {
			t.Fatalf("Put(%+v): %v", k, err)
		}
	}

	n, err := store.DeleteByUser(ctx, "acme", "alice")
	if err != nil || n != 3 {
		t.Fatalf("DeleteByUser = %d, %v; want 3, nil", n, err)
	}
	for _, gone := range []idempotency.Key{alice, aliceS1, aliceS2} {
		if _, found, _ := store.Get(ctx, gone); found {
			t.Errorf("%+v survived DeleteByUser", gone)
		}
	}
	for _, kept := range []idempotency.Key{bob, globexAlice} {
		if _, found, _ := store.Get(ctx, kept); !found {
			t.Errorf("DeleteByUser removed %+v", kept)
		}
	}
	if n, err := store.DeleteByUser(ctx, "acme", "alice"); err != nil || n != 0 {
		t.Errorf("second DeleteByUser = %d, %v; want 0, nil", n, err)
	}

	if n, err := store.DeleteByTenant(ctx, "acme"); err != nil || n != 1 {
		t.Errorf("DeleteByTenant = %d, %v; want 1, nil", n, err)
	}
	if _, found, _ := store.Get(ctx, globexAlice); !found {
		t.Error("DeleteByTenant(acme) removed globex's record")
	}

	if _, err := store.DeleteByUser(ctx, "acme", ""); err == nil {
		t.Error("DeleteByUser with no subject succeeded, want an error")
	}
	if _, err := store.DeleteByUser(ctx, "", "alice"); err == nil {
		t.Error("DeleteByUser with no tenant succeeded, want an error")
	}
	if _, err := store.DeleteByTenant(ctx, ""); err == nil {
		t.Error("DeleteByTenant with no tenant succeeded, want an error")
	}
	if _, found, _ := store.Get(ctx, globexAlice); !found {
		t.Error("a refused deletion removed a record")
	}
}

// passthroughRedactor is a CredentialRedactor that changes nothing.
type passthroughRedactor struct{}

func (passthroughRedactor) Strip(b []byte) []byte  { return b }
func (passthroughRedactor) Remint(b []byte) []byte { return b }

// envelopeCodeAndReason decodes a §15.1 error envelope and returns its code
// and details.reason.
func envelopeCodeAndReason(t *testing.T, body io.Reader) (string, string) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	reason, _ := env.Error.Details["reason"].(string)
	return env.Error.Code, reason
}
