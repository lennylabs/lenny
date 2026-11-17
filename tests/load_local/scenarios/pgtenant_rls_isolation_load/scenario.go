// SPDX-License-Identifier: MIT

//go:build load_local

// Package pgtenant_rls_isolation_load models the §12.4 RLS contract:
// concurrent multi-tenant writes go through a tenant-pinned session
// context, and reads must never observe rows from another tenant.
// The scenario uses a scenario-local row-level-security model that
// mirrors the production Postgres RLS policy (tenant_id is the
// session GUC; rows whose tenant_id does not match the session GUC
// are invisible).
//
// TESTING.md §12.7.a component-isolated benches.
package pgtenant_rls_isolation_load

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "pgtenant_rls_isolation_load"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// rlsTable is a scenario-local model of a Postgres table with RLS.
// Every row carries a tenant_id; selectAs(tenant) returns only rows
// whose tenant_id matches.
type rlsTable struct {
	mu   sync.RWMutex
	rows []row
}

type row struct {
	tenantID string
	id       string
	body     string
}

func (t *rlsTable) insert(tenant, id, body string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rows = append(t.rows, row{tenantID: tenant, id: id, body: body})
}

func (t *rlsTable) selectAs(tenant string) []row {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := []row{}
	for _, r := range t.rows {
		if r.tenantID == tenant {
			out = append(out, r)
		}
	}
	return out
}

type Scenario struct {
	counters *scenkit.Counters
	table    *rlsTable
	tenants  []string
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.table = &rlsTable{}
	s.tenants = []string{"acme", "globex", "initech"}
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	tenant := s.tenants[vu%len(s.tenants)]
	id := fmt.Sprintf("%s-%d-%d", tenant, vu, iter)
	s.table.insert(tenant, id, "x")
	s.counters.Inc("inserts")

	// Read back from the same tenant — must include the just-written
	// row. Read from a different tenant — must not include it.
	mine := s.table.selectAs(tenant)
	foundOwn := false
	for _, r := range mine {
		if r.id == id {
			foundOwn = true
			break
		}
	}
	if !foundOwn {
		s.counters.Inc("own_invisible_unexpected")
		return fmt.Errorf("§12.4 violated: own row invisible after insert")
	}

	other := s.tenants[(vu+1)%len(s.tenants)]
	cross := s.table.selectAs(other)
	for _, r := range cross {
		if r.id == id {
			s.counters.Inc("cross_visible_unexpected")
			return fmt.Errorf("§12.4 violated: tenant %q sees row inserted by tenant %q", other, tenant)
		}
	}
	s.counters.Inc("isolation_holds")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("cross_visible_unexpected"); v > 0 {
		return fmt.Errorf("§12.4 violated: %d cross-tenant row leaks", v)
	}
	if v := s.counters.Get("own_invisible_unexpected"); v > 0 {
		return fmt.Errorf("§12.4 violated: %d own-tenant rows invisible after insert", v)
	}
	if s.counters.Get("isolation_holds") == 0 {
		return fmt.Errorf("scenario did not exercise the isolation path")
	}
	return nil
}
