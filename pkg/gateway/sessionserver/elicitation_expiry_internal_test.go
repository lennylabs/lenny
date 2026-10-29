// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// failingExpiry is an interaction store whose ExpireRaisedBy fails.
type failingExpiry struct {
	interactionstore.Store
	calls int
}

func (f *failingExpiry) ExpireRaisedBy(context.Context, string, string) (int, error) {
	f.calls++
	return 0, errors.New("postgres down")
}

func seedRaised(t *testing.T, store interactionstore.Store, sessionID string) {
	t.Helper()
	if err := store.Put(context.Background(), interactionstore.Interaction{
		ID: "elic-1", Kind: interactionstore.KindElicitation, SessionID: sessionID, TenantID: "acme",
		UserID: "alice", Phase: interactionstore.PhasePending,
		Detail: map[string]any{interactionstore.DetailOriginPod: sessionID},
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
}

// TestPostTerminalHookExpiresRaisedElicitations_spec_9_2 pins that the
// post-terminal hook expires the elicitations the session's pod raised, and
// that an expiry error is logged without stopping the hook.
//
// spec: 9.2 (Elicitation Chain), 7.2 (Interactive Session Model)
func TestPostTerminalHookExpiresRaisedElicitations_spec_9_2(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	row := sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateCompleted, CreatedAt: now, UpdatedAt: now}

	t.Run("expires", func(t *testing.T) {
		store := memstore.New()
		if err := store.Create(context.Background(), row); err != nil {
			t.Fatal(err)
		}
		inter := interactionstore.NewMemory()
		seedRaised(t, inter, "s1")
		s := New(store, Options{DevTenantHeader: true, Interactions: inter})
		defer s.Close()
		s.OnSessionTerminal(context.Background(), session.StateRunning, row)
		if err := s.WaitTerminalWork(context.Background()); err != nil {
			t.Fatalf("WaitTerminalWork: %v", err)
		}
		if _, err := inter.Get(context.Background(), "acme", "s1", "alice", "elic-1"); !errors.Is(err, interactionstore.ErrNotFound) {
			t.Fatalf("Get after the terminal hook = %v, want ErrNotFound", err)
		}
	})
	t.Run("error_does_not_stop_the_hook", func(t *testing.T) {
		store := memstore.New()
		if err := store.Create(context.Background(), row); err != nil {
			t.Fatal(err)
		}
		inter := &failingExpiry{Store: interactionstore.NewMemory()}
		s := New(store, Options{DevTenantHeader: true, Interactions: inter})
		defer s.Close()
		s.OnSessionTerminal(context.Background(), session.StateRunning, row)
		if err := s.WaitTerminalWork(context.Background()); err != nil {
			t.Fatalf("WaitTerminalWork: %v", err)
		}
		if inter.calls != 1 {
			t.Fatalf("ExpireRaisedBy calls = %d, want 1", inter.calls)
		}
	})
}

// TestSuspensionReleaseExpiresRaisedElicitations_spec_9_2 pins that the
// suspension release expires the raised elicitations after the pod release,
// and that a checkpoint failure leaves them answerable.
//
// spec: 9.2 (Elicitation Chain), 6.2 (Pod State Machine)
func TestSuspensionReleaseExpiresRaisedElicitations_spec_9_2(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cpErr      error
		wantExpiry bool
	}{
		{"released", nil, true},
		{"checkpoint_failed", errors.New("checkpoint failed"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newSuspensionRig(t, true)
			inter := interactionstore.NewMemory()
			r.srv.interactions = inter
			seedRaised(t, inter, "s1")
			row := r.seedSuspended(t, "s1", r.now.Add(-time.Hour))
			r.cp.err = tc.cpErr
			r.srv.ReleaseSuspendedPod(context.Background(), row)
			r.wait(t, "s1")
			_, err := inter.Get(context.Background(), "acme", "s1", "alice", "elic-1")
			if expired := errors.Is(err, interactionstore.ErrNotFound); expired != tc.wantExpiry {
				t.Fatalf("expired = %v (err %v), want %v", expired, err, tc.wantExpiry)
			}
		})
	}
}
