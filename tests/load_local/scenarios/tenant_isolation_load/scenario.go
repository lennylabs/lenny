// SPDX-License-Identifier: MIT

//go:build load_local

// Package tenant_isolation_load drives two tenants concurrently
// against the inproc gateway. The §11.2 invariant: every tenant's
// sessions complete within the same per-tenant latency budget when
// the other tenant generates pressure.
//
// TESTING.md §12.7.a multi-component scenarios.
package tenant_isolation_load

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/inproc"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "tenant_isolation_load"

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{} })
}

type Scenario struct {
	scenkit.InProcMixin
	counters *scenkit.Counters

	mu        sync.Mutex
	acmeLat   []float64
	globexLat []float64
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 16, Duration: 2 * time.Second}
}

func (s *Scenario) Setup(ctx context.Context) error {
	s.counters = scenkit.NewCounters()
	return s.SetupInProc(ctx, inproc.Config{})
}

func (s *Scenario) Teardown(ctx context.Context) error { return s.TeardownInProc(ctx) }

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	tenant := "acme"
	if vu%2 == 0 {
		tenant = "globex"
	}
	start := time.Now()
	status, _, err := scenkit.DoJSON(ctx, "POST", s.Env().GatewayURL()+"/v1/sessions",
		[]byte(`{"runtimeRef":"echo"}`),
		scenkit.H("X-Lenny-Tenant-ID", tenant))
	if err != nil {
		s.counters.IncOnError(ctx, "failures", err)
		return err
	}
	if status != http.StatusCreated {
		s.counters.Inc("failures")
		return fmt.Errorf("status=%d", status)
	}
	elapsed := time.Since(start).Seconds()
	s.mu.Lock()
	if tenant == "acme" {
		s.acmeLat = append(s.acmeLat, elapsed)
	} else {
		s.globexLat = append(s.globexLat, elapsed)
	}
	s.mu.Unlock()
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	r.AddCustom("sessions_created", float64(s.Env().SessionCount()))
	s.mu.Lock()
	r.AddCustom("acme_samples", float64(len(s.acmeLat)))
	r.AddCustom("globex_samples", float64(len(s.globexLat)))
	s.mu.Unlock()
	if f := s.counters.Get("failures"); f > 0 {
		return fmt.Errorf("§11.2 violated: %d failed sessions", f)
	}
	if s.Env().SessionCount() == 0 {
		return fmt.Errorf("no sessions reached the gateway")
	}
	return nil
}
