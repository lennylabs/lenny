// SPDX-License-Identifier: MIT

package toolops

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/callercheck"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// TestRequestCallerReadsThePrincipal checks that the caller identity is the
// context's principal, copied field by field, and that a context with no
// principal names no caller.
// spec: GWSEAM-RequestCaller
func TestRequestCallerReadsThePrincipal(t *testing.T) {
	if _, ok := (Callers{}).RequestCaller(context.Background()); ok {
		t.Fatal("a context with no principal resolved a caller")
	}
	groups := []string{"eng"}
	ctx := authmw.WithPrincipal(context.Background(), authmw.Principal{
		Subject: "alice", TenantID: "acme", SessionID: "sess_1", CallerType: "agent", Groups: groups,
		JTI: "jti-1", Origin: "playground",
	})
	got, ok := (Callers{}).RequestCaller(ctx)
	want := seam.Caller{Subject: "alice", TenantID: "acme", SessionID: "sess_1", CallerType: "agent", Groups: []string{"eng"}}
	if !ok || got.Subject != want.Subject || got.TenantID != want.TenantID || got.SessionID != want.SessionID ||
		got.CallerType != want.CallerType || len(got.Groups) != 1 || got.Groups[0] != "eng" ||
		got.JTI != "jti-1" || got.Origin != "playground" {
		t.Fatalf("RequestCaller = %+v, %v; want %+v, true", got, ok, want)
	}
	got.Groups[0] = "changed"
	if groups[0] != "eng" {
		t.Error("the caller shares its group slice with the principal")
	}
}

// issuedTokens is an issued-token reader over a fixed row or error.
type issuedTokens struct {
	row issuedtokenstore.IssuedToken
	err error
}

func (f issuedTokens) Get(context.Context, string, string) (issuedtokenstore.IssuedToken, error) {
	return f.row, f.err
}

// rejectionAudit accepts every rejection row.
type rejectionAudit struct{ rows int }

func (a *rejectionAudit) Append(context.Context, string, string, json.RawMessage, time.Time) (audit.Row, error) {
	a.rows++
	return audit.Row{}, nil
}

// TestCheckCreatingCallerMapsTheCallerCheck checks that a context with no
// principal and a bearer the check admits pass, that a refused bearer comes
// back as a CallerRefusal once its rejection row is written, and that a
// failed read is a transient error that is no refusal.
// spec: GWSEAM-CheckCreatingCaller
func TestCheckCreatingCallerMapsTheCallerCheck(t *testing.T) {
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	bearer := authmw.WithPrincipal(context.Background(), authmw.Principal{Subject: "alice@acme.com", TenantID: "acme", JTI: "jti-alice"})
	revoked := issuedtokenstore.IssuedToken{JTI: "jti-alice", TenantID: "acme", Subject: "alice@acme.com", RevokedAt: at.Add(-time.Minute)}

	rec := &rejectionAudit{}
	refusing := NewCallerCheck(callercheck.Deps{IssuedTokens: issuedTokens{row: revoked}, Audit: rec})
	if err := refusing.CheckCreatingCaller(context.Background(), at); err != nil {
		t.Errorf("no principal: err = %v, want nil", err)
	}
	err := refusing.CheckCreatingCaller(bearer, at)
	var refusal *seam.CallerRefusal
	if !errors.As(err, &refusal) || refusal.Reason == "" || !errors.Is(err, seam.ErrCallerRefused) {
		t.Fatalf("revoked bearer: err = %v, want a CallerRefusal with a reason", err)
	}
	if rec.rows != 1 {
		t.Errorf("rejection rows = %d, want 1 before the refusal returns", rec.rows)
	}

	failing := NewCallerCheck(callercheck.Deps{IssuedTokens: issuedTokens{err: errors.New("store down")}, Audit: rec})
	err = failing.CheckCreatingCaller(bearer, at)
	if err == nil || errors.Is(err, seam.ErrCallerRefused) {
		t.Fatalf("failed read: err = %v, want a transient error", err)
	}

	admitting := NewCallerCheck(callercheck.Deps{IssuedTokens: issuedTokens{err: issuedtokenstore.ErrNotFound}})
	if err := admitting.CheckCreatingCaller(bearer, at); err != nil {
		t.Errorf("unknown bearer: err = %v, want nil", err)
	}
	if err := (CallerCheck{}).CheckCreatingCaller(bearer, at); err != nil {
		t.Errorf("zero check: err = %v, want nil", err)
	}
}
