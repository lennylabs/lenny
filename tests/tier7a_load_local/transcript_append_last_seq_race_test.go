// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a coverage for the transcript store's Append return value under
// concurrent writers to one session, over the in-memory store and over the
// Postgres store. An Open Responses turn records Append's return value as its
// response's end sequence while a REST delivery may append to the same
// session at the same moment, so each call's returned Seq must name exactly
// the entries that call wrote.
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	transcriptpg "github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// appendPosture is one store posture: a transcript store and a function that
// returns a fresh (tenant, session) pair the store accepts appends for.
type appendPosture struct {
	store      transcriptstore.Store
	newSession func(t *testing.T) (tenant, sessionID string)
}

func memAppendPosture() appendPosture {
	return appendPosture{
		store: transcriptstore.NewMemory(),
		newSession: func(*testing.T) (string, string) {
			return "t-" + uuid.NewString()[:8], uuid.NewString()
		},
	}
}

func pgAppendPosture(t *testing.T) appendPosture {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	sessions := sessionpg.New(pg.Pool)
	return appendPosture{
		store: transcriptpg.New(pg.Pool),
		newSession: func(t *testing.T) (string, string) {
			t.Helper()
			ctx := context.Background()
			tenant := "t-" + uuid.NewString()[:8]
			if _, err := pg.Pool.Exec(ctx,
				`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, tenant, []byte{0x01}); err != nil {
				t.Fatalf("seed tenant: %v", err)
			}
			id := uuid.NewString()
			if err := sessions.Create(ctx, sessionstore.Session{
				ID: id, TenantID: tenant, State: session.StateRunning, RuntimeRef: "echo",
			}); err != nil {
				t.Fatalf("seed session: %v", err)
			}
			return tenant, id
		},
	}
}

// appendCall is one concurrent Append: its writer index, batch size, and the
// Seq it returned.
type appendCall struct {
	writer  int
	size    int
	lastSeq uint64
	err     error
}

// runConcurrentAppendRound races writers Append calls of random batch sizes
// on one session and checks the round's invariants.
func runConcurrentAppendRound(t *testing.T, rng *rand.Rand, p appendPosture, writers int) {
	t.Helper()
	ctx := context.Background()
	tenant, sid := p.newSession(t)
	calls := make([]appendCall, writers)
	delays := make([]time.Duration, writers)
	for i := range calls {
		calls[i] = appendCall{writer: i, size: 1 + rng.Intn(4)}
		delays[i] = time.Duration(rng.Intn(1500)) * time.Microsecond
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range calls {
		wg.Add(1)
		go func(c *appendCall, delay time.Duration) {
			defer wg.Done()
			<-start
			time.Sleep(delay)
			entries := make([]transcriptstore.Entry, c.size)
			for j := range entries {
				entries[j] = transcriptstore.Entry{Role: "user", Content: fmt.Sprintf("w%d-e%d", c.writer, j)}
			}
			c.lastSeq, c.err = p.store.Append(ctx, tenant, sid, entries...)
		}(&calls[i], delays[i])
	}
	close(start)
	wg.Wait()
	stored, err := p.store.Get(ctx, tenant, sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	checkAppendInvariants(t, calls, stored)
}

// checkAppendInvariants asserts that every call succeeded, that the returned
// last-seqs are distinct and their ranges disjoint and gap-free over the
// stored transcript, and that each call's returned Seq equals the highest Seq
// among the entries that call wrote, with its entries contiguous and in the
// order given.
func checkAppendInvariants(t *testing.T, calls []appendCall, stored []transcriptstore.Entry) {
	t.Helper()
	seqsByWriter := map[int][]uint64{}
	for _, e := range stored {
		var w, j int
		if _, err := fmt.Sscanf(e.Content, "w%d-e%d", &w, &j); err != nil {
			t.Fatalf("unparseable entry %q: %v", e.Content, err)
		}
		if j != len(seqsByWriter[w]) {
			t.Fatalf("writer %d entries out of order at Seq %d (%q)", w, e.Seq, e.Content)
		}
		seqsByWriter[w] = append(seqsByWriter[w], e.Seq)
	}
	total := 0
	sorted := append([]appendCall(nil), calls...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].lastSeq < sorted[b].lastSeq })
	var prev uint64
	for _, c := range sorted {
		if c.err != nil {
			t.Fatalf("writer %d Append: %v", c.writer, c.err)
		}
		if c.lastSeq <= prev {
			t.Fatalf("writer %d returned %d, not above the previous call's %d: last-seqs not strictly ordered", c.writer, c.lastSeq, prev)
		}
		if first := c.lastSeq - uint64(c.size) + 1; first != prev+1 {
			t.Fatalf("writer %d range [%d,%d] does not start right after %d: ranges overlap or leave a gap", c.writer, first, c.lastSeq, prev)
		}
		seqs := seqsByWriter[c.writer]
		if len(seqs) != c.size {
			t.Fatalf("writer %d stored %d entries, wrote %d", c.writer, len(seqs), c.size)
		}
		for j, s := range seqs {
			if want := c.lastSeq - uint64(c.size-1-j); s != want {
				t.Fatalf("writer %d entry %d has Seq %d, want %d (returned last Seq %d)", c.writer, j, s, want, c.lastSeq)
			}
		}
		prev = c.lastSeq
		total += c.size
	}
	if len(stored) != total || prev != uint64(total) {
		t.Fatalf("stored %d entries with highest returned Seq %d, writers wrote %d", len(stored), prev, total)
	}
}

// TestTranscriptAppendConcurrentLastSeq_spec_15 runs randomized interleavings,
// under -race, of concurrent Append calls of random batch sizes to one session
// over each store posture. Every call's returned last Seq is distinct, the
// ranges the calls claim are disjoint and cover the stored transcript without
// a gap, and each returned Seq equals the highest Seq among that call's own
// entries.
//
// spec: §15.1 (REST API), §15 (Open Responses conversation sessions)
// diagnosis: a failure means Append's return value does not name the entries
// that call wrote when another writer appends to the same session
// concurrently: the store reads the end Seq outside the per-session
// serialization (a Get or Page after the write), or the Postgres store returns
// a value computed before its transaction committed. An Open Responses
// response would then record an end sequence that includes another writer's
// entries or excludes its own.
func TestTranscriptAppendConcurrentLastSeq_spec_15(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	postures := []struct {
		name    string
		posture func(t *testing.T) appendPosture
		rounds  int
		writers int
	}{
		{"memory", func(*testing.T) appendPosture { return memAppendPosture() }, 60, 24},
		{"pgstore", pgAppendPosture, 10, 12},
	}
	for _, pc := range postures {
		t.Run(pc.name, func(t *testing.T) {
			p := pc.posture(t)
			for r := 0; r < pc.rounds; r++ {
				t.Run(fmt.Sprintf("round-%d", r), func(t *testing.T) {
					runConcurrentAppendRound(t, rng, p, pc.writers)
				})
			}
		})
	}
}
