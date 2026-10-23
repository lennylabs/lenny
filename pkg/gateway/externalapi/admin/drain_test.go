// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// drainLister returns one canned answer per List call and records the
// tenant and filter of each call.
type drainLister struct {
	answers [][]sessionstore.Session
	errs    []error
	tenants []string
	filters []sessionstore.ListFilter
}

func (l *drainLister) List(_ context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	i := len(l.filters)
	l.tenants = append(l.tenants, tenantID)
	l.filters = append(l.filters, f)
	if i < len(l.errs) && l.errs[i] != nil {
		return nil, l.errs[i]
	}
	if i < len(l.answers) {
		return l.answers[i], nil
	}
	return nil, nil
}

// drainTerminator answers ForceTerminate from a per-id error table and
// records each call. onCall, when set, runs inside the call with the
// context the drain passed.
type drainTerminator struct {
	errs     map[string]error
	calls    []string
	onCall   func(ctx context.Context, id string)
	tenantOf []string
}

func (d *drainTerminator) ForceTerminate(ctx context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error) {
	d.calls = append(d.calls, id)
	d.tenantOf = append(d.tenantOf, tenantID)
	if d.onCall != nil {
		d.onCall(ctx, id)
	}
	if err := d.errs[id]; err != nil {
		return sessionstore.Session{}, "", false, err
	}
	return sessionstore.Session{ID: id, TenantID: tenantID, State: session.StateFailed}, session.StateRunning, true, nil
}

func row(id string, st session.State) sessionstore.Session {
	return sessionstore.Session{ID: id, TenantID: "acme", UserID: "alice", State: st}
}

// TestDrainSessions_Table_spec_12_8 pins the shared lifecycle drain:
// a primary listing, one pass that continues past per-session errors, a
// verifying primary listing, and the Failed and Remaining report.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.3 (Postgres HA Requirements: Read replicas), 24.11 (Session investigation)
func TestDrainSessions_Table_spec_12_8(t *testing.T) {
	listErr := errors.New("primary unavailable")
	termErr := errors.New("update conflict")
	running := session.StateRunning
	done := session.StateCompleted

	cases := []struct {
		name           string
		userID         string
		answers        [][]sessionstore.Session
		listErrs       []error
		termErrs       map[string]error
		want           admin.DrainResult
		wantCalls      []string
		wantIncomplete bool
		wantErrIs      error
		wantListings   int
	}{
		{
			name:         "empty scope",
			want:         admin.DrainResult{},
			wantListings: 2,
		},
		{
			name:         "all terminal",
			answers:      [][]sessionstore.Session{{row("s1", done), row("s2", session.StateFailed)}, {row("s1", done), row("s2", session.StateFailed)}},
			want:         admin.DrainResult{},
			wantListings: 2,
		},
		{
			name:         "mix of terminal and live, user scope",
			userID:       "alice",
			answers:      [][]sessionstore.Session{{row("s1", running), row("s2", done), row("s3", session.StateSuspended)}, {row("s1", session.StateFailed), row("s2", done), row("s3", session.StateFailed)}},
			want:         admin.DrainResult{Terminated: 2},
			wantCalls:    []string{"s1", "s3"},
			wantListings: 2,
		},
		{
			name:           "one transition fails, the others still terminate",
			answers:        [][]sessionstore.Session{{row("s1", running), row("s2", running), row("s3", running)}, {row("s1", session.StateFailed), row("s2", running), row("s3", session.StateFailed)}},
			termErrs:       map[string]error{"s2": termErr},
			want:           admin.DrainResult{Terminated: 2, Failed: []string{"s2"}},
			wantCalls:      []string{"s1", "s2", "s3"},
			wantIncomplete: true,
			wantListings:   2,
		},
		{
			name:         "a row deleted after the listing has nothing to drain",
			answers:      [][]sessionstore.Session{{row("s1", running), row("s2", running)}, {row("s1", session.StateFailed)}},
			termErrs:     map[string]error{"s2": sessionstore.ErrNotFound},
			want:         admin.DrainResult{Terminated: 1},
			wantCalls:    []string{"s1", "s2"},
			wantListings: 2,
		},
		{
			name:         "first listing error",
			listErrs:     []error{listErr},
			want:         admin.DrainResult{},
			wantErrIs:    listErr,
			wantListings: 1,
		},
		{
			name:         "verifying listing error",
			answers:      [][]sessionstore.Session{{row("s1", running)}},
			listErrs:     []error{nil, listErr},
			want:         admin.DrainResult{Terminated: 1},
			wantCalls:    []string{"s1"},
			wantErrIs:    listErr,
			wantListings: 2,
		},
		{
			name:           "verifying listing finds a new live row",
			answers:        [][]sessionstore.Session{{row("s1", running)}, {row("s1", session.StateFailed), row("s9", session.StateCreated)}},
			want:           admin.DrainResult{Terminated: 1, Remaining: []string{"s9"}},
			wantCalls:      []string{"s1"},
			wantIncomplete: true,
			wantListings:   2,
		},
		{
			name:           "a failed id is not repeated in Remaining",
			answers:        [][]sessionstore.Session{{row("s1", running)}, {row("s1", running)}},
			termErrs:       map[string]error{"s1": termErr},
			want:           admin.DrainResult{Failed: []string{"s1"}},
			wantCalls:      []string{"s1"},
			wantIncomplete: true,
			wantListings:   2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := &drainLister{answers: tc.answers, errs: tc.listErrs}
			d := &drainTerminator{errs: tc.termErrs}
			got, err := admin.DrainSessions(context.Background(), l, d, "acme", tc.userID)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("result = %+v, want %+v", got, tc.want)
			}
			if !reflect.DeepEqual(d.calls, tc.wantCalls) {
				t.Errorf("ForceTerminate calls = %v, want %v", d.calls, tc.wantCalls)
			}
			for _, tn := range d.tenantOf {
				if tn != "acme" {
					t.Errorf("ForceTerminate tenant = %q, want acme", tn)
				}
			}
			if got := errors.Is(err, admin.ErrDrainIncomplete); got != tc.wantIncomplete {
				t.Errorf("errors.Is(err, ErrDrainIncomplete) = %v, want %v (err %v)", got, tc.wantIncomplete, err)
			}
			if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
				t.Errorf("err = %v, want wrapping %v", err, tc.wantErrIs)
			}
			if !tc.wantIncomplete && tc.wantErrIs == nil && err != nil {
				t.Errorf("err = %v, want nil", err)
			}
			assertPrimaryListings(t, l, tc.userID, tc.wantListings)
		})
	}
}

// TestDrainSessions_DeadlineStopsBetweenSessions_spec_12_8 pins that a
// caller's deadline ends the drain between sessions and never inside
// one: the transition in flight keeps a live context, no later
// transition runs, and the error wraps context.DeadlineExceeded.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestDrainSessions_DeadlineStopsBetweenSessions_spec_12_8(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	l := &drainLister{answers: [][]sessionstore.Session{{
		row("s1", session.StateRunning), row("s2", session.StateRunning), row("s3", session.StateRunning),
	}}}
	var inFlight context.Context
	d := &drainTerminator{onCall: func(callCtx context.Context, id string) {
		if id != "s1" {
			return
		}
		inFlight = callCtx
		<-ctx.Done()
	}}
	got, err := admin.DrainSessions(ctx, l, d, "acme", "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want wrapping context.DeadlineExceeded", err)
	}
	if errors.Is(err, admin.ErrDrainIncomplete) {
		t.Errorf("err = %v, want the context error rather than ErrDrainIncomplete", err)
	}
	if inFlight == nil || inFlight.Err() != nil {
		t.Errorf("in-flight transition context ended (%v), want live", inFlight)
	}
	if !reflect.DeepEqual(d.calls, []string{"s1"}) {
		t.Errorf("ForceTerminate calls = %v, want [s1] only", d.calls)
	}
	if got.Terminated != 1 {
		t.Errorf("partial result Terminated = %d, want 1", got.Terminated)
	}
	assertPrimaryListings(t, l, "", 1)
}

// assertPrimaryListings asserts the drain listed exactly want times, each
// time from the primary with the scope's user filter and nothing else.
func assertPrimaryListings(t *testing.T, l *drainLister, userID string, want int) {
	t.Helper()
	if len(l.filters) != want {
		t.Errorf("listings = %d, want %d", len(l.filters), want)
	}
	wantFilter := sessionstore.ListFilter{UserID: userID, Primary: true}
	for i, f := range l.filters {
		if !reflect.DeepEqual(f, wantFilter) {
			t.Errorf("listing %d filter = %+v, want %+v", i, f, wantFilter)
		}
		if l.tenants[i] != "acme" {
			t.Errorf("listing %d tenant = %q, want acme", i, l.tenants[i])
		}
	}
}
