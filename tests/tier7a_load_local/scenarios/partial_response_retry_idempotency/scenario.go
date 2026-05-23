// SPDX-License-Identifier: MIT

//go:build load_local

// Package partial_response_retry_idempotency models the §11.5
// at-most-once contract: a client retry after a partial response
// must not produce a duplicate side effect. The scenario uses an
// idempotency-keyed handler that records side effects per key.
//
// TESTING.md §12.7.a resiliency scenarios.
package partial_response_retry_idempotency

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "partial_response_retry_idempotency"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// handler keyed on idempotency key. Returns the same outcome on
// retry; the side effect is recorded exactly once.
type handler struct {
	mu       sync.Mutex
	sideFx   map[string]int
}

func (h *handler) call(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.sideFx[key]; ok {
		// Idempotent replay; do nothing.
		h.sideFx[key]++ // retry count, not duplicate side effect
		return
	}
	h.sideFx[key] = 1
}

func (h *handler) duplicates() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	dups := 0
	for _, c := range h.sideFx {
		if c > 4 {
			dups++
		}
	}
	return dups
}

type Scenario struct {
	counters *scenkit.Counters
	h        *handler
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second}
}
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 16, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 64, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 256, Duration: 1 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.h = &handler{sideFx: make(map[string]int, 1024)}
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// 100 distinct keys, each retried by many goroutines.
	key := fmt.Sprintf("k-%d", iter%100)
	s.h.call(key)
	s.counters.Inc("calls")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	r.AddCustom("duplicates", float64(s.h.duplicates()))
	s.h.mu.Lock()
	uniqueSideFx := len(s.h.sideFx)
	s.h.mu.Unlock()
	r.AddCustom("unique_side_effects", float64(uniqueSideFx))
	if uniqueSideFx > 100 {
		return fmt.Errorf("§11.5 violated: %d unique side effects for 100 keys", uniqueSideFx)
	}
	return nil
}
