// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a coverage for the replay transcript copy against the owner's
// erasure on the in-memory stores, where the transcripts are erased by the
// session-scoped eraser rather than by a foreign-key cascade. Concurrent
// prompt_history replays of a user's sessions, each admitted before the
// erasure's flag, race the flag write, the drain, and the erasure
// orchestrator's DeleteByUser.
package tier7a_load_local_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
)

// replayRaceStores is the memory posture's session, tenant, user, and
// transcript stores, wired as cmd/lenny-gateway wires them, with the copier
// wrapped to record how many entries each replay copied.
type replayRaceStores struct {
	sessions    *memstore.Store
	users       *userstore.Memory
	transcripts *transcriptstore.Memory

	mu     sync.Mutex
	copied map[string]int // destination session ID → entries copied
}

func newReplayRaceStores(t *testing.T, tenant string) *replayRaceStores {
	t.Helper()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: tenant}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	s := &replayRaceStores{users: userstore.NewMemory(), transcripts: transcriptstore.NewMemory(), copied: map[string]int{}}
	seedFenceUsers(t, s.users, tenant)
	copier := func(ctx context.Context, tenantID, src, dst string) error {
		if err := s.transcripts.CopySession(ctx, tenantID, src, dst); err != nil {
			return err
		}
		got, err := s.transcripts.Get(ctx, tenantID, dst)
		if err != nil && !errors.Is(err, transcriptstore.ErrNotFound) {
			return err
		}
		s.mu.Lock()
		s.copied[dst] = len(got)
		s.mu.Unlock()
		return nil
	}
	s.sessions = memstore.New(
		memstore.WithCreationMarkers(sessionstore.StoreMarkers(tenants, s.users)),
		memstore.WithTranscriptCopier(copier),
	)
	return s
}

// orchestrator is the memory posture's erasure over these stores: a lister
// over the user's session rows from the primary and the IDs reserved for
// the user, the session-scoped transcripts eraser, and the sessions
// user-scoped eraser.
func (s *replayRaceStores) orchestrator() *erasure.Orchestrator {
	return erasure.New(erasure.Config{
		Sessions: func(ctx context.Context, tenantID, userID string) ([]string, error) {
			rows, err := s.sessions.List(ctx, tenantID, sessionstore.ListFilter{UserID: userID, Primary: true})
			if err != nil {
				return nil, err
			}
			ids, err := s.sessions.ReservedSessionIDs(ctx, tenantID, userID)
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				ids = append(ids, r.ID)
			}
			return ids, nil
		},
		SessionScoped: []erasure.SessionEraser{{Name: "transcripts", DeleteBySession: s.transcripts.DeleteBySession}},
		UserScoped:    []erasure.Eraser{{Name: "sessions", DeleteByUser: s.sessions.DeleteByUser}},
	})
}

const replaySourceEntries = 4

func (s *replayRaceStores) seedSources(t *testing.T, tenant string, n int) []sessionstore.Session {
	t.Helper()
	ctx := context.Background()
	out := make([]sessionstore.Session, n)
	for i := range out {
		src := sessionstore.Session{ID: uuid.NewString(), TenantID: tenant, UserID: "alice", RuntimeRef: "echo", State: session.StateCompleted}
		if err := s.sessions.Create(ctx, src); err != nil {
			t.Fatalf("seed source: %v", err)
		}
		for j := 0; j < replaySourceEntries; j++ {
			if _, err := s.transcripts.Append(ctx, tenant, src.ID, transcriptstore.Entry{Role: "user", Content: fmt.Sprintf("m%d", j)}); err != nil {
				t.Fatalf("seed transcript: %v", err)
			}
		}
		out[i] = src
	}
	return out
}

// runReplayErasureRound races replays of alice's sessions against her
// erasure and checks the round's invariants.
func runReplayErasureRound(t *testing.T, rng *rand.Rand, replays int) {
	t.Helper()
	ctx := context.Background()
	tenant := "t-" + uuid.NewString()[:8]
	s := newReplayRaceStores(t, tenant)
	sources := s.seedSources(t, tenant, 3)
	type outcome struct {
		id  string
		err error
	}
	outcomes := make([]outcome, replays)
	delays := make([]time.Duration, replays)
	picks := make([]int, replays)
	for i := range delays {
		delays[i] = time.Duration(rng.Intn(2000)) * time.Microsecond
		picks[i] = rng.Intn(len(sources))
	}
	erasureDelay := time.Duration(rng.Intn(2000)) * time.Microsecond

	// The replays' request-time checks and their ID reservations ran
	// before the flag, as the replay handler reserves the ID before it
	// copies the source snapshot; only the inserts race the erasure.
	rows := make([]sessionstore.Session, replays)
	for i := range rows {
		src := sources[picks[i]]
		rows[i] = sessionstore.Session{
			ID: uuid.NewString(), TenantID: tenant, UserID: src.UserID,
			ParentSessionID: src.ID, RuntimeRef: "echo", State: session.StateCreated,
		}
		if err := s.sessions.Reserve(ctx, tenant, rows[i].UserID, rows[i].ID, false); err != nil {
			t.Fatalf("reserve replay ID: %v", err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < replays; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			src := sources[picks[i]]
			replay := rows[i]
			time.Sleep(delays[i])
			outcomes[i] = outcome{id: replay.ID, err: s.sessions.CreateWithTranscript(ctx, replay, src.ID)}
		}(i)
	}

	time.Sleep(erasureDelay)
	if _, err := s.users.Update(ctx, tenant, "alice", func(u *userstore.User) error {
		u.ProcessingRestricted, u.ErasureJobID = true, "job-1"
		return nil
	}); err != nil {
		t.Fatalf("flag write: %v", err)
	}
	if _, err := admin.DrainSessions(ctx, s.sessions, storeTerminator{s.sessions}, nil, tenant, "alice"); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if _, err := s.orchestrator().DeleteByUser(ctx, tenant, "alice"); err != nil {
		t.Fatalf("erasure: %v", err)
	}
	wg.Wait()

	for _, o := range outcomes {
		if entries, err := s.transcripts.Get(ctx, tenant, o.id); !errors.Is(err, transcriptstore.ErrNotFound) {
			t.Fatalf("replay %s: %d transcript entries outlived the erasure (err %v)", o.id, len(entries), err)
		}
		s.mu.Lock()
		n, copiedAny := s.copied[o.id]
		s.mu.Unlock()
		var ref *sessionstore.CreationRefusal
		switch {
		case o.err == nil:
			if !copiedAny || n != replaySourceEntries {
				t.Fatalf("committed replay %s copied %d entries, want the source's full %d", o.id, n, replaySourceEntries)
			}
		case errors.As(o.err, &ref):
			if copiedAny {
				t.Fatalf("refused replay %s wrote %d entries", o.id, n)
			}
			if _, err := s.sessions.Get(ctx, tenant, o.id); !errors.Is(err, sessionstore.ErrNotFound) {
				t.Fatalf("refused replay %s left a row (%v)", o.id, err)
			}
		default:
			t.Fatalf("replay %s: unexpected error %v", o.id, o.err)
		}
	}
	for _, src := range sources {
		if _, err := s.transcripts.Get(ctx, tenant, src.ID); !errors.Is(err, transcriptstore.ErrNotFound) {
			t.Fatalf("source %s transcript outlived the erasure", src.ID)
		}
	}
}

// TestReplayTranscriptCopyRaceAgainstErasure_spec_12_8 runs randomized
// interleavings, under -race, of concurrent prompt_history replays of a
// user's sessions against the user's erasure (flag write, drain, and
// DeleteByUser through the erasure orchestrator with the transcripts
// session-scoped eraser). After DeleteByUser returns and every replay has
// finished, no transcript entry remains under any replayed ID, and each
// replay either committed a row with the source's full transcript or wrote
// nothing.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.8 (Processing restriction during erasure (GDPR Article 18)), 15.1 (REST API)
// diagnosis: a failure means a replay's transcript copy is not atomic with
// its fenced insert in the memory posture: a copy can land after the
// transcripts eraser ran, or without its session row, so transcript entries
// of an erased user survive the erasure.
func TestReplayTranscriptCopyRaceAgainstErasure_spec_12_8(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	for r := 0; r < 60; r++ {
		t.Run(fmt.Sprintf("round-%d", r), func(t *testing.T) {
			runReplayErasureRound(t, rng, 16)
		})
	}
}
