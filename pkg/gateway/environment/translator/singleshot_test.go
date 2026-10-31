// SPDX-License-Identifier: MIT

package translator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §15 built-in adapter single-shot compute model; §17.4 in-memory mode.
func TestNoopSingleShotBinderPersistsRunningRow(t *testing.T) {
	store := memstore.New()
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := newNoopSingleShotBinder(
		store,
		func() string { return "sess_noop_1" },
		func() time.Time { return fixed },
	)

	spec := SingleShotSpec{
		TenantID:    "acme",
		RuntimeRef:  "echo",
		Environment: "prod",
	}
	binding, err := b.BindSingleShot(ownerContext(context.Background(), "alice@acme.com"), spec)
	if err != nil {
		t.Fatalf("BindSingleShot: %v", err)
	}
	id := binding.SessionID
	if id != "sess_noop_1" {
		t.Fatalf("session id: got %q, want %q", id, "sess_noop_1")
	}
	// spec: §15 (Open Responses conversation sessions) — the no-op binder
	// claims no pod, so it never holds the session.
	if binding.Hold {
		t.Error("no-op binder reported hold; it claims no pod and must release")
	}

	row, err := store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("state: got %q, want %q", row.State, session.StateRunning)
	}
	// spec: §10.2 (Session ownership) rule 1 — the owner is the principal's
	// subject.
	if row.TenantID != "acme" || row.UserID != "alice@acme.com" ||
		row.RuntimeRef != "echo" || row.Environment != "prod" {
		t.Errorf("row fields mismatch: %+v", row)
	}
	if !row.CreatedAt.Equal(fixed) || !row.UpdatedAt.Equal(fixed) {
		t.Errorf("timestamps: created=%v updated=%v want %v", row.CreatedAt, row.UpdatedAt, fixed)
	}
}

// TestNoopSingleShotBinderWrapsStoreError asserts the binder returns no
// session id and a wrapped, inspectable error when the store rejects the
// row, rather than a bare string, so callers can branch on the underlying
// store failure.
//
// spec: §15 built-in adapter single-shot compute model.
func TestNoopSingleShotBinderWrapsStoreError(t *testing.T) {
	store := memstore.New()
	// Pre-seed a row so the binder's Create collides on the same id.
	seed := sessionstore.Session{
		ID:        "dup_id",
		TenantID:  "acme",
		State:     session.StateRunning,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.Create(context.Background(), seed); err != nil {
		t.Fatalf("seed create: %v", err)
	}

	b := newNoopSingleShotBinder(
		store,
		func() string { return "dup_id" },
		func() time.Time { return time.Now().UTC() },
	)
	binding, err := b.BindSingleShot(ownerContext(context.Background(), "alice@acme.com"), SingleShotSpec{TenantID: "acme"})
	if err == nil {
		t.Fatalf("expected error on duplicate id, got nil (id=%q)", binding.SessionID)
	}
	if binding.SessionID != "" {
		t.Errorf("session id on error: got %q, want empty", binding.SessionID)
	}
	if !errors.Is(err, sessionstore.ErrAlreadyExists) {
		t.Errorf("error chain: %v does not wrap ErrAlreadyExists", err)
	}
}

// spec: §15 built-in adapter single-shot compute model.
func TestSingleShotErrorMessage(t *testing.T) {
	e := &SingleShotError{
		HTTPStatus:        503,
		Code:              "SESSION_CREATION_FAILED",
		Message:           "warm pool exhausted",
		RetryAfterSeconds: 5,
		Retryable:         true,
	}
	if got, want := e.Error(), "SESSION_CREATION_FAILED: warm pool exhausted"; got != want {
		t.Errorf("Error(): got %q, want %q", got, want)
	}
	// The typed error satisfies the error interface for the adapter's
	// errors.As type assertion.
	var target *SingleShotError
	if !errors.As(error(e), &target) {
		t.Errorf("errors.As did not resolve *SingleShotError")
	}
}

// refusingStore refuses every insert with a creation-fence refusal.
type refusingStore struct{ sessionstore.Store }

func (refusingStore) Create(context.Context, sessionstore.Session) error {
	return &sessionstore.CreationRefusal{Code: "USER_INVALIDATED", Message: "the session owner has been invalidated"}
}

// TestNoopSingleShotBinderMapsFenceRefusal_spec_12_8 pins the fallback
// binder's refusal surface: a fence refusal from the insert is a 403
// *SingleShotError carrying the refusal's code and message, which the
// adapter renders in its native envelope, rather than a wrapped error the
// adapter renders as 500 server_error.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestNoopSingleShotBinderMapsFenceRefusal_spec_12_8(t *testing.T) {
	b := newNoopSingleShotBinder(refusingStore{memstore.New()},
		func() string { return "sess_refused" }, time.Now)
	_, err := b.BindSingleShot(ownerContext(context.Background(), "alice@acme.com"), SingleShotSpec{TenantID: "acme"})
	var sse *SingleShotError
	if !errors.As(err, &sse) {
		t.Fatalf("err = %v, want *SingleShotError", err)
	}
	if sse.HTTPStatus != 403 || sse.Code != "USER_INVALIDATED" || sse.Message == "" {
		t.Errorf("SingleShotError = %+v, want 403 USER_INVALIDATED with the refusal's message", sse)
	}
}

// ownerContext returns ctx carrying an authenticated principal whose Subject
// is subject. The internal test package cannot import the shared
// ownerprincipal helper, which imports the session server, which imports
// this package.
func ownerContext(ctx context.Context, subject string) context.Context {
	return authmw.WithPrincipal(ctx, authmw.Principal{Subject: subject})
}

// TestNoopSingleShotBinderRefusesMissingSubject_spec_10_2 pins the default
// binder's no-subject refusal: a request with no principal, and one whose
// principal carries an empty Subject, is the 401 UNAUTHORIZED
// *SingleShotError and writes no session row. Before the binder took the
// owner from the principal it wrote an ownerless running row for both.
//
// spec: §10.2 (Session ownership) rules 1 and 3.
func TestNoopSingleShotBinderRefusesMissingSubject_spec_10_2(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
	}{
		{name: "no principal", ctx: context.Background()},
		{name: "empty subject", ctx: authmw.WithPrincipal(context.Background(), authmw.Principal{TenantID: "acme"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			b := newNoopSingleShotBinder(store, func() string { return "sess_unauth" }, time.Now)
			binding, err := b.BindSingleShot(tc.ctx, SingleShotSpec{TenantID: "acme", RuntimeRef: "echo"})
			var sse *SingleShotError
			if !errors.As(err, &sse) {
				t.Fatalf("err = %v, want *SingleShotError", err)
			}
			if sse.HTTPStatus != 401 || sse.Code != "UNAUTHORIZED" {
				t.Errorf("SingleShotError = %+v, want 401 UNAUTHORIZED", sse)
			}
			if binding.SessionID != "" {
				t.Errorf("session id on refusal = %q, want empty", binding.SessionID)
			}
			if _, gerr := store.Get(context.Background(), "acme", "sess_unauth"); !errors.Is(gerr, sessionstore.ErrNotFound) {
				t.Errorf("store.Get after refusal: err = %v, want ErrNotFound (no row written)", gerr)
			}
		})
	}
}
