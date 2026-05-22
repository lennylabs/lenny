// SPDX-License-Identifier: MIT

//go:build load_local

// Package audit_chain_concurrent exercises pkg/audit.Chain from many
// goroutines appending to per-tenant chains. The §11.7 invariant:
// the hash chain remains intact under concurrent Append, sequence
// numbers are monotone within a tenant, and Verify reports
// ChainVerified.
//
// TESTING.md §12.7.a regression scenarios.
package audit_chain_concurrent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "audit_chain_concurrent"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

type Scenario struct {
	counters *scenkit.Counters
	chainSet *audit.ChainSet
	tenants  []string
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.chainSet = audit.NewChainSet()
	s.tenants = []string{"acme", "globex", "initech"}
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	tenant := s.tenants[vu%len(s.tenants)]
	payload, _ := json.Marshal(map[string]int{"vu": vu, "iter": iter})
	row := s.chainSet.Append(tenant, "session.created", payload, time.Now())
	if row.Seq == 0 {
		return fmt.Errorf("append returned zero seq")
	}
	s.counters.Inc("appended")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	for _, t := range s.tenants {
		chain := s.chainSet.Chain(t)
		if chain == nil {
			return fmt.Errorf("chain missing post-run: %s", t)
		}
		if v := chain.Verify(); v.Integrity != audit.ChainVerified {
			return fmt.Errorf("§11.7 violated: tenant=%s integrity=%s", t, v.Integrity)
		}
		for i, row := range chain.Rows() {
			if row.Seq != uint64(i+1) {
				return fmt.Errorf("§11.7 violated: tenant=%s row[%d].Seq=%d want %d", t, i, row.Seq, i+1)
			}
		}
	}
	return nil
}
