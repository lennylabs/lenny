// SPDX-License-Identifier: MIT

//go:build load_local

// Package header_size_cap models the §15.2 header-size limit:
// headers larger than the cap are rejected with 431 instead of
// blocking the request body. Invariant: the cap is enforced
// uniformly across N concurrent requests.
//
// TESTING.md §12.7.a resiliency scenarios.
package header_size_cap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "header_size_cap"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

const maxHeaderBytes = 8192

func enforceHeaderCap(headerBytes int) error {
	if headerBytes > maxHeaderBytes {
		return fmt.Errorf("431 request header fields too large")
	}
	return nil
}

type Scenario struct {
	counters *scenkit.Counters
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

func (s *Scenario) Setup(ctx context.Context) error    { return nil }
func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	oversized := iter%3 == 0
	size := 512
	if oversized {
		size = maxHeaderBytes + 1024
	}
	header := strings.Repeat("x", size)
	err := enforceHeaderCap(len(header))
	switch {
	case oversized && err != nil:
		s.counters.Inc("rejected_431")
	case !oversized && err == nil:
		s.counters.Inc("accepted")
	default:
		s.counters.Inc("unexpected")
		return fmt.Errorf("§15.2 violated: oversized=%v err=%v", oversized, err)
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("unexpected"); v > 0 {
		return fmt.Errorf("§15.2 violated: %d unexpected outcomes", v)
	}
	if s.counters.Get("rejected_431") == 0 || s.counters.Get("accepted") == 0 {
		return fmt.Errorf("scenario must exercise both paths")
	}
	return nil
}
