// SPDX-License-Identifier: MIT

package issuedtokenstore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// spec: §13.3 (Credential Flow)
//
// The step (1a) refusals carry the reasons the specification names: a
// source whose user is invalidated reports subject_user_invalidated,
// actor_user_invalidated, or caller_user_invalidated by its role, and a
// parent at the cap reports lineage_depth_exceeded.
func TestStepOneARefusalReasons(t *testing.T) {
	for role, want := range map[SourceRole]string{
		SourceSubjectToken: "subject_user_invalidated",
		SourceActorToken:   "actor_user_invalidated",
		SourceCallerToken:  "caller_user_invalidated",
	} {
		err := &SourceUserInvalidatedError{Role: role, Subject: "alice@acme.com"}
		if got := err.Reason(); got != want {
			t.Errorf("%s: Reason() = %q, want %q", role, got, want)
		}
		if err.Error() == "" {
			t.Errorf("%s: empty error text", role)
		}
	}
	depth := &LineageDepthError{ParentJTI: "p", Depth: 32, Max: 32}
	if depth.Reason() != "lineage_depth_exceeded" || depth.Error() == "" {
		t.Errorf("LineageDepthError = %q, %q", depth.Reason(), depth.Error())
	}
	inner := &SourceRevokedError{Role: SourceSubjectToken, JTI: "b"}
	bearer := &CallerBearerRefusedError{Refusal: inner}
	var got *SourceRevokedError
	if !errors.As(bearer, &got) || got != inner || bearer.Error() == "" {
		t.Errorf("CallerBearerRefusedError does not unwrap to its refusal: %v", bearer)
	}
}

// spec: §12.2 (Storage Roles)
//
// A row's depth is 0 without a parent, the parent row's depth plus one
// when the parent has a row, and 1 when the parent has none.
func TestChildDepth(t *testing.T) {
	cases := []struct {
		parent      string
		parentDepth int
		hasRow      bool
		want        int
	}{
		{"", 7, true, 0},
		{"p", 0, true, 1},
		{"p", 31, true, 32},
		{"p", 0, false, 1},
	}
	for _, tc := range cases {
		if got := childDepth(tc.parent, tc.parentDepth, tc.hasRow); got != tc.want {
			t.Errorf("childDepth(%q, %d, %t) = %d, want %d", tc.parent, tc.parentDepth, tc.hasRow, got, tc.want)
		}
	}
}

// spec: §13.3 (Credential Flow), §11.4 (User Invalidation)
//
// The lineage bounds default to a depth cap of 32 and a revocation timeout
// of 5 seconds, a non-positive bound takes its default, and WithLimits
// keeps the pool and applies the bounds it is given.
func TestLimitsDefaults(t *testing.T) {
	d := DefaultLimits()
	if d.LineageMaxDepth != 32 || d.RevocationTimeout != 5*time.Second {
		t.Fatalf("DefaultLimits() = %+v, want 32 and 5s", d)
	}
	if got := (Limits{LineageMaxDepth: -1}).withDefaults(); got != d {
		t.Errorf("withDefaults of non-positive bounds = %+v, want %+v", got, d)
	}
	s := New(nil)
	if s.Limits() != d {
		t.Errorf("New().Limits() = %+v, want the defaults", s.Limits())
	}
	tuned := s.WithLimits(Limits{LineageMaxDepth: 4})
	if tuned.Limits().LineageMaxDepth != 4 || tuned.Limits().RevocationTimeout != d.RevocationTimeout || tuned.pool != s.pool {
		t.Errorf("WithLimits = %+v", tuned.Limits())
	}
}

// spec: §13.3 (Credential Flow)
//
// A statement cancelled by statement_timeout maps to ErrRevocationTimeout,
// unless the caller's own context ended, and every other error passes
// through unchanged.
func TestRevocationTimeoutError(t *testing.T) {
	timeout := fmt.Errorf("cascade: %w", &pgconn.PgError{Code: "57014"})
	if err := revocationTimeoutError(context.Background(), timeout); !errors.Is(err, ErrRevocationTimeout) {
		t.Errorf("57014 = %v, want ErrRevocationTimeout", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := revocationTimeoutError(cancelled, timeout); errors.Is(err, ErrRevocationTimeout) {
		t.Errorf("57014 after the caller's context ended = %v, want it unchanged", err)
	}
	other := &pgconn.PgError{Code: "40P01"}
	if err := revocationTimeoutError(context.Background(), other); err != other {
		t.Errorf("40P01 = %v, want it unchanged", err)
	}
}

// spec: §13.3 (Credential Flow)
//
// JTIsIn returns the jtis the impersonation step revoked in one tenant,
// which the cascade accepts as revoked callers.
func TestImpersonationStepResultJTIsIn(t *testing.T) {
	r := ImpersonationStepResult{Revoked: []RevokedImpersonation{
		{JTI: "a", TenantID: "acme"}, {JTI: "g", TenantID: "globex"}, {JTI: "b", TenantID: "acme"},
	}}
	got := r.JTIsIn("acme")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("JTIsIn(acme) = %v, want [a b]", got)
	}
	if got := r.JTIsIn("initech"); got != nil {
		t.Errorf("JTIsIn(initech) = %v, want nil", got)
	}
}
