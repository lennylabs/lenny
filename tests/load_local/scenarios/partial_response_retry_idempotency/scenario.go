// SPDX-License-Identifier: MIT

//go:build load_local

// Package partial_response_retry_idempotency asserts the §11.5
// idempotency invariant: under concurrent replays of the same key,
// the operation is executed at most once and every other request
// either replays the cached response or is rejected with
// IDEMPOTENCY_KEY_REUSED. The scenario drives pkg/idempotency
// (Key.Validate, HashBody, DetectReuse) against an in-memory record
// store and counts per-key side-effects under load.
//
// TESTING.md §12.7.a resiliency scenarios.
package partial_response_retry_idempotency

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "partial_response_retry_idempotency"

// keyspace is the number of distinct idempotency keys the scenario
// cycles through. Many concurrent VUs target the same key so the
// at-most-once invariant gets stressed.
const keyspace = 100

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// store is the in-memory Postgres stand-in: per-key records keyed
// (tenant, value). A real gateway uses Postgres with row-level locks.
// The scenario's correctness check does not depend on the storage
// choice — it depends on DetectReuse + the executor mutex below.
type store struct {
	mu      sync.Mutex
	records map[string]idempotency.Record
	// sideEffects counts how many times each key actually executed
	// its operation. The §11.5 contract requires this to stay at 1
	// per key regardless of concurrent replays.
	sideEffects map[string]int
}

func newStore() *store {
	return &store{records: map[string]idempotency.Record{}, sideEffects: map[string]int{}}
}

// process runs the §11.5 admission logic for one inbound request.
// Returns the action taken, plus whether this call performed the
// side-effect (true for the single execution that wins the race).
func (s *store) process(k idempotency.Key, bodyHash string, body []byte, now time.Time) (idempotency.Action, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.records[k.TenantID+"|"+k.Value]
	action, err := idempotency.DetectReuse(stored, bodyHash, now)
	if err != nil {
		return action, false, err
	}
	switch action {
	case idempotency.ActionStoreNew:
		s.records[k.TenantID+"|"+k.Value] = idempotency.Record{
			Key:      k,
			BodyHash: bodyHash,
			Response: idempotency.Response{StatusCode: 200, Body: body},
			StoredAt: now,
		}
		s.sideEffects[k.Value]++
		return action, true, nil
	case idempotency.ActionReplay:
		return action, false, nil
	default:
		return action, false, errors.New("unexpected action")
	}
}

type Scenario struct {
	counters *scenkit.Counters
	store    *store
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 16, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 64, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 256, Duration: 1 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.store = newStore()
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	keyIdx := iter % keyspace
	k := idempotency.Key{TenantID: "acme", Value: fmt.Sprintf("req-%d", keyIdx)}
	if err := k.Validate(); err != nil {
		s.counters.Inc("validation_failed")
		return nil
	}
	// Body is identical for every replay of this key — the §11.5
	// "same body, same key" replay path. A separate scenario can
	// cover the different-body rejection path.
	body := []byte(fmt.Sprintf("payload for key-%d", keyIdx))
	bodyHash := idempotency.HashBody(body)

	action, executed, err := s.store.process(k, bodyHash, body, time.Now())
	switch {
	case err != nil:
		s.counters.Inc("rejected_unexpected")
	case action == idempotency.ActionStoreNew && executed:
		s.counters.Inc("executed")
	case action == idempotency.ActionReplay:
		s.counters.Inc("replayed")
	default:
		s.counters.Inc("unclassified")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	executed := s.counters.Get("executed")
	replayed := s.counters.Get("replayed")
	if executed == 0 || replayed == 0 {
		return fmt.Errorf("scenario must exercise both store-new and replay paths (executed=%d replayed=%d)", executed, replayed)
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	for keyValue, count := range s.store.sideEffects {
		if count != 1 {
			return fmt.Errorf("§11.5 violated: key %q executed %d times (want exactly 1)", keyValue, count)
		}
	}
	if got, want := len(s.store.sideEffects), keyspace; got != want {
		return fmt.Errorf("scenario did not exercise the full keyspace: %d/%d keys saw a side effect", got, want)
	}
	return nil
}
