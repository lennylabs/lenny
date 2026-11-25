// SPDX-License-Identifier: MIT

package idempotencymw

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/idempotency"
)

// swapRedactor stands in for the upload-token redactor: Strip replaces the
// credential with a reference, and Remint replaces the reference with the
// credential.
type swapRedactor struct{ strips, remints atomic.Int32 }

const (
	testCredential = "credential-value"
	testReference  = "reference-value"
)

func (s *swapRedactor) Strip(body []byte) []byte {
	s.strips.Add(1)
	return bytes.ReplaceAll(body, []byte(testCredential), []byte(testReference))
}

func (s *swapRedactor) Remint(stored []byte) []byte {
	s.remints.Add(1)
	return bytes.ReplaceAll(stored, []byte(testReference), []byte(testCredential))
}

// credentialHandler answers 201 with a body carrying the credential and
// counts how often it runs.
func credentialHandler(runs *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		runs.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"s1","uploadToken":"`+testCredential+`"}`)
	})
}

func postWithKey(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{"runtimeRef":"echo"}`))
	req.Header.Set(HeaderName, "k1")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestStoredResponseHoldsNoCredentialAndReplayRestoresIt checks that the
// store WithCredentials returns hands the body through the redactor on Put
// and the stored body through it when Claim finds the record, so the
// underlying store holds the reference and the client receives the
// credential both times.
//
// spec: §11.5 (Idempotency)
func TestStoredResponseHoldsNoCredentialAndReplayRestoresIt(t *testing.T) {
	store := NewMemoryStore()
	redactor := &swapRedactor{}
	var runs atomic.Int32
	h := Wrap(credentialHandler(&runs), WithCredentials(store, redactor), Options{})

	first := postWithKey(t, h)
	if first.Code != http.StatusCreated || !strings.Contains(first.Body.String(), testCredential) {
		t.Fatalf("first response = %d %s, want 201 with the credential", first.Code, first.Body)
	}
	rec, found, err := store.Get(context.Background(), "acme", "k1")
	if err != nil || !found {
		t.Fatalf("Get: found=%v err=%v", found, err)
	}
	if strings.Contains(string(rec.Response.Body), testCredential) || !strings.Contains(string(rec.Response.Body), testReference) {
		t.Fatalf("stored body = %s, want the reference in place of the credential", rec.Response.Body)
	}

	second := postWithKey(t, h)
	if second.Code != http.StatusCreated || second.Body.String() != first.Body.String() {
		t.Fatalf("replay = %d %s, want the original %s", second.Code, second.Body, first.Body)
	}
	if runs.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", runs.Load())
	}
	if redactor.strips.Load() != 1 || redactor.remints.Load() != 1 {
		t.Fatalf("strips=%d remints=%d, want 1 and 1", redactor.strips.Load(), redactor.remints.Load())
	}
}

// TestNoRedactorStoresTheBodyAsWritten checks that WithCredentials with no
// redactor leaves the store as it is.
//
// spec: §11.5 (Idempotency)
func TestNoRedactorStoresTheBodyAsWritten(t *testing.T) {
	store := NewMemoryStore()
	var runs atomic.Int32
	h := Wrap(credentialHandler(&runs), WithCredentials(store, nil), Options{})
	first := postWithKey(t, h)
	rec, _, _ := store.Get(context.Background(), "acme", "k1")
	if string(rec.Response.Body) != first.Body.String() {
		t.Fatalf("stored body = %s, want %s", rec.Response.Body, first.Body)
	}
	if replay := postWithKey(t, h); replay.Body.String() != first.Body.String() {
		t.Fatalf("replay = %s, want %s", replay.Body, first.Body)
	}
}

// TestRedactorIsNotCalledForAResponseThatIsNotStored checks that a 5xx,
// which the middleware releases rather than stores, never reaches Strip.
//
// spec: §11.5 (Idempotency)
func TestRedactorIsNotCalledForAResponseThatIsNotStored(t *testing.T) {
	redactor := &swapRedactor{}
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}), WithCredentials(NewMemoryStore(), redactor), Options{})
	postWithKey(t, h)
	if redactor.strips.Load() != 0 {
		t.Fatalf("Strip ran %d times for a 5xx, want 0", redactor.strips.Load())
	}
}

// errStore fails every call with err.
type errStore struct{ err error }

func (e errStore) Get(context.Context, string, string) (idempotency.Record, bool, error) {
	return idempotency.Record{}, false, e.err
}
func (e errStore) Put(context.Context, idempotency.Record) error { return e.err }
func (e errStore) Claim(context.Context, string, string, string, time.Time) (idempotency.Record, bool, error) {
	return idempotency.Record{}, false, e.err
}
func (e errStore) Release(context.Context, string, string) error { return e.err }

// TestCredentialStoreRemintsOnGetAndPassesErrorsAndPendingRowsThrough
// covers the store's other paths: Get of a stored record remints it, a
// pending claim and an absent record are returned untouched, a body-less
// record never reaches the redactor, Release reaches the inner store, and
// an inner store error is returned as it is.
//
// spec: §11.5 (Idempotency)
func TestCredentialStoreRemintsOnGetAndPassesErrorsAndPendingRowsThrough(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryStore()
	redactor := &swapRedactor{}
	store := WithCredentials(inner, redactor)

	if _, claimed, err := store.Claim(ctx, "acme", "k1", "h", time.Now().UTC()); err != nil || !claimed {
		t.Fatalf("first Claim: claimed=%v err=%v", claimed, err)
	}
	pending, claimed, err := store.Claim(ctx, "acme", "k1", "h", time.Now().UTC())
	if err != nil || claimed || pending.Response.StatusCode != 0 {
		t.Fatalf("second Claim: %+v claimed=%v err=%v, want the pending row", pending, claimed, err)
	}
	if _, found, err := store.Get(ctx, "acme", "absent"); err != nil || found {
		t.Fatalf("Get(absent): found=%v err=%v", found, err)
	}
	if redactor.remints.Load() != 0 {
		t.Fatalf("Remint ran %d times for body-less rows, want 0", redactor.remints.Load())
	}

	rec := idempotency.Record{
		Key:      idempotency.Key{TenantID: "acme", Value: "k1"},
		BodyHash: "h",
		Response: idempotency.Response{StatusCode: http.StatusCreated, Body: []byte(`{"t":"` + testCredential + `"}`)},
		StoredAt: time.Now().UTC(),
	}
	if err := store.Put(ctx, rec); err != nil {
		t.Fatalf("Put: %v", err)
	}
	raw, _, _ := inner.Get(ctx, "acme", "k1")
	if strings.Contains(string(raw.Response.Body), testCredential) {
		t.Fatalf("inner store holds the credential: %s", raw.Response.Body)
	}
	got, found, err := store.Get(ctx, "acme", "k1")
	if err != nil || !found || string(got.Response.Body) != string(rec.Response.Body) {
		t.Fatalf("Get = %s found=%v err=%v, want the original body", got.Response.Body, found, err)
	}
	if err := store.Release(ctx, "acme", "k1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, found, _ := inner.Get(ctx, "acme", "k1"); found {
		t.Fatal("Release did not reach the inner store")
	}

	boom := errors.New("store down")
	failing := WithCredentials(errStore{err: boom}, redactor)
	if _, _, err := failing.Get(ctx, "acme", "k1"); !errors.Is(err, boom) {
		t.Errorf("Get err = %v, want %v", err, boom)
	}
	if _, _, err := failing.Claim(ctx, "acme", "k1", "h", time.Now()); !errors.Is(err, boom) {
		t.Errorf("Claim err = %v, want %v", err, boom)
	}
	if err := failing.Put(ctx, rec); !errors.Is(err, boom) {
		t.Errorf("Put err = %v, want %v", err, boom)
	}
}
