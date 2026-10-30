// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// fakeUserTokenStore is a userTokenStore over in-memory token sets. own
// holds the user's own tokens keyed by tenant; minted holds the live
// impersonation bearers keyed by actor and then by target tenant.
type fakeUserTokenStore struct {
	own          map[string][]string
	minted       map[string]map[string][]string
	subjectErr   error
	actorErr     error
	actorCalls   int
	subjectCalls int
	gotReason    string
	log          *[]string
}

func (f *fakeUserTokenStore) RevokeBySubject(_ context.Context, tenantID, _, reason string, _ time.Time) ([]string, error) {
	f.subjectCalls++
	f.gotReason = reason
	f.record("subject")
	if f.subjectErr != nil {
		return nil, f.subjectErr
	}
	return append([]string(nil), f.own[tenantID]...), nil
}

func (f *fakeUserTokenStore) RevokeImpersonationsByActor(_ context.Context, actorSub, reason string, _ time.Time) ([]string, error) {
	f.actorCalls++
	f.gotReason = reason
	f.record("actor")
	if f.actorErr != nil {
		return nil, f.actorErr
	}
	var out []string
	for _, jtis := range f.minted[actorSub] {
		out = append(out, jtis...)
	}
	return out, nil
}

func (f *fakeUserTokenStore) record(e string) {
	if f.log != nil {
		*f.log = append(*f.log, e)
	}
}

// fakeCrossTenantReads records every cross_tenant_read event.
type fakeCrossTenantReads struct {
	events []auditstore.CrossTenantRead
	err    error
	log    *[]string
}

func (f *fakeCrossTenantReads) EmitCrossTenantRead(_ context.Context, r auditstore.CrossTenantRead) error {
	f.events = append(f.events, r)
	if f.log != nil {
		*f.log = append(*f.log, "emit")
	}
	return f.err
}

// cascadeCtx carries the platform-admin principal the invalidate handler
// runs as.
func cascadeCtx() context.Context {
	return authmw.WithPrincipal(context.Background(), authmw.Principal{Subject: "alice@acme.com", TenantID: "platform"})
}

func newCascadeStore() *fakeUserTokenStore {
	return &fakeUserTokenStore{
		own: map[string][]string{"platform": {"own-1", "own-2"}},
		minted: map[string]map[string][]string{
			"dave@acme.com": {"acme": {"imp-acme"}, "globex": {"imp-globex"}},
		},
	}
}

func sortedJTIs(in []string) string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return strings.Join(out, ",")
}

// TestUserTokenRevokerCascadeRevokesMintedBearers_spec_13_3 asserts that a
// cascade revokes the actor's impersonation bearers in every target tenant,
// returns them with the user's own tokens, and emits exactly one
// cross_tenant_read whose row count is the cascade's count rather than the
// union's.
//
// spec: §13.3 (Platform-admin impersonation, rules 6 and 7), §11.4 step 5
func TestUserTokenRevokerCascadeRevokesMintedBearers_spec_13_3(t *testing.T) {
	store := newCascadeStore()
	reads := &fakeCrossTenantReads{}
	u := &userTokenRevoker{store: store, reads: reads}
	jtis, err := u.RevokeUserTokens(cascadeCtx(), "platform", "dave@acme.com", "user_invalidated", time.Now(), true)
	if err != nil {
		t.Fatalf("RevokeUserTokens: %v", err)
	}
	if got := sortedJTIs(jtis); got != "imp-acme,imp-globex,own-1,own-2" {
		t.Errorf("jtis = %s, want the user's own tokens and both minted bearers", got)
	}
	if store.gotReason != "user_invalidated" {
		t.Errorf("reason = %q, want user_invalidated", store.gotReason)
	}
	want := auditstore.CrossTenantRead{
		Category:      "impersonation_session",
		RowCount:      2,
		Endpoint:      "POST /v1/admin/users/{userId}/invalidate",
		CallerSubject: "alice@acme.com",
	}
	if len(reads.events) != 1 || reads.events[0] != want {
		t.Errorf("events = %+v, want exactly %+v", reads.events, want)
	}
}

// TestUserTokenRevokerCascadeEmitsOnEmptyResult_spec_13_3 asserts that a
// cascade that revokes no row still emits one cross_tenant_read, with a row
// count of zero, because the query ran under the all-tenants context.
//
// spec: §13.3 (Platform-admin impersonation, rule 7), §12.3
func TestUserTokenRevokerCascadeEmitsOnEmptyResult_spec_13_3(t *testing.T) {
	store := newCascadeStore()
	reads := &fakeCrossTenantReads{}
	u := &userTokenRevoker{store: store, reads: reads}
	jtis, err := u.RevokeUserTokens(cascadeCtx(), "platform", "erin@acme.com", "user_invalidated", time.Now(), true)
	if err != nil {
		t.Fatalf("RevokeUserTokens: %v", err)
	}
	if got := sortedJTIs(jtis); got != "own-1,own-2" {
		t.Errorf("jtis = %s, want only the user's own tokens", got)
	}
	if len(reads.events) != 1 || reads.events[0].RowCount != 0 {
		t.Errorf("events = %+v, want one event with RowCount 0", reads.events)
	}
}

// TestUserTokenRevokerPartialResults_spec_11_4 asserts that every committed
// revocation's JTIs survive another step's failure, and that the returned
// error carries each failure.
//
// spec: §11.4 step 5, §13.3 (Platform-admin impersonation, rule 6)
func TestUserTokenRevokerPartialResults_spec_11_4(t *testing.T) {
	subjectErr := errors.New("subject revoke failed")
	actorErr := errors.New("actor revoke failed")
	emitErr := errors.New("audit store unreachable")
	cases := []struct {
		name       string
		subjectErr error
		actorErr   error
		emitErr    error
		wantJTIs   string
		wantErrs   []error
		wantRows   int
	}{
		{
			name:     "cascade fails after the subject revoke commits",
			actorErr: actorErr,
			wantJTIs: "own-1,own-2",
			wantErrs: []error{actorErr},
		},
		{
			name:       "subject revoke fails and the cascade commits",
			subjectErr: subjectErr,
			wantJTIs:   "imp-acme,imp-globex",
			wantErrs:   []error{subjectErr},
			wantRows:   2,
		},
		{
			name:     "emit fails after the cascade commits",
			emitErr:  emitErr,
			wantJTIs: "imp-acme,imp-globex,own-1,own-2",
			wantErrs: []error{emitErr},
			wantRows: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newCascadeStore()
			store.subjectErr, store.actorErr = tc.subjectErr, tc.actorErr
			reads := &fakeCrossTenantReads{err: tc.emitErr}
			u := &userTokenRevoker{store: store, reads: reads}
			jtis, err := u.RevokeUserTokens(cascadeCtx(), "platform", "dave@acme.com", "user_invalidated", time.Now(), true)
			if got := sortedJTIs(jtis); got != tc.wantJTIs {
				t.Errorf("jtis = %s, want %s", got, tc.wantJTIs)
			}
			for _, want := range tc.wantErrs {
				if !errors.Is(err, want) {
					t.Errorf("err = %v, want it to wrap %v", err, want)
				}
			}
			if store.actorCalls != 1 {
				t.Errorf("cascade ran %d times, want 1 whatever the subject revoke returned", store.actorCalls)
			}
			if len(reads.events) != 1 || reads.events[0].RowCount != tc.wantRows {
				t.Errorf("events = %+v, want one event with RowCount %d", reads.events, tc.wantRows)
			}
		})
	}
}

// TestUserTokenRevokerSkipsCascadeWithoutPlatformAdmin_spec_13_3 asserts
// that when the caller does not hold platform-admin no all-tenants query
// runs and no cross_tenant_read is emitted.
//
// spec: §13.3 (Platform-admin impersonation, rule 6)
func TestUserTokenRevokerSkipsCascadeWithoutPlatformAdmin_spec_13_3(t *testing.T) {
	store := newCascadeStore()
	reads := &fakeCrossTenantReads{}
	u := &userTokenRevoker{store: store, reads: reads}
	jtis, err := u.RevokeUserTokens(cascadeCtx(), "platform", "dave@acme.com", "user_invalidated", time.Now(), false)
	if err != nil {
		t.Fatalf("RevokeUserTokens: %v", err)
	}
	if got := sortedJTIs(jtis); got != "own-1,own-2" {
		t.Errorf("jtis = %s, want only the user's own tokens", got)
	}
	if store.actorCalls != 0 || len(reads.events) != 0 {
		t.Errorf("cascade calls = %d, events = %d; want neither", store.actorCalls, len(reads.events))
	}
}

// TestUserTokenRevokerNilEmitterFailsClosed_spec_13_3 asserts that a
// revoker with no cross_tenant_read emitter runs no all-tenants query,
// still returns the user's own tokens, and reports the missing emitter.
//
// spec: §13.3 (Platform-admin impersonation, rule 7), §12.3
func TestUserTokenRevokerNilEmitterFailsClosed_spec_13_3(t *testing.T) {
	store := newCascadeStore()
	u := &userTokenRevoker{store: store}
	jtis, err := u.RevokeUserTokens(cascadeCtx(), "platform", "dave@acme.com", "user_invalidated", time.Now(), true)
	if !errors.Is(err, errNoCrossTenantReadEmitter) {
		t.Errorf("err = %v, want errNoCrossTenantReadEmitter", err)
	}
	if store.actorCalls != 0 {
		t.Errorf("cascade ran %d times without an emitter, want 0", store.actorCalls)
	}
	if got := sortedJTIs(jtis); got != "own-1,own-2" {
		t.Errorf("jtis = %s, want the user's own tokens", got)
	}
}

// TestUserTokenRevokerEmitsAfterTheCascade_spec_13_3 asserts that the
// cross_tenant_read event follows the cascade query, so its row count is the
// count the query committed.
//
// spec: §13.3 (Platform-admin impersonation, rule 7)
func TestUserTokenRevokerEmitsAfterTheCascade_spec_13_3(t *testing.T) {
	var log []string
	store := newCascadeStore()
	store.log = &log
	u := &userTokenRevoker{store: store, reads: &fakeCrossTenantReads{log: &log}}
	if _, err := u.RevokeUserTokens(cascadeCtx(), "platform", "dave@acme.com", "user_invalidated", time.Now(), true); err != nil {
		t.Fatalf("RevokeUserTokens: %v", err)
	}
	if strings.Join(log, ",") != "subject,actor,emit" {
		t.Errorf("order = %v, want subject, actor, emit", log)
	}
}
