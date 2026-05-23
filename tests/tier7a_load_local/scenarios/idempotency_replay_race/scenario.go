// SPDX-License-Identifier: MIT

//go:build load_local

// Package idempotency_replay_race exercises pkg/idempotency under
// many concurrent goroutines replaying the same key. The §11.5
// invariant: a non-terminal stored Record always returns Replay,
// a body-mismatch always returns Reject, and the action is
// deterministic for identical inputs.
//
// TESTING.md §12.7.a regression scenarios.
package idempotency_replay_race

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "idempotency_replay_race"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
	body     []byte
	hash     string
	record   idempotency.Record
}

func (s *Scenario) Name() string { return name }
// RampProfiles enumerates ascending VU counts for capacity discovery
// under LENNY_TIER7A_CAPACITY=1.
func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 64, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 128, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 256, Duration: 2 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 512, Duration: 2 * time.Second},
	}
}

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.body = []byte(`{"sessionId":"acme-alice-1"}`)
	s.hash = idempotency.HashBody(s.body)
	s.record = idempotency.Record{
		Key:      idempotency.Key{TenantID: "acme", Value: "k-1"},
		BodyHash: s.hash,
		StoredAt: time.Now(),
		Response: idempotency.Response{
			StatusCode: 201,
			Body:       []byte(`{"id":"sess-1"}`),
		},
	}
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	inbound := s.hash
	if (vu+iter)%2 != 0 {
		inbound = idempotency.HashBody([]byte(`{"sessionId":"acme-bob-DRIFT"}`))
	}
	action, err := idempotency.DetectReuse(s.record, inbound, time.Now())
	s.counters.Inc("hits")
	switch {
	case action == idempotency.ActionReplay && err == nil:
		s.counters.Inc("replays")
	case action == idempotency.ActionReject:
		s.counters.Inc("rejects")
	default:
		s.counters.Inc("unexpected")
		return fmt.Errorf("unexpected outcome action=%v err=%v", action, err)
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("unexpected"); v > 0 {
		return fmt.Errorf("§11.5 violated: %d unexpected outcomes", v)
	}
	if s.counters.Get("replays") == 0 || s.counters.Get("rejects") == 0 {
		return fmt.Errorf("scenario did not exercise both paths")
	}
	return nil
}
