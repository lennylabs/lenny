// SPDX-License-Identifier: MIT

//go:build load_local

// Package quota_decrement_race exercises pkg/quota.HierarchicalCheck
// under many goroutines per tenant. The §11.2 invariant: global,
// tenant, and user-level checks are evaluated in order and the
// least-allowing state wins.
//
// TESTING.md §12.7.a regression scenarios.
package quota_decrement_race

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/quota"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "quota_decrement_race"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters  *scenkit.Counters
	hierarchy quota.Hierarchy
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 32, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.hierarchy = quota.Hierarchy{Global: 10_000, Tenant: 1_000, User: 100}
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	globalUsed := int64((vu + iter) % int(s.hierarchy.Global*2))
	tenantUsed := int64((vu*7 + iter) % int(s.hierarchy.Tenant*2))
	userUsed := int64((vu*13 + iter) % int(s.hierarchy.User*2))
	result := quota.HierarchicalCheck(globalUsed, tenantUsed, userUsed, s.hierarchy)
	switch result.State {
	case quota.StateHardExceeded:
		s.counters.Inc("exceeded")
	case quota.StateSoftWarning:
		s.counters.Inc("warned")
	case quota.StateOK:
		s.counters.Inc("ok")
	default:
		return fmt.Errorf("unknown state %q", result.State)
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if s.counters.Get("ok") == 0 || s.counters.Get("exceeded") == 0 {
		return fmt.Errorf("scenario must exercise both ok and exceeded paths")
	}
	return nil
}
