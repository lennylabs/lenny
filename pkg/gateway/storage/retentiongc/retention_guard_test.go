// SPDX-License-Identifier: MIT

package retentiongc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/retentiongc"
)

// guardCap is the post-terminal window cap the guard tests run with.
const guardCap = 10 * time.Minute

// guardCase is one row the retention guard either collects or leaves.
type guardCase struct {
	name    string
	row     sessionstore.Session
	opts    retentiongc.Options
	collect bool
}

// runGuardCase seeds tc.row, runs one Tick at gcClock, and checks whether
// the row was collected.
func runGuardCase(t *testing.T, tc guardCase) {
	t.Helper()
	store := memstore.New()
	row := tc.row
	row.ID, row.TenantID = "sess_g", "acme"
	if row.State == "" {
		row.State = session.StateCompleted
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	arts := &recordingArtifact{name: "artifacts"}
	c := retentiongc.New(store, retentiongc.StaticTenants{"acme"}, []retentiongc.Artifact{arts.artifact()}, tc.opts)
	n, err := c.Tick(context.Background(), gcClock)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := n == 1 && len(arts.deleted) == 1; got != tc.collect {
		t.Errorf("collected = %v (n=%d, deleted=%v), want %v", got, n, arts.deleted, tc.collect)
	}
}

// TestRetentionGuardCountsFromTerminatedAt_spec_7_1 pins the §7.1 artifact
// retention guard: a row is collected only once its retention window has
// elapsed from TerminatedAt, whatever RetentionExpiresAt says. The row past
// its create-time deadline but inside the window from TerminatedAt is the
// session whose terminal roll was lost; on the pre-fix collector it is
// collected, and here it is left. A zero TerminatedAt, a nil RetentionFor,
// and a RetentionFor error leave the row, and a legal hold still wins.
//
// spec: §7.1 (Artifact retention), §12.5 (Artifact Store)
func TestRetentionGuardCountsFromTerminatedAt_spec_7_1(t *testing.T) {
	past := gcClock.Add(-time.Hour)
	ended := gcClock.Add(-2 * testRetention)
	failing := func(context.Context, sessionstore.Session) (time.Duration, error) {
		return 0, errors.New("tenant store unreachable")
	}
	for _, tc := range []guardCase{
		{
			name: "past both deadlines is collected",
			row:  sessionstore.Session{RetentionExpiresAt: past, TerminatedAt: ended},
			opts: retentiongc.Options{RetentionFor: fixedRetention}, collect: true,
		},
		{
			name: "lost roll: past the create-time deadline, inside the window from TerminatedAt",
			row:  sessionstore.Session{RetentionExpiresAt: past, TerminatedAt: gcClock.Add(-time.Hour)},
			opts: retentiongc.Options{RetentionFor: fixedRetention}, collect: false,
		},
		{
			name: "zero TerminatedAt is ineligible",
			row:  sessionstore.Session{RetentionExpiresAt: past},
			opts: retentiongc.Options{RetentionFor: fixedRetention}, collect: false,
		},
		{
			name: "nil RetentionFor is ineligible",
			row:  sessionstore.Session{RetentionExpiresAt: past, TerminatedAt: ended},
			opts: retentiongc.Options{}, collect: false,
		},
		{
			name: "a RetentionFor error leaves the row",
			row:  sessionstore.Session{RetentionExpiresAt: past, TerminatedAt: ended},
			opts: retentiongc.Options{RetentionFor: failing}, collect: false,
		},
		{
			name: "legal hold still wins",
			row:  sessionstore.Session{RetentionExpiresAt: past, TerminatedAt: ended, LegalHold: true},
			opts: retentiongc.Options{RetentionFor: fixedRetention}, collect: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) { runGuardCase(t, tc) })
	}
}

// TestRetentionGuardHoldsThePostTerminalWindow_spec_7_1 pins the §7.1
// post-terminal window check of the retention guard: a row past both
// deadlines whose pod release is not recorded and whose TerminatedAt is
// inside the cap is left, because a seal may still be uploading under its
// prefix; it is collected once PodReleasedAt is set or the cap passes. With
// no cap (Options{} wiring), every terminal row that names a pod and
// records no release is left, so an unwired cap fails closed.
//
// spec: §7.1 (Post-terminal window), §7.1 (Artifact retention), §12.8 (Compliance Interfaces)
func TestRetentionGuardHoldsThePostTerminalWindow_spec_7_1(t *testing.T) {
	past := gcClock.Add(-time.Hour)
	// A tier window shorter than the cap: the row is past both retention
	// deadlines while still inside its post-terminal window.
	short := func(context.Context, sessionstore.Session) (time.Duration, error) { return time.Minute, nil }
	recent := gcClock.Add(-5 * time.Minute)
	for _, tc := range []guardCase{
		{
			name: "inside the window, release not recorded",
			row:  sessionstore.Session{RetentionExpiresAt: past, TerminatedAt: recent, PodAssignment: "pod-a"},
			opts: retentiongc.Options{RetentionFor: short, PostTerminalCap: guardCap}, collect: false,
		},
		{
			name: "inside the cap, release recorded",
			row: sessionstore.Session{
				RetentionExpiresAt: past, TerminatedAt: recent, PodAssignment: "pod-a", PodReleasedAt: recent.Add(time.Second),
			},
			opts: retentiongc.Options{RetentionFor: short, PostTerminalCap: guardCap}, collect: true,
		},
		{
			name: "release not recorded, cap passed",
			row:  sessionstore.Session{RetentionExpiresAt: past, TerminatedAt: gcClock.Add(-guardCap), PodAssignment: "pod-a"},
			opts: retentiongc.Options{RetentionFor: short, PostTerminalCap: guardCap}, collect: true,
		},
		{
			name: "no cap wired: a row naming a pod with no release is left",
			row:  sessionstore.Session{RetentionExpiresAt: past, TerminatedAt: gcClock.Add(-48 * time.Hour), PodAssignment: "pod-a"},
			opts: retentiongc.Options{RetentionFor: fixedRetention}, collect: false,
		},
		{
			name: "no cap wired: a row whose release is recorded is collected",
			row: sessionstore.Session{
				RetentionExpiresAt: past, TerminatedAt: gcClock.Add(-48 * time.Hour), PodAssignment: "pod-a",
				PodReleasedAt: gcClock.Add(-47 * time.Hour),
			},
			opts: retentiongc.Options{RetentionFor: fixedRetention}, collect: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) { runGuardCase(t, tc) })
	}
}

// TestRetentionGuardOptionsZeroCollectsNoPodRow_spec_7_1 pins the
// Options{} collector: a terminal row past every deadline that names a pod
// and records no release is not collected and its release stays unrecorded.
//
// spec: §7.1 (Post-terminal window), §12.8 (Compliance Interfaces)
func TestRetentionGuardOptionsZeroCollectsNoPodRow_spec_7_1(t *testing.T) {
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_pod", TenantID: "acme", State: session.StateFailed, PodAssignment: "pod-a",
		RetentionExpiresAt: gcClock.Add(-time.Hour), TerminatedAt: gcClock.Add(-90 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	arts := &recordingArtifact{name: "artifacts"}
	c := retentiongc.New(store, retentiongc.StaticTenants{"acme"}, []retentiongc.Artifact{arts.artifact()}, retentiongc.Options{})
	if n, err := c.Tick(context.Background(), gcClock); err != nil || n != 0 || len(arts.deleted) != 0 {
		t.Fatalf("Tick = (%d, %v), deleted %v; want nothing collected", n, err, arts.deleted)
	}
	got, _ := store.Get(context.Background(), "acme", "sess_pod")
	if !got.PodReleasedAt.IsZero() {
		t.Errorf("PodReleasedAt = %v, want unrecorded", got.PodReleasedAt)
	}
}

// reholdStore places a legal hold, or an unreleased-pod binding, on the
// row between the listing and the pre-delete re-read.
type reholdStore struct {
	sessionstore.Store
	mutate func(*sessionstore.Session)
}

func (r *reholdStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	row, err := r.Store.Get(ctx, tenantID, id)
	if err == nil {
		r.mutate(&row)
	}
	return row, err
}

// TestRetentionGuardRereadAppliesThePredicate_spec_7_1 pins the pre-delete
// re-read to the same predicate as the listing pass: a row that the re-read
// shows inside its window from TerminatedAt, or inside its post-terminal
// window, is not collected.
//
// spec: §7.1 (Artifact retention), §7.1 (Post-terminal window)
func TestRetentionGuardRereadAppliesThePredicate_spec_7_1(t *testing.T) {
	for name, mutate := range map[string]func(*sessionstore.Session){
		"re-read inside the retention window": func(s *sessionstore.Session) { s.TerminatedAt = gcClock.Add(-time.Minute) },
		"re-read inside the post-terminal window": func(s *sessionstore.Session) {
			s.TerminatedAt = gcClock.Add(-time.Minute)
			s.PodAssignment = "pod-a"
		},
	} {
		t.Run(name, func(t *testing.T) {
			base := memstore.New()
			seed(t, base, sessionstore.Session{ID: "sess_r", RetentionExpiresAt: gcClock.Add(-time.Hour)})
			arts := &recordingArtifact{name: "artifacts"}
			c := retentiongc.New(&reholdStore{Store: base, mutate: mutate}, retentiongc.StaticTenants{"acme"},
				[]retentiongc.Artifact{arts.artifact()},
				retentiongc.Options{RetentionFor: func(context.Context, sessionstore.Session) (time.Duration, error) {
					return time.Hour, nil
				}, PostTerminalCap: guardCap})
			if n, err := c.Tick(context.Background(), gcClock); err != nil || n != 0 || len(arts.deleted) != 0 {
				t.Errorf("Tick = (%d, %v), deleted %v; want nothing collected", n, err, arts.deleted)
			}
		})
	}
}
