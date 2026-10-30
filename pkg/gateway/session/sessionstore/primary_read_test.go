// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// updateOnlyStore hides the memstore's GetPrimary, so ReadPrimary takes its
// read-only Update fallback.
type updateOnlyStore struct{ sessionstore.Store }

// blockingPrimary is a primary reader that ignores its context and blocks
// until release closes.
type blockingPrimary struct {
	sessionstore.Store
	release chan struct{}
}

func (b *blockingPrimary) GetPrimary(context.Context, string, string) (sessionstore.Session, error) {
	<-b.release
	return sessionstore.Session{}, nil
}

func seedPrimaryRow(t *testing.T, st sessionstore.Store) sessionstore.Session {
	t.Helper()
	if err := st.Create(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning,
		CoordinationGeneration: 3,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	row, err := st.Get(context.Background(), "acme", "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return row
}

// TestReadPrimaryReturnsTheStoredRowWithoutWriting_spec_12_3 asserts that
// ReadPrimary returns the stored row and writes nothing (no UpdatedAt or
// accumulated-age change), through a store's GetPrimary and through the
// read-only Update fallback, and that it returns ErrNotFound for a missing row
// and for another tenant's row.
//
// spec: §12.3 (Postgres HA Requirements)
func TestReadPrimaryReturnsTheStoredRowWithoutWriting_spec_12_3(t *testing.T) {
	for _, tc := range []struct {
		name string
		wrap func(sessionstore.Store) sessionstore.Store
	}{
		{"GetPrimary", func(s sessionstore.Store) sessionstore.Store { return s }},
		{"Update fallback", func(s sessionstore.Store) sessionstore.Store { return updateOnlyStore{s} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := memstore.New()
			before := seedPrimaryRow(t, inner)
			st := tc.wrap(inner)
			time.Sleep(2 * time.Millisecond)
			got, err := sessionstore.ReadPrimary(context.Background(), st, 0, "acme", "s1")
			if err != nil {
				t.Fatalf("ReadPrimary: %v", err)
			}
			if got.ID != "s1" || got.State != session.StateRunning || got.CoordinationGeneration != 3 {
				t.Errorf("row = %+v, want the stored running row at generation 3", got)
			}
			after, _ := inner.Get(context.Background(), "acme", "s1")
			if !after.UpdatedAt.Equal(before.UpdatedAt) || after.AccumulatedSessionAgeSeconds != before.AccumulatedSessionAgeSeconds {
				t.Errorf("row after the read: updated %v age %v, want %v / %v unchanged",
					after.UpdatedAt, after.AccumulatedSessionAgeSeconds, before.UpdatedAt, before.AccumulatedSessionAgeSeconds)
			}
			for _, key := range [][2]string{{"acme", "missing"}, {"globex", "s1"}} {
				if _, err := sessionstore.ReadPrimary(context.Background(), st, 0, key[0], key[1]); !errors.Is(err, sessionstore.ErrNotFound) {
					t.Errorf("ReadPrimary(%s/%s) = %v, want ErrNotFound", key[0], key[1], err)
				}
			}
		})
	}
}

// TestReadPrimaryIsBounded_spec_12_3 asserts that ReadPrimary returns
// context.DeadlineExceeded within its timeout when the store ignores its
// context, applies DefaultPrimaryReadTimeout to a zero timeout, and honors an
// earlier caller deadline.
//
// spec: §12.3 (Postgres HA Requirements)
func TestReadPrimaryIsBounded_spec_12_3(t *testing.T) {
	if sessionstore.DefaultPrimaryReadTimeout != 5*time.Second {
		t.Fatalf("DefaultPrimaryReadTimeout = %v, want 5s", sessionstore.DefaultPrimaryReadTimeout)
	}
	t.Run("own timeout", func(t *testing.T) {
		st := &blockingPrimary{Store: memstore.New(), release: make(chan struct{})}
		defer close(st.release)
		begin := time.Now()
		_, err := sessionstore.ReadPrimary(context.Background(), st, 30*time.Millisecond, "acme", "s1")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
		if el := time.Since(begin); el > time.Second {
			t.Errorf("returned after %v, want about 30ms", el)
		}
	})
	t.Run("earlier caller deadline", func(t *testing.T) {
		st := &blockingPrimary{Store: memstore.New(), release: make(chan struct{})}
		defer close(st.release)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		begin := time.Now()
		_, err := sessionstore.ReadPrimary(ctx, st, time.Hour, "acme", "s1")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
		if el := time.Since(begin); el > time.Second {
			t.Errorf("returned after %v, want the caller's 20ms deadline", el)
		}
	})
	t.Run("zero timeout uses the default", func(t *testing.T) {
		st := &blockingPrimary{Store: memstore.New(), release: make(chan struct{})}
		defer close(st.release)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := sessionstore.ReadPrimary(ctx, st, 0, "acme", "s1")
			done <- err
		}()
		select {
		case err := <-done:
			t.Fatalf("a zero timeout returned at once (%v); want the default bound", err)
		case <-time.After(50 * time.Millisecond):
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want Canceled", err)
		}
	})
}

// TestMarkTerminatedRecordsTheTerminatedFact_spec_7_2 asserts that
// MarkTerminated stamps TerminatedAt and the state's reason once, keeps the
// stamp on a later write, rewrites the reason on a relabel to failed, leaves a
// non-terminal row unchanged, and advances CoordinationGeneration by one for
// every terminal state written from resuming and for no other source state.
//
// spec: §7.2 (Interactive Session Model), §8.8 (TaskRecord and TaskResult Schema)
func TestMarkTerminatedRecordsTheTerminatedFact_spec_7_2(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	reasons := map[session.State]string{
		session.StateCompleted: "Completed",
		session.StateFailed:    "Failed",
		session.StateCancelled: "Cancelled",
		session.StateExpired:   "Expired",
	}
	for _, from := range session.AllStates() {
		for _, to := range session.AllStates() {
			r := sessionstore.Session{State: to, CoordinationGeneration: 4, RecoveryGeneration: 2}
			sessionstore.MarkTerminated(&r, from, now)
			want, terminal := reasons[to]
			if !terminal {
				if !r.TerminatedAt.IsZero() || r.TerminatedReason != "" || r.CoordinationGeneration != 4 {
					t.Errorf("%s->%s: non-terminal row changed: %+v", from, to, r)
				}
				continue
			}
			if !r.TerminatedAt.Equal(now) || r.TerminatedReason != want {
				t.Errorf("%s->%s: stamp = %v/%q, want %v/%q", from, to, r.TerminatedAt, r.TerminatedReason, now, want)
			}
			wantGen := int64(4)
			if from == session.StateResuming {
				wantGen = 5
			}
			if r.CoordinationGeneration != wantGen || r.RecoveryGeneration != 2 {
				t.Errorf("%s->%s: generations = %d/%d, want %d/2", from, to, r.CoordinationGeneration, r.RecoveryGeneration, wantGen)
			}
		}
	}

	r := sessionstore.Session{State: session.StateCompleted}
	sessionstore.MarkTerminated(&r, session.StateRunning, now)
	r.State = session.StateFailed
	sessionstore.MarkTerminated(&r, session.StateCompleted, now.Add(time.Hour))
	if !r.TerminatedAt.Equal(now) || r.TerminatedReason != "Failed" {
		t.Errorf("relabel = %v/%q, want the first stamp %v kept with reason Failed", r.TerminatedAt, r.TerminatedReason, now)
	}
}
