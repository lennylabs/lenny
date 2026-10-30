// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// Users is the set of users every World seeds in its tenant. Scenarios erase
// alice, run the raced suspend's create as bob, and full-revoke carol, so the
// full_revoke tombstone never refuses a create another operation needs.
var Users = []string{"alice", "bob", "carol"}

// sourceEntries is the transcript length of each seeded replay source.
const sourceEntries = 3

// World is one tenant of a posture, with its users, the fakes for the
// object store and the checkpoint manifest, the admin router, and the
// scenario's recorded outcomes and violations.
type World struct {
	S      *Stores
	Tenant string
	// Objects is the blob store that holds each source's checkpoint chunk,
	// derive chunk copies, and delegated-child file exports.
	Objects *Objects
	// Manifests is the checkpoint_manifest store that holds each source's
	// chunked checkpoint and the manifest rows a derive writes under the
	// derived session ID.
	Manifests *Manifests
	// Faults injects store errors into the drain's listing and forced
	// termination.
	Faults *Faults
	// Audit records the admin router's audit events.
	Audit *AuditSink
	// Leases is the credential lease pool. Each seeded live session holds
	// one lease. A twin World shares it.
	Leases *Leases
	// Tokens is the TokenStore. Each user holds tokensPerUser rows. A twin
	// World shares it.
	Tokens *Tokens

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

	mu       sync.Mutex
	sources  map[string]string // user → completed source session ID
	outcomes []CreateOutcome
	// parentCommits is the commit sequence number of each delegation
	// parent the delegate path created.
	parentCommits map[string]int64
	violations    []string
	// erasedAt is, per user, the sequence number taken when the user's
	// erasure wrote its completion receipt, just before Complete cleared
	// the restriction. A reservation or insert whose sequence number is
	// lower committed before the clear and is in the erasure's reach.
	erasedAt map[string]int64
	// suspendTransitions counts the suspension-marker writes that moved
	// the marker from false to true.
	suspendTransitions int
	// racedSuspends holds each raced suspend's observation for Check.
	racedSuspends []racedObservation
	// seededLive lists, per user, the live sessions SeedLive inserted.
	seededLive map[string][]string
	// deletion records how far the tenant's deletion has got.
	deletion deletionProgress
	// twin is the other tenant of a pair, and twinBaseline its residue
	// when the pair was built. Check asserts the twin is unchanged.
	twin         *World
	twinBaseline Residue
	// userCommitHooks holds, per user, the hook to run right after that
	// user's row next commits.
	userCommitHooks map[string]func()
	// userBaseline is each user's snapshot when the World was built, which
	// Check compares a bystander against.
	userBaseline map[string]userSnapshot
	// touched lists the users an erasure or a full_revoke targeted.
	touched map[string]bool
}

// tokensPerUser is the number of TokenStore rows each seeded user holds.
const tokensPerUser = 2

// shared is the backing state two Worlds of a pair share: the blob store,
// the checkpoint manifests, the lease pool, and the TokenStore, each keyed
// by tenant as the production stores are.
type shared struct {
	objects   *Objects
	manifests *Manifests
	leases    *Leases
	tokens    *Tokens
}

// NewWorld seeds a fresh tenant with Users, one completed replay source per
// user with a transcript, liveSeeds running sessions per user each holding
// one credential lease, and tokensPerUser TokenStore rows per user.
func NewWorld(t testing.TB, s *Stores, liveSeeds int) *World {
	t.Helper()
	return newWorld(t, s, liveSeeds, shared{objects: NewObjects(), manifests: NewManifests(), leases: NewLeases(), tokens: NewTokens()})
}

// NewTwin seeds a second tenant over the same stores, sharing the World's
// blob store, manifests, lease pool, and TokenStore, and records its
// residue. Check then asserts that nothing the World's operations do
// changes the twin. The twin takes no operation of its own.
func (w *World) NewTwin(t testing.TB, liveSeeds int) *World {
	t.Helper()
	tw := newWorld(t, w.S, liveSeeds, shared{objects: w.Objects, manifests: w.Manifests, leases: w.Leases, tokens: w.Tokens})
	base, err := tw.Residue(context.Background())
	if err != nil {
		t.Fatalf("twin residue: %v", err)
	}
	w.twin, w.twinBaseline = tw, base
	return tw
}

func newWorld(t testing.TB, s *Stores, liveSeeds int, sh shared) *World {
	t.Helper()
	ctx := context.Background()
	w := &World{
		S:          s,
		Tenant:     "t-" + uuid.NewString()[:8],
		Objects:    sh.objects,
		Manifests:  sh.manifests,
		Leases:     sh.leases,
		Tokens:     sh.tokens,
		Faults:     &Faults{},
		Audit:      &AuditSink{},
		Logger:     newTestLogger(t),
		sources:    map[string]string{},
		erasedAt:   map[string]int64{},
		seededLive: map[string][]string{},

		parentCommits: map[string]int64{},
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
		w.Tokens.Seed(w.Tenant, u, tokensPerUser)
		for i := 0; i < liveSeeds; i++ {
			w.SeedLive(t, u)
		}
	}
	w.snapshotUsers(ctx)
	return w
}

// seedSource writes a completed session of user with a transcript, which
// the replay path copies from, and a one-chunk checkpoint, which the derive
// path copies from.
func (w *World) seedSource(t testing.TB, user string) {
	t.Helper()
	ctx := context.Background()
	src := sessionstore.Session{
		ID: uuid.NewString(), TenantID: w.Tenant, UserID: user, RuntimeRef: "echo", State: session.StateCompleted,
		IsolationProfile: isolation.ProfileSandboxed,
	}
	src.WorkspaceSnapshot = w.seedCheckpoint(t, src.ID)
	if err := w.S.Sessions.Create(ctx, src); err != nil {
		t.Fatalf("seed source of %s: %v", user, err)
	}
	for j := 0; j < sourceEntries; j++ {
		if _, err := w.S.Transcripts.Append(ctx, w.Tenant, src.ID, transcriptstore.Entry{Role: "user", Content: fmt.Sprintf("m%d", j)}); err != nil {
			t.Fatalf("seed transcript of %s: %v", user, err)
		}
	}
	w.sources[user] = src.ID
}

// seedCheckpoint writes a complete one-chunk checkpoint of the source
// session and returns the snapshot reference that names it.
func (w *World) seedCheckpoint(t testing.TB, sourceID string) *sessionstore.WorkspaceSnapshot {
	t.Helper()
	ckpt := uuid.NewString()
	enc := partialmanifeststore.ChunkEncodingTar
	chunk := resumechunks.ChunkObjectURI(w.Tenant, sourceID, ckpt, 0, enc)
	chunk.TTL = time.Hour
	if _, err := w.Objects.Put(chunk, "application/x-tar", strings.NewReader("chunk-0")); err != nil {
		t.Fatalf("seed checkpoint chunk of %s: %v", sourceID, err)
	}
	w.Manifests.seedParent(partialmanifeststore.Record{
		TenantID: w.Tenant, CheckpointID: ckpt, SessionID: sourceID, ChunkCount: 1, ChunkEncoding: enc,
	})
	return &sessionstore.WorkspaceSnapshot{Ref: fmt.Sprintf("/%s/%s/%s/%s", w.Tenant, blobstore.ObjectTypeCheckpoint, sourceID, ckpt)}
}

// SeedLive inserts one running session of user holding one credential
// lease and returns its ID.
func (w *World) SeedLive(t testing.TB, user string) string {
	t.Helper()
	row := sessionstore.Session{ID: uuid.NewString(), TenantID: w.Tenant, UserID: user, RuntimeRef: "echo", State: session.StateRunning}
	if err := w.S.Sessions.Create(context.Background(), row); err != nil {
		t.Fatalf("seed live session of %s: %v", user, err)
	}
	w.Leases.Grant(w.Tenant, user, row.ID)
	w.mu.Lock()
	w.seededLive[user] = append(w.seededLive[user], row.ID)
	w.mu.Unlock()
	return row.ID
}

// SeededLive returns the live sessions SeedLive inserted for user.
func (w *World) SeededLive(user string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.seededLive[user]...)
}

// LeaseRenewer renews one random held lease of any tenant sharing the
// World's pool, as the LLM proxy renews the leases of running sessions.
func (w *World) LeaseRenewer(pick func(n int) int) { w.Leases.RenewRandom(pick) }

// sessionGone reports whether the session's row is absent from the store.
func (w *World) sessionGone(ctx context.Context, id string) bool {
	_, err := w.S.Sessions.Get(ctx, w.Tenant, id)
	return errors.Is(err, sessionstore.ErrNotFound)
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
	return admin.DrainSessions(ctx, w.Lister, w.Terminator, nil, w.Tenant, user)
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
