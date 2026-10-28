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
	stale    map[string]bool
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
	if d.stale[id] {
		return sessionstore.Session{ID: id, TenantID: tenantID, State: session.StateFailed}, session.StateFailed, false, nil
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
			got, err := admin.DrainSessions(context.Background(), l, d, nil, "acme", tc.userID)
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
	got, err := admin.DrainSessions(ctx, l, d, nil, "acme", "")
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

// publishCall is one TerminateUserSessions call the drain made.
type publishCall struct {
	tenantID, userID string
	ids              []string
	ctxErr           error
}

// recordingPods is a fake binding-release fan-out: it records each call,
// with the error its context carried, and answers a canned result.
type recordingPods struct {
	calls  []publishCall
	answer admin.UserTerminationResult
	// listings, when set, records how many listings preceded each call.
	lister *drainLister
	before []int
}

func (p *recordingPods) TerminateUserSessions(ctx context.Context, tenantID, userID string, ids []string) admin.UserTerminationResult {
	p.calls = append(p.calls, publishCall{tenantID: tenantID, userID: userID, ids: append([]string(nil), ids...), ctxErr: ctx.Err()})
	if p.lister != nil {
		p.before = append(p.before, len(p.lister.filters))
	}
	return p.answer
}

// TestDrainSessionsPublishesTransitionedSessionsOnBindRelease_spec_12_8 pins
// the drain's publish on the binding-release fan-out: only the sessions the
// drain moved to a terminal state are published, once, after the
// transitions and before the verifying listing, under the scope's tenant and
// user. A row already terminal, a row deleted after the listing, a failed
// transition, and a transition another writer already made are excluded,
// and an empty set publishes nothing. The pre-fix drain published nothing,
// so a drained session bound on another replica kept its pod until the
// erasure deleted its row (F-12.8.43).
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 11.4 (User Invalidation), 28.5.7 (Gateway-to-store)
func TestDrainSessionsPublishesTransitionedSessionsOnBindRelease_spec_12_8(t *testing.T) {
	running := session.StateRunning
	cases := []struct {
		name     string
		userID   string
		rows     []sessionstore.Session
		termErrs map[string]error
		stale    map[string]bool
		want     []publishCall
	}{
		{
			name:   "only transitioned ids are published, user scope",
			userID: "alice",
			rows: []sessionstore.Session{
				row("moved", running), row("done", session.StateCompleted), row("gone", running),
				row("broken", running), row("raced", running), row("moved2", session.StateSuspended),
			},
			termErrs: map[string]error{"gone": sessionstore.ErrNotFound, "broken": errors.New("update conflict")},
			stale:    map[string]bool{"raced": true},
			want:     []publishCall{{tenantID: "acme", userID: "alice", ids: []string{"moved", "moved2"}}},
		},
		{
			name: "tenant scope publishes with an empty user",
			rows: []sessionstore.Session{row("s1", running)},
			want: []publishCall{{tenantID: "acme", ids: []string{"s1"}}},
		},
		{
			name:  "no transition publishes nothing",
			rows:  []sessionstore.Session{row("done", session.StateCompleted), row("raced", running)},
			stale: map[string]bool{"raced": true},
		},
		{
			name: "empty scope publishes nothing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := &drainLister{answers: [][]sessionstore.Session{tc.rows}}
			pods := &recordingPods{lister: l}
			d := &drainTerminator{errs: tc.termErrs, stale: tc.stale}
			_, _ = admin.DrainSessions(context.Background(), l, d, pods, "acme", tc.userID)
			if !reflect.DeepEqual(pods.calls, tc.want) {
				t.Fatalf("TerminateUserSessions calls = %+v, want %+v", pods.calls, tc.want)
			}
			for i, n := range pods.before {
				if n != 1 {
					t.Errorf("publish %d ran after %d listings, want after the first listing and before the verifying one", i, n)
				}
			}
		})
	}
}

// TestDrainSessionsPublishOutcomeLeavesTheResult_spec_12_8 pins that the
// fan-out's outcome never changes the drain's result or error: the same
// scope drained with no fan-out, with one that releases, and with one that
// reports every release failed gives the same DrainResult and error class.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 28.5.7 (Gateway-to-store)
func TestDrainSessionsPublishOutcomeLeavesTheResult_spec_12_8(t *testing.T) {
	scopes := map[string][][]sessionstore.Session{
		"clean": {{row("s1", session.StateRunning)}, {row("s1", session.StateFailed)}},
		"incomplete": {
			{row("s1", session.StateRunning), row("s2", session.StateRunning)},
			{row("s1", session.StateFailed), row("s2", session.StateRunning)},
		},
	}
	fanOuts := map[string]admin.UserPodTerminator{
		"nil":      nil,
		"released": &recordingPods{answer: admin.UserTerminationResult{PodsTerminated: 2}},
		"failed":   &recordingPods{answer: admin.UserTerminationResult{FailedSessions: []string{"s1", "s2"}}},
	}
	for scope, answers := range scopes {
		baseL := &drainLister{answers: answers}
		baseRes, baseErr := admin.DrainSessions(context.Background(), baseL, &drainTerminator{stale: map[string]bool{"s2": scope == "incomplete"}}, nil, "acme", "")
		for name, pods := range fanOuts {
			t.Run(scope+"/"+name, func(t *testing.T) {
				l := &drainLister{answers: answers}
				d := &drainTerminator{stale: map[string]bool{"s2": scope == "incomplete"}}
				got, err := admin.DrainSessions(context.Background(), l, d, pods, "acme", "")
				if !reflect.DeepEqual(got, baseRes) {
					t.Errorf("result = %+v, want %+v as without a fan-out", got, baseRes)
				}
				if errors.Is(err, admin.ErrDrainIncomplete) != errors.Is(baseErr, admin.ErrDrainIncomplete) || (err == nil) != (baseErr == nil) {
					t.Errorf("err = %v, want %v as without a fan-out", err, baseErr)
				}
			})
		}
	}
}

// TestDrainSessionsPublishesAfterTheDeadlineEndsTheLoop_spec_12_8 pins that
// a caller's deadline that ends the drain between sessions still publishes
// the sessions already transitioned, on a context that is not cancelled,
// and still returns the context error.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 28.5.7 (Gateway-to-store)
func TestDrainSessionsPublishesAfterTheDeadlineEndsTheLoop_spec_12_8(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &drainLister{answers: [][]sessionstore.Session{{row("s1", session.StateRunning), row("s2", session.StateRunning)}}}
	d := &drainTerminator{onCall: func(context.Context, string) { cancel() }}
	pods := &recordingPods{}
	_, err := admin.DrainSessions(ctx, l, d, pods, "acme", "alice")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want wrapping context.Canceled", err)
	}
	want := []publishCall{{tenantID: "acme", userID: "alice", ids: []string{"s1"}}}
	if !reflect.DeepEqual(pods.calls, want) {
		t.Errorf("TerminateUserSessions calls = %+v, want %+v on a live context", pods.calls, want)
	}
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

// TestReleasePendingListsThePostTerminalWindow_spec_12_8 pins the §12.8
// store-deletion gate's listing: it is a primary read with the scope's
// UserID, it returns exactly the rows in their §7.1 post-terminal window, a
// listing error is returned, and a zero cap returns ErrReleaseCapUnset
// without listing.
//
// spec: §12.8 (Compliance Interfaces), §7.1 (Post-terminal window)
func TestReleasePendingListsThePostTerminalWindow_spec_12_8(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	const capD = 5 * time.Minute
	recent := now.Add(-time.Minute)
	rows := []sessionstore.Session{
		{ID: "in-window", State: session.StateFailed, PodAssignment: "p1", TerminatedAt: recent},
		{ID: "released", State: session.StateCompleted, PodAssignment: "p2", TerminatedAt: recent, PodReleasedAt: recent},
		{ID: "past-cap", State: session.StateCompleted, PodAssignment: "p3", TerminatedAt: now.Add(-capD)},
		{ID: "no-pod", State: session.StateCompleted, TerminatedAt: recent},
		{ID: "live", State: session.StateRunning, PodAssignment: "p4"},
	}
	l := &drainLister{answers: [][]sessionstore.Session{rows}}
	got, err := admin.ReleasePending(context.Background(), l, "acme", "alice", now, capD)
	if err != nil || !reflect.DeepEqual(got, []string{"in-window"}) {
		t.Fatalf("ReleasePending = (%v, %v), want [in-window]", got, err)
	}
	if want := (sessionstore.ListFilter{UserID: "alice", Primary: true}); len(l.filters) != 1 || !reflect.DeepEqual(l.filters[0], want) || l.tenants[0] != "acme" {
		t.Errorf("listing = %v %v, want one primary read of acme with %+v", l.tenants, l.filters, want)
	}

	down := errors.New("primary unreachable")
	if _, err := admin.ReleasePending(context.Background(), &drainLister{errs: []error{down}}, "acme", "", now, capD); !errors.Is(err, down) {
		t.Errorf("listing error = %v, want it wrapped", err)
	}

	unwired := &drainLister{answers: [][]sessionstore.Session{rows}}
	if _, err := admin.ReleasePending(context.Background(), unwired, "acme", "", now, 0); !errors.Is(err, admin.ErrReleaseCapUnset) {
		t.Errorf("zero cap = %v, want ErrReleaseCapUnset", err)
	}
	if len(unwired.filters) != 0 {
		t.Errorf("zero cap listed %d times, want none", len(unwired.filters))
	}
}
