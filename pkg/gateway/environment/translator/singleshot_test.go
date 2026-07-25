// SPDX-License-Identifier: MIT

package translator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
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
		UserID:      "alice",
		RuntimeRef:  "echo",
		Environment: "prod",
	}
	id, err := b.BindSingleShot(context.Background(), spec)
	if err != nil {
		t.Fatalf("BindSingleShot: %v", err)
	}
	if id != "sess_noop_1" {
		t.Fatalf("session id: got %q, want %q", id, "sess_noop_1")
	}

	row, err := store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("state: got %q, want %q", row.State, session.StateRunning)
	}
	if row.TenantID != "acme" || row.UserID != "alice" ||
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
	id, err := b.BindSingleShot(context.Background(), SingleShotSpec{TenantID: "acme"})
	if err == nil {
		t.Fatalf("expected error on duplicate id, got nil (id=%q)", id)
	}
	if id != "" {
		t.Errorf("session id on error: got %q, want empty", id)
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
