// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §7.2 (Interactive Session Model)
//
// A hold fault parks the matched store call in its callback before the call
// runs, so a write another actor makes while the call is held lands first,
// and the held call then runs against the current row. Only the first
// matching call is held.
func TestHoldFaultParksTheCallUntilTheCallbackReturns_spec_7_2(t *testing.T) {
	ctx := context.Background()
	st := NewTracedStore(memstore.New(), NewTrace())
	if err := st.Create(ctx, sessionstore.Session{ID: "s1", TenantID: Tenant, State: session.StateReady}); err != nil {
		t.Fatal(err)
	}
	held, release := make(chan struct{}), make(chan struct{})
	st.trace.ArmMatching("Update", "", FaultHold, func(TraceEntry, FaultKind) {
		close(held)
		<-release
	})
	done := make(chan session.State, 1)
	go func() {
		row, err := st.Update(ctx, Tenant, "s1", func(r *sessionstore.Session) error { return nil })
		if err != nil {
			t.Errorf("held update: %v", err)
		}
		done <- row.State
	}()
	<-held
	// The held call has not run, so a second matching call is not held and
	// lands first.
	if _, err := st.Update(ctx, Tenant, "s1", func(r *sessionstore.Session) error {
		r.State = session.StateCancelled
		return nil
	}); err != nil {
		t.Fatalf("racing update: %v", err)
	}
	select {
	case <-done:
		t.Fatal("the held call ran before its callback returned")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if got := <-done; got != session.StateCancelled {
		t.Errorf("held call read state %s, want the racing write's cancelled", got)
	}
}
