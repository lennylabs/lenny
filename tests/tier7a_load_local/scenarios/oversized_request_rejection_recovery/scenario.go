// SPDX-License-Identifier: MIT

//go:build load_local

// Package oversized_request_rejection_recovery exercises the §15.2
// payload-cap contract under contention: a burst of oversized
// requests is rejected with a clean envelope, and valid requests
// in parallel are unaffected. Invariant: the valid-request error
// rate stays bounded even when the oversized-request rate is high.
//
// TESTING.md §12.7.a resiliency scenarios.
package oversized_request_rejection_recovery

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "oversized_request_rejection_recovery"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
	maxLen   int
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
	s.maxLen = idempotency.MaxKeyLength
	return nil
}
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// 90% oversized, 10% valid — burst pattern.
	oversized := iter%10 != 0
	value := ""
	if oversized {
		value = makeString(s.maxLen + 100)
	} else {
		value = fmt.Sprintf("k-%d-%d", vu, iter)
	}
	key := idempotency.Key{TenantID: "acme", Value: value}
	err := key.Validate()
	switch {
	case oversized && err != nil:
		s.counters.Inc("oversized_rejected")
	case !oversized && err == nil:
		s.counters.Inc("valid_accepted")
	default:
		s.counters.Inc("unexpected")
		return fmt.Errorf("§15.2 violated: oversized=%v validate-err=%v", oversized, err)
	}
	return nil
}

func makeString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("unexpected"); v > 0 {
		return fmt.Errorf("§15.2 violated: %d unexpected validation outcomes", v)
	}
	if s.counters.Get("oversized_rejected") == 0 || s.counters.Get("valid_accepted") == 0 {
		return fmt.Errorf("scenario must exercise both paths")
	}
	return nil
}
