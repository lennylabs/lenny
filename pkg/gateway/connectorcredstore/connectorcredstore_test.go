// SPDX-License-Identifier: MIT

package connectorcredstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
)

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestMemoryPutAndGet(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	store := connectorcredstore.NewMemory(fixedClock(now))
	cred := connectorcredstore.ConnectorCredential{
		TenantID:     "acme",
		ConnectorID:  "github",
		UserID:       "alice@acme.com",
		AccessToken:  "at-1",
		RefreshToken: "rt-1",
		TokenType:    "Bearer",
		Scopes:       []string{"repo"},
		ExpiresAt:    now.Add(time.Hour),
	}
	if err := store.Put(context.Background(), cred); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Get(context.Background(), "acme", "github", "alice@acme.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != "at-1" || got.RefreshToken != "rt-1" {
		t.Fatalf("Get returned the wrong credential: %+v", got)
	}
	if !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}
	if !got.HasToken() {
		t.Fatalf("HasToken = false for a credential with an access token")
	}
}

func TestMemoryGetMissing(t *testing.T) {
	store := connectorcredstore.NewMemory(nil)
	_, err := store.Get(context.Background(), "acme", "github", "nobody@acme.com")
	if !errors.Is(err, connectorcredstore.ErrNotFound) {
		t.Fatalf("Get of a missing triple: got %v, want ErrNotFound", err)
	}
}

// TestMemoryPutReplacesAndPreservesCreatedAt covers a re-authorization
// or a token refresh: a second Put for the same triple replaces the
// token but keeps the original CreatedAt and advances UpdatedAt.
func TestMemoryPutReplacesAndPreservesCreatedAt(t *testing.T) {
	first := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	second := first.Add(2 * time.Hour)
	clk := first
	store := connectorcredstore.NewMemory(func() time.Time { return clk })

	base := connectorcredstore.ConnectorCredential{
		TenantID: "acme", ConnectorID: "github", UserID: "alice@acme.com", AccessToken: "at-old",
	}
	if err := store.Put(context.Background(), base); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	clk = second
	base.AccessToken = "at-new"
	if err := store.Put(context.Background(), base); err != nil {
		t.Fatalf("second Put: %v", err)
	}
	got, err := store.Get(context.Background(), "acme", "github", "alice@acme.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != "at-new" {
		t.Fatalf("AccessToken = %q, want at-new", got.AccessToken)
	}
	if !got.CreatedAt.Equal(first) {
		t.Fatalf("CreatedAt = %v, want the original %v", got.CreatedAt, first)
	}
	if !got.UpdatedAt.Equal(second) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, second)
	}
}

func TestMemoryPutRejectsIncompleteCredential(t *testing.T) {
	store := connectorcredstore.NewMemory(nil)
	for i, c := range []connectorcredstore.ConnectorCredential{
		{ConnectorID: "github", UserID: "a", AccessToken: "t"},      // no tenant
		{TenantID: "acme", UserID: "a", AccessToken: "t"},           // no connector
		{TenantID: "acme", ConnectorID: "github", AccessToken: "t"}, // no user
		{TenantID: "acme", ConnectorID: "github", UserID: "a"},      // no access token
	} {
		if err := store.Put(context.Background(), c); err == nil {
			t.Errorf("case %d: Put accepted an incomplete credential", i)
		}
	}
}

func TestMemoryDelete(t *testing.T) {
	store := connectorcredstore.NewMemory(nil)
	cred := connectorcredstore.ConnectorCredential{
		TenantID: "acme", ConnectorID: "github", UserID: "alice@acme.com", AccessToken: "at-1",
	}
	if err := store.Put(context.Background(), cred); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Delete(context.Background(), "acme", "github", "alice@acme.com"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(context.Background(), "acme", "github", "alice@acme.com"); !errors.Is(err, connectorcredstore.ErrNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrNotFound", err)
	}
	if err := store.Delete(context.Background(), "acme", "github", "alice@acme.com"); !errors.Is(err, connectorcredstore.ErrNotFound) {
		t.Fatalf("Delete of an absent triple: got %v, want ErrNotFound", err)
	}
}

// spec: §4.3 line 200 / §9.3 — RotateAccessToken replaces only the
// access token and stamps UpdatedAt; the previously stored refresh
// token survives when RotationRecord.RefreshToken is empty.
func TestMemoryRotateAccessTokenKeepsPriorRefreshToken(t *testing.T) {
	first := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	clk := first
	store := connectorcredstore.NewMemory(func() time.Time { return clk })
	if err := store.Put(context.Background(), connectorcredstore.ConnectorCredential{
		TenantID: "acme", ConnectorID: "github", UserID: "alice@acme.com",
		AccessToken: "at-old", RefreshToken: "rt-old", TokenType: "Bearer",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	clk = second
	if err := store.RotateAccessToken(context.Background(), connectorcredstore.RotationRecord{
		TenantID: "acme", ConnectorID: "github", UserID: "alice@acme.com",
		AccessToken: "at-new",
		ExpiresAt:   second.Add(time.Hour),
	}); err != nil {
		t.Fatalf("RotateAccessToken: %v", err)
	}
	got, err := store.Get(context.Background(), "acme", "github", "alice@acme.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != "at-new" {
		t.Errorf("AccessToken = %q, want at-new", got.AccessToken)
	}
	if got.RefreshToken != "rt-old" {
		t.Errorf("RefreshToken = %q, want rt-old (no rotation)", got.RefreshToken)
	}
	if !got.UpdatedAt.Equal(second) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, second)
	}
	if !got.CreatedAt.Equal(first) {
		t.Errorf("CreatedAt = %v, want the original %v", got.CreatedAt, first)
	}
}

// spec: §4.3 line 200 / §9.3 — RotateAccessToken accepts a rotated
// refresh token and replaces the stored value.
func TestMemoryRotateAccessTokenReplacesRotatedRefreshToken(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	store := connectorcredstore.NewMemory(fixedClock(now))
	if err := store.Put(context.Background(), connectorcredstore.ConnectorCredential{
		TenantID: "acme", ConnectorID: "github", UserID: "alice@acme.com",
		AccessToken: "at-old", RefreshToken: "rt-old",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.RotateAccessToken(context.Background(), connectorcredstore.RotationRecord{
		TenantID: "acme", ConnectorID: "github", UserID: "alice@acme.com",
		AccessToken: "at-new", RefreshToken: "rt-new",
	}); err != nil {
		t.Fatalf("RotateAccessToken: %v", err)
	}
	got, _ := store.Get(context.Background(), "acme", "github", "alice@acme.com")
	if got.RefreshToken != "rt-new" {
		t.Errorf("RefreshToken = %q, want rt-new (rotated)", got.RefreshToken)
	}
}

// spec: §4.3 line 200 — RotateAccessToken on a missing triple returns
// ErrNotFound; the caller must Put first.
func TestMemoryRotateAccessTokenMissing(t *testing.T) {
	store := connectorcredstore.NewMemory(nil)
	err := store.RotateAccessToken(context.Background(), connectorcredstore.RotationRecord{
		TenantID: "acme", ConnectorID: "github", UserID: "alice@acme.com",
		AccessToken: "at-new",
	})
	if !errors.Is(err, connectorcredstore.ErrNotFound) {
		t.Fatalf("RotateAccessToken on a missing triple: got %v, want ErrNotFound", err)
	}
}

// spec: §4.3 line 200 — RotateAccessToken rejects incomplete inputs.
func TestMemoryRotateAccessTokenRejectsIncompleteRecord(t *testing.T) {
	store := connectorcredstore.NewMemory(nil)
	for i, r := range []connectorcredstore.RotationRecord{
		{ConnectorID: "github", UserID: "a", AccessToken: "t"},      // no tenant
		{TenantID: "acme", UserID: "a", AccessToken: "t"},           // no connector
		{TenantID: "acme", ConnectorID: "github", AccessToken: "t"}, // no user
		{TenantID: "acme", ConnectorID: "github", UserID: "a"},      // no access token
	} {
		if err := store.RotateAccessToken(context.Background(), r); err == nil {
			t.Errorf("case %d: RotateAccessToken accepted an incomplete record", i)
		}
	}
}

func TestMemoryListByConnector(t *testing.T) {
	store := connectorcredstore.NewMemory(nil)
	put := func(connector, user string) {
		if err := store.Put(context.Background(), connectorcredstore.ConnectorCredential{
			TenantID: "acme", ConnectorID: connector, UserID: user, AccessToken: "at",
		}); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	put("github", "bob@acme.com")
	put("github", "alice@acme.com")
	put("jira", "alice@acme.com")
	// A different tenant's credential must not leak into the list.
	if err := store.Put(context.Background(), connectorcredstore.ConnectorCredential{
		TenantID: "globex", ConnectorID: "github", UserID: "carol@globex.com", AccessToken: "at",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rows, err := store.ListByConnector(context.Background(), "acme", "github")
	if err != nil {
		t.Fatalf("ListByConnector: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListByConnector returned %d rows, want 2", len(rows))
	}
	// Ordered by user id.
	if rows[0].UserID != "alice@acme.com" || rows[1].UserID != "bob@acme.com" {
		t.Fatalf("ListByConnector order: %q, %q", rows[0].UserID, rows[1].UserID)
	}
}
