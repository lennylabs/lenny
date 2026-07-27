// SPDX-License-Identifier: MIT

//go:build load_local

// Package low_resource_startup drives the permissive `/healthz` probe
// endpoint of the lenny-ops server while every CPU on the host is
// saturated, modelling a replica that starts and runs on a
// resource-starved node.
//
// spec: §25.4 line 1061 — "**`/healthz`** (permissive): 200 if the
// process is alive and at least one of {Postgres, K8s API} is
// reachable. Used by liveness and startup." The permissive probe
// reports that the process is running and does not fail on a
// downstream dependency outage, so it must answer without consulting
// the §25.6 dependency probes.
//
// The scenario asserts two properties under CPU pressure:
//
//  1. Every permissive probe answers 200 with `status: ok`. This is a
//     functional invariant, so it admits no failures.
//  2. The service time stays inside the probe timeout the §25.4
//     liveness and startup probes run with. Neither the §25.4 probe
//     block (`livenessProbe` at lines 1053-1058: `failureThreshold: 5`,
//     `periodSeconds: 10`) nor the rendered ops Deployment sets
//     `timeoutSeconds`, so each probe inherits the Kubernetes default
//     of one second and a slower response counts as one probe failure.
//     The budget is a p99 rather than a per-sample ceiling: a single
//     probe over the timeout is one failure out of the five consecutive
//     failures that restart the pod, so the contract is about the
//     distribution rather than about any one sample. A per-sample
//     ceiling also fails on scheduler jitter that a normally-loaded
//     host produces regardless of the code under test.
//
// The server is built with dependency probes that block until their
// own §25.6 two-second deadline expires. A permissive probe that
// consulted them would cost two seconds per call and blow the budget
// by three orders of magnitude, which is what makes the latency
// assertion a check on the endpoint rather than on the host.
//
// TESTING.md §12.7.a resiliency scenarios.
package low_resource_startup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/probe"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "low_resource_startup"

// probeTimeout is the timeout a §25.4 liveness or startup probe runs
// with. The spec's probe block and the rendered ops Deployment both
// leave `timeoutSeconds` unset, so the Kubernetes default applies and a
// `/healthz` response slower than this counts as a probe failure.
const probeTimeout = 1 * time.Second

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// burner generates synthetic CPU load by spinning on a counter.
type burner struct {
	stop chan struct{}
	wg   sync.WaitGroup
}

func (b *burner) start() {
	for i := 0; i < runtime.NumCPU(); i++ {
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			x := 0
			for {
				select {
				case <-b.stop:
					return
				default:
				}
				for k := 0; k < 1000; k++ {
					x++
				}
			}
		}()
	}
}

func (b *burner) shutdown() {
	close(b.stop)
	b.wg.Wait()
}

type Scenario struct {
	counters *scenkit.Counters
	burner   *burner
	srv      *opsserver.Server
}

func (s *Scenario) Name() string { return name }
func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 4, Duration: 1 * time.Second}
}

func (s *Scenario) RampProfiles() []loadgen.Profile {
	return []loadgen.Profile{
		{Kind: loadgen.ConstantVU, VUs: 2, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 8, Duration: 1 * time.Second},
		{Kind: loadgen.ConstantVU, VUs: 32, Duration: 1 * time.Second},
	}
}

func (s *Scenario) Setup(ctx context.Context) error {
	// Probes that never settle on their own. probe.Run bounds each by
	// the §25.6 two-second deadline, so a permissive /healthz that
	// consulted its dependencies would take two seconds per request.
	// The permissive path must not reach them at all.
	blocking := func(pctx context.Context) error {
		<-pctx.Done()
		return pctx.Err()
	}
	s.srv = opsserver.New(opsserver.Options{
		Probes: map[string]probe.Func{
			opsserver.ProbePostgresName: blocking,
			opsserver.ProbeK8sAPIName:   blocking,
		},
	})
	s.burner = &burner{stop: make(chan struct{})}
	s.burner.start()
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error {
	s.burner.shutdown()
	return nil
}

func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	// A kubelet probe carries no caller-scoped cancellation, so the
	// request runs on its own context rather than the run context; the
	// loadgen driver already bounds the run.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		s.counters.Inc("healthz_not_ok")
		return fmt.Errorf("§25.4 violated: permissive /healthz returned %d under CPU pressure", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		s.counters.Inc("healthz_not_ok")
		return fmt.Errorf("§25.4 violated: permissive /healthz body is not JSON: %w", err)
	}
	if body["status"] != "ok" {
		s.counters.Inc("healthz_not_ok")
		return fmt.Errorf("§25.4 violated: permissive /healthz reported status %q under CPU pressure", body["status"])
	}
	s.counters.Inc("healthz_ok")
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	if v := s.counters.Get("healthz_not_ok"); v > 0 {
		return fmt.Errorf("§25.4 violated: %d permissive /healthz responses were not 200 ok under CPU pressure", v)
	}
	if s.counters.Get("healthz_ok") == 0 {
		return fmt.Errorf("scenario did not exercise the permissive /healthz probe")
	}
	// spec: §25.4 lines 1053-1058 — the liveness probe restarts the pod
	// after five consecutive failures, so the contract the load test
	// pins is the distribution of probe service times against the
	// one-second probe timeout, not any single sample.
	if p99 := r.Latency.P99; p99 > probeTimeout.Seconds() {
		return fmt.Errorf("§25.4 violated: permissive /healthz p99 %.3fs exceeds the %s probe timeout under CPU pressure",
			p99, probeTimeout)
	}
	return nil
}
