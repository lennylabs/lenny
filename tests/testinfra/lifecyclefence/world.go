// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// Users is the set of users every World seeds in its tenant.
var Users = []string{"alice", "bob"}

// sourceEntries is the transcript length of each seeded replay source.
const sourceEntries = 3

// World is one tenant of a posture, with its users, the fakes for the
// object store and the checkpoint manifest, the admin router, and the
// scenario's recorded outcomes and violations.
type World struct {
	S      *Stores
	Tenant string
	// Objects stands for the blob store that holds derive snapshot copies
	// and delegated-child file exports, keyed by session ID.
	Objects *KeyedSet
	// Manifests stands for the checkpoint_manifest rows a chunked derive
	// writes under the derived session ID.
	Manifests *KeyedSet
	// Faults injects store errors into the drain's listing and forced
	// termination.
	Faults *Faults
	// Audit records the admin router's audit events.
	Audit *AuditSink

	// Lister is the drain's listing seam: the session store behind Faults.
	Lister *FaultySessions
	// Terminator is the drain's forced terminal transition behind Faults.
	Terminator *Terminator
	// Logger writes through the scenario test's t.Log. The erasure runners
	// and the tenant-deletion reconcilers log through it, so a run's verbose
	// output records each drain's elapsed time and any Phase 2 pause.
	Logger *slog.Logger

	seq atomic.Int64

	adminOnce sync.Once
	adminH    http.Handler

	mu         sync.Mutex
	sources    map[string]string // user → completed source session ID
	outcomes   []CreateOutcome
	violations []string
	// erasedAt is, per user, the sequence number taken when the user's
	// erasure wrote its completion receipt, just before Complete cleared
	// the restriction. A reservation or insert whose sequence number is
	// lower committed before the clear and is in the erasure's reach.
	erasedAt map[string]int64
	// suspendTransitions counts the suspension-marker writes that moved
	// the marker from false to true.
	suspendTransitions int
}

// NewWorld seeds a fresh tenant with Users, one completed replay source per
// user with a transcript, and liveSeeds running sessions per user.
func NewWorld(t testing.TB, s *Stores, liveSeeds int) *World {
	t.Helper()
	ctx := context.Background()
	w := &World{
		S:         s,
		Tenant:    "t-" + uuid.NewString()[:8],
		Objects:   NewKeyedSet(),
		Manifests: NewKeyedSet(),
		Faults:    &Faults{},
		Audit:     &AuditSink{},
		Logger:    newTestLogger(t),
		sources:   map[string]string{},
		erasedAt:  map[string]int64{},
	}
	w.Lister = &FaultySessions{Store: s.Sessions, faults: w.Faults}
	w.Terminator = &Terminator{store: s.Sessions, faults: w.Faults}
	if err := s.seedTenant(ctx, w.Tenant); err != nil {
		t.Fatalf("seed tenant %s: %v", w.Tenant, err)
	}
	for _, u := range Users {
		if err := s.Users.Create(ctx, userstore.User{TenantID: w.Tenant, Subject: u}); err != nil {
			t.Fatalf("seed user %s: %v", u, err)
		}
		w.seedSource(t, u)
		for i := 0; i < liveSeeds; i++ {
			w.SeedLive(t, u)
		}
	}
	return w
}

// seedSource writes a completed session of user with a transcript, which
// the replay path copies from.
func (w *World) seedSource(t testing.TB, user string) {
	t.Helper()
	ctx := context.Background()
	src := sessionstore.Session{ID: uuid.NewString(), TenantID: w.Tenant, UserID: user, RuntimeRef: "echo", State: session.StateCompleted}
	if err := w.S.Sessions.Create(ctx, src); err != nil {
		t.Fatalf("seed source of %s: %v", user, err)
	}
	for j := 0; j < sourceEntries; j++ {
		if err := w.S.Transcripts.Append(ctx, w.Tenant, src.ID, transcriptstore.Entry{Role: "user", Content: fmt.Sprintf("m%d", j)}); err != nil {
			t.Fatalf("seed transcript of %s: %v", user, err)
		}
	}
	w.sources[user] = src.ID
}

// SeedLive inserts one running session of user and returns its ID.
func (w *World) SeedLive(t testing.TB, user string) string {
	t.Helper()
	row := sessionstore.Session{ID: uuid.NewString(), TenantID: w.Tenant, UserID: user, RuntimeRef: "echo", State: session.StateRunning}
	if err := w.S.Sessions.Create(context.Background(), row); err != nil {
		t.Fatalf("seed live session of %s: %v", user, err)
	}
	return row.ID
}

// next returns the World's next sequence number. Sequence numbers order the
// commits the checker compares, such as a reservation against the erasure
// that clears its owner's restriction.
func (w *World) next() int64 { return w.seq.Add(1) }

// Violatef records an invariant violation found inside a concurrent
// goroutine. Check reports it.
func (w *World) Violatef(format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.violations = append(w.violations, fmt.Sprintf(format, args...))
}

// LiveInScope returns the non-terminal sessions of the scope, read from the
// primary without faults: the whole tenant when user is empty.
func (w *World) LiveInScope(ctx context.Context, user string) ([]string, error) {
	rows, err := w.S.Sessions.List(ctx, w.Tenant, sessionstore.ListFilter{UserID: user, Primary: true})
	if err != nil {
		return nil, fmt.Errorf("list scope of tenant %s user %q: %w", w.Tenant, user, err)
	}
	var live []string
	for _, r := range rows {
		if !session.IsTerminal(r.State) {
			live = append(live, r.ID)
		}
	}
	return live, nil
}

// assertScopeDrained records a violation when the scope holds a
// non-terminal session at the moment named by when.
func (w *World) assertScopeDrained(ctx context.Context, user, when string) {
	live, err := w.LiveInScope(ctx, user)
	if err != nil {
		w.Violatef("%s: %v", when, err)
		return
	}
	if len(live) > 0 {
		w.Violatef("%s: scope (user %q) still holds non-terminal sessions %v", when, user, live)
	}
}

// Drain runs the shared lifecycle drain over the scope through the World's
// fault seams.
func (w *World) Drain(ctx context.Context, user string) (admin.DrainResult, error) {
	return admin.DrainSessions(ctx, w.Lister, w.Terminator, w.Tenant, user)
}

// TerminalWriter ends a random admitted session the way a session's own
// terminal pipeline does: a locked update that leaves a terminal row as it
// is and moves a live one to completed.
func (w *World) TerminalWriter(ctx context.Context, pick func(n int) int) {
	w.mu.Lock()
	n := len(w.outcomes)
	var id string
	if n > 0 {
		o := w.outcomes[pick(n)]
		if o.Err == nil {
			id = o.ID
		}
	}
	w.mu.Unlock()
	if id == "" {
		return
	}
	_, _ = w.S.Sessions.Update(ctx, w.Tenant, id, func(r *sessionstore.Session) error {
		if !session.IsTerminal(r.State) {
			r.State = session.StateCompleted
		}
		return nil
	})
}

// Sleep pauses for a random number of microseconds below max, so
// concurrent goroutines interleave differently in each round.
func Sleep(pick func(n int) int, maxMicros int) {
	if maxMicros > 0 {
		time.Sleep(time.Duration(pick(maxMicros)) * time.Microsecond)
	}
}
