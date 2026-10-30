// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
)

// unreportedStore is a session store that does not report its post-terminal
// cap.
type unreportedStore struct{ sessionstore.Store }

// spec: §7.1 (Post-terminal window), §5.2 (Post-recovery rehydration
// atomicity)
//
// Every store the harness builds counts the post-terminal window under the
// cap the invariant checker applies. A Postgres store built without the cap,
// directly or behind the harness's stepped-clock and trace decorators, would
// treat a terminal row with no release marker as released while the checker
// counts it as holding its pod, so the harness refuses it.
func TestCheckStoreCapRefusesAStoreOffTheHarnessCap_spec_7_1(t *testing.T) {
	now := func() time.Time { return time.Now().UTC() }
	cases := []struct {
		name    string
		store   sessionstore.Store
		wantErr string
	}{
		{"default memstore", NewMemStore(now), ""},
		{"capped Postgres store", CappedPostgresStore(nil), ""},
		{"capped Postgres store with a read pool", CappedPostgresStore(nil, sessionpg.WithReadPool(nil)), ""},
		{"capped Postgres store behind the decorators", NewTracedStore(PostgresStoreOn(nil)(now), NewTrace()), ""},
		{"uncapped Postgres store", sessionpg.New(nil), "cap 0s"},
		{"uncapped Postgres store behind the stepped clock", NewSteppedClockStore(sessionpg.New(nil), now), "cap 0s"},
		{"memstore on another cap", memstore.NewWithClock(now, memstore.WithPostTerminalCap(PostTerminalCap+time.Second)), "under cap"},
		{"store that reports no cap", unreportedStore{NewMemStore(now)}, "does not report"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckStoreCap(tc.store)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("CheckStoreCap = %v, want nil", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("CheckStoreCap = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

// spec: §7.1 (Post-terminal window)
//
// The capped Postgres constructor applies the harness cap after the caller's
// options, so an option that sets a cap of its own cannot leave the store on
// a cap the checker does not apply.
func TestCappedPostgresStoreAppliesTheHarnessCapLast_spec_7_1(t *testing.T) {
	st := CappedPostgresStore(nil, sessionpg.WithPostTerminalCap(time.Nanosecond))
	if got := st.PostTerminalCap(); got != PostTerminalCap {
		t.Fatalf("cap = %v, want the harness cap %v", got, PostTerminalCap)
	}
}
