// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a coverage for the fenced insert of a derive-failure row. Concurrent
// inserts at the generation their derive admitted race coordination
// generation bumps of the source session in randomized interleavings under
// -race. An insert that starts after a bump has returned must be refused,
// and every insert either writes its row or writes nothing, so no failed row
// lands at a generation the source has already left.
//
// spec: §7.1 (Normal Flow).
package tier7a_load_local_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// fenceRounds is the number of randomized rounds the fenced-insert race runs.
const fenceRounds = 200

// fenceInsert is one racing insert's view of the race: whether a bump had
// returned before it started, and its outcome.
type fenceInsert struct {
	id         string
	afterBump  bool
	err        error
	generation int64
}

// spec: §7.1 (Normal Flow)
// diagnosis: a derive-failure row was inserted at a source generation that a
// coordination generation bump had already advanced: an insert that started
// after the bump returned wrote its row, a refused insert left a row, or an
// insert failed with an error other than the generation refusal. The insert
// must read the source generation inside its own write, as the Postgres
// store does under FOR SHARE, rather than trust the generation the derive
// admitted.
func TestDeriveFailureInsertRacesGenerationBumps_spec_7_1(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	ctx := context.Background()
	outcomes := map[string]int{}
	for round := 0; round < fenceRounds; round++ {
		s := memstore.New()
		const admitted = int64(1)
		if err := s.Create(ctx, sessionstore.Session{ID: "src", TenantID: "acme", State: session.StateRunning, CoordinationGeneration: admitted}); err != nil {
			t.Fatal(err)
		}
		inserts := make([]*fenceInsert, 1+rng.Intn(4))
		bumps := 1 + rng.Intn(2)
		var bumped atomic.Bool
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range inserts {
			in := &fenceInsert{id: fmt.Sprintf("d%d", i), generation: admitted}
			inserts[i] = in
			jitter := time.Duration(rng.Intn(200)) * time.Microsecond
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				time.Sleep(jitter)
				in.afterBump = bumped.Load()
				row := sessionstore.Session{ID: in.id, TenantID: "acme", State: session.StateFailed, ParentSessionID: "src"}
				in.err = s.CreateIfGeneration(ctx, row, "acme", "src", in.generation)
			}()
		}
		for b := 0; b < bumps; b++ {
			jitter := time.Duration(rng.Intn(200)) * time.Microsecond
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				time.Sleep(jitter)
				if _, err := s.Update(ctx, "acme", "src", func(r *sessionstore.Session) error {
					r.CoordinationGeneration++
					return nil
				}); err != nil {
					t.Errorf("round %d: bump: %v", round, err)
				}
				bumped.Store(true)
			}()
		}
		close(start)
		wg.Wait()
		for _, in := range inserts {
			_, getErr := s.Get(ctx, "acme", in.id)
			switch {
			case in.err == nil && in.afterBump:
				t.Fatalf("round %d: insert %s started after a bump returned and wrote its row at the stale generation %d", round, in.id, in.generation)
			case in.err == nil:
				if getErr != nil {
					t.Fatalf("round %d: admitted insert %s left no row: %v", round, in.id, getErr)
				}
				outcomes["inserted before the bump"]++
			case errors.Is(in.err, sessionstore.ErrGenerationAdvanced):
				if getErr == nil {
					t.Fatalf("round %d: refused insert %s left a row", round, in.id)
				}
				outcomes["refused after the bump"]++
			default:
				t.Fatalf("round %d: insert %s = %v, want nil or ErrGenerationAdvanced", round, in.id, in.err)
			}
		}
	}
	t.Logf("outcomes: %v", outcomes)
}
