// SPDX-License-Identifier: MIT

//go:build load_local

// Package sessionstore_write_amplification observes the write rate
// pkg/gateway/sessionstore in-memory backend produces under N
// concurrent SetStatus calls across many sessions. The §15.1
// invariant: SetStatus is idempotent on the same status value and
// does not amplify writes when callers repeat the same transition.
//
// TESTING.md §12.7.a component-isolated benches.
package sessionstore_write_amplification

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "sessionstore_write_amplification"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type fakeSessionStore struct {
	mu sync.RWMutex
	m  map[string]string
}

func (f *fakeSessionStore) setStatus(id, to string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m[id] == to {
		return false
	}
	f.m[id] = to
	return true
}

type Scenario struct {
	counters *scenkit.Counters
	store    *fakeSessionStore
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.store = &fakeSessionStore{m: make(map[string]string, 1000)}
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	id := fmt.Sprintf("s-%d", iter%100)
	if s.store.setStatus(id, "running") {
		s.counters.Inc("transitions")
	}
	s.counters.Inc("calls")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("transitions"); v > 100 {
		return fmt.Errorf("§15.1 violated: more than 100 transitions for 100 sessions = %d", v)
	}
	if s.counters.Get("calls") < 1000 {
		return fmt.Errorf("scenario did not get enough load: calls=%d", s.counters.Get("calls"))
	}
	return nil
}
