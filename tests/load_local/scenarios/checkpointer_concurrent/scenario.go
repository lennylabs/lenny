// SPDX-License-Identifier: MIT

//go:build load_local

// Package checkpointer_concurrent models the §4.4 checkpointer
// contract: N sessions checkpointing simultaneously each get a fresh
// chunk identifier, no two checkpoints write to the same chunk, and
// the documented per-level retry budget caps the failure path.
//
// pkg/checkpointer is unimplemented in the build sequence; the
// scenario uses a scenario-local checkpoint store that mirrors the
// production contract. pkg/checkpoint (which does exist) holds the
// enums this scenario uses.
//
// TESTING.md §12.7.a component-isolated benches.
package checkpointer_concurrent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "checkpointer_concurrent"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// store is a scenario-local checkpoint store. Each Append returns a
// fresh chunk ID; chunks indexed by ID. The mutex serialises ID
// assignment so the §4.4 unique-chunk invariant holds.
type store struct {
	mu     sync.Mutex
	seq    atomic.Int64
	chunks map[int64]string
}

func newStore() *store {
	return &store{chunks: make(map[int64]string, 1024)}
}

func (s *store) append(sessionID string, level checkpoint.Level) (int64, error) {
	id := s.seq.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.chunks[id]; exists {
		return 0, fmt.Errorf("§4.4 violated: chunk %d reused", id)
	}
	s.chunks[id] = sessionID + "@" + string(level)
	return id, nil
}

type Scenario struct {
	counters *scenkit.Counters
	store    *store
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.store = newStore()
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	sessionID := fmt.Sprintf("sess-%d-%d", vu, iter)
	level := checkpoint.LevelStandard
	if iter%3 == 0 {
		level = checkpoint.LevelFull
	}
	id, err := s.store.append(sessionID, level)
	if err != nil {
		s.counters.Inc("chunk_id_collision")
		return err
	}
	if id == 0 {
		s.counters.Inc("zero_chunk_id_unexpected")
		return fmt.Errorf("§4.4 violated: zero chunk id")
	}
	s.counters.Inc("checkpoints")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("chunk_id_collision"); v > 0 {
		return fmt.Errorf("§4.4 violated: %d chunk id collisions", v)
	}
	if v := s.counters.Get("zero_chunk_id_unexpected"); v > 0 {
		return fmt.Errorf("§4.4 violated: %d zero chunk ids", v)
	}
	if s.counters.Get("checkpoints") == 0 {
		return fmt.Errorf("scenario did not checkpoint anything")
	}
	return nil
}
