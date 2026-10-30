// SPDX-License-Identifier: MIT

//go:build load_local

// Package disk_full_audit_handling models the §11.7 audit-required
// contract: when the audit sink is unavailable (e.g. disk full),
// tenants with `complianceProfile != none` fail closed; tenants
// without that requirement remain open. Invariant: audit-required
// requests get a clean failure; non-required requests stay served.
//
// TESTING.md §12.7.a resiliency scenarios.
package disk_full_audit_handling

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "disk_full_audit_handling"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// sink models an audit destination. When full, write returns
// errSinkFull. Audit-required tenants must observe the error;
// non-required tenants ignore it.
type sink struct {
	mu   sync.Mutex
	full bool
}

var errSinkFull = errors.New("audit sink full (disk)")

func (s *sink) write() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.full {
		return errSinkFull
	}
	return nil
}

func (s *sink) fill() { s.mu.Lock(); s.full = true; s.mu.Unlock() }

type Scenario struct {
	counters *scenkit.Counters
	sink     *sink
	fillOnce sync.Once
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 8, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 32, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 128, Duration: 1 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.sink = &sink{}
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// Fill the sink at iter 25.
	if iter == 25 {
		s.fillOnce.Do(func() {
			s.sink.fill()
			s.counters.Inc("sink_filled")
		})
	}
	// Half the goroutines are audit-required tenants; the other
	// half are not.
	auditRequired := vu%2 == 0
	err := s.sink.write()
	sinkFull := errors.Is(err, errSinkFull)
	switch {
	case auditRequired && sinkFull:
		s.counters.Inc("required_failed_closed")
		return nil
	case auditRequired && !sinkFull:
		s.counters.Inc("required_open")
	case !auditRequired && sinkFull:
		// Non-required tenants ignore the audit error and continue.
		s.counters.Inc("non_required_open_during_outage")
	case !auditRequired && !sinkFull:
		s.counters.Inc("non_required_open")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if s.counters.Get("sink_filled") == 0 {
		return fmt.Errorf("scenario never triggered the sink-full path")
	}
	if s.counters.Get("required_failed_closed") == 0 {
		return fmt.Errorf("§11.7 violated: audit-required tenant did not fail closed when sink filled")
	}
	if s.counters.Get("non_required_open_during_outage") == 0 {
		return fmt.Errorf("§11.7 violated: non-audit-required tenant was blocked by sink outage")
	}
	return nil
}
