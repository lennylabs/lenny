// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/gatewayleader"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/session/orphansession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// liveMirror reports every pod running in pool-a with a 42-second lag, so
// a reconcile pass publishes the pool's lag series and fails no session.
type liveMirror struct{}

func (liveMirror) GetByPodID(context.Context, string) (orphansession.MirrorPod, bool, error) {
	return orphansession.MirrorPod{PoolID: "pool-a", Phase: "running"}, true, nil
}

func (liveMirror) MirrorLagSeconds(context.Context, string) (float64, error) { return 42, nil }

type oneTenant struct{}

func (oneTenant) ListTenants(context.Context) ([]string, error) { return []string{"acme"}, nil }

// lagSeriesLine is the series a pass over pool-a publishes.
const lagSeriesLine = `lenny_agent_pod_state_mirror_lag_seconds{pool="pool-a"} 42`

// scrapeGateway returns the gateway metrics exposition.
func scrapeGateway(m *gatewaymetrics.Metrics) string {
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rr.Body.String()
}

// orphanLeaderWiring is a wiring whose orphan-session reconciler reconciles
// one bound running session every few milliseconds into m.
func orphanLeaderWiring(t *testing.T, m *gatewaymetrics.Metrics) *gatewayWiring {
	t.Helper()
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s-1", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-1", PoolRef: "pool-a",
	}); err != nil {
		t.Fatal(err)
	}
	r := orphansession.New(store, oneTenant{}, liveMirror{}, orphansession.Options{
		Interval: 5 * time.Millisecond,
		Metrics:  m,
	})
	return &gatewayWiring{gatewayWiringFields: gatewayWiringFields{gwMetrics: m, orphanSessionReconciler: r}}
}

// TestOrphanSessionReconcilerRegistersOnTheLeaderGate_spec_10_1_4 pins the
// wiring of the orphan-session reconciler: it registers on the
// gateway-leader gate under "orphan-session-reconciler" only when it was
// built, the registration precedes the gate's start, and no replica starts
// it outside the gate.
//
// spec: §10.1.4, §12.5
func TestOrphanSessionReconcilerRegistersOnTheLeaderGate_spec_10_1_4(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		w    *gatewayWiring
		want []string
	}{
		{"built", orphanLeaderWiring(t, m), []string{"orphan-session-reconciler"}},
		{"no mirror wired", &gatewayWiring{gatewayWiringFields: gatewayWiringFields{gwMetrics: m}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var names []string
			tc.w.addOrphanSessionReconciler(func(name string, run func(context.Context)) {
				if run == nil {
					t.Errorf("%s registered a nil run", name)
				}
				names = append(names, name)
			})
			if strings.Join(names, ",") != strings.Join(tc.want, ",") {
				t.Errorf("registered %v, want %v", names, tc.want)
			}
		})
	}

	space := regexp.MustCompile(`\s+`)
	read := func(file string) string {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return space.ReplaceAllString(string(b), " ")
	}
	sweeps := read("leader_sweeps.go")
	reg := strings.Index(sweeps, "w.addOrphanSessionReconciler(func(name string, run func(context.Context)) { leaderGate.Add(name, run) })")
	run := strings.Index(sweeps, "go leaderGate.Run(")
	if reg < 0 || run < 0 || reg > run {
		t.Errorf("leader_sweeps.go: the reconciler's leader-gate registration (at %d) must precede go leaderGate.Run (at %d)", reg, run)
	}
	workers := read("reconciler_workers.go")
	if strings.Contains(workers, "go orphanSessionReconciler.Run") || strings.Contains(workers, ".Run(w.watchdogCtx, func(failed int") {
		t.Error("reconciler_workers.go still starts the orphan-session reconciler on every replica")
	}
	if !strings.Contains(workers, "w.orphanSessionReconciler = orphansession.New(") {
		t.Error("reconciler_workers.go does not store the orphan-session reconciler on the wiring")
	}
}

// TestOrphanSessionReconcilerRunsOnlyWhileLeaderAndClearsLagOnLoss_spec_10_1_4
// drives the registered reconciler through the gateway-leader gate: a
// follower publishes no lag series, the leader publishes its pools' series,
// and losing the lease deletes them, so the PodStateMirrorStale alert reads
// only the current leader's values. Re-acquiring the lease publishes again.
//
// spec: §10.1.4, §12.5
func TestOrphanSessionReconcilerRunsOnlyWhileLeaderAndClearsLagOnLoss_spec_10_1_4(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatal(err)
	}
	w := orphanLeaderWiring(t, m)
	elector := newFakeElector(false)
	gate := gatewayleader.NewGate(elector)
	w.addOrphanSessionReconciler(func(name string, run func(context.Context)) { gate.Add(name, run) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go gate.Run(ctx)

	time.Sleep(30 * time.Millisecond)
	if strings.Contains(scrapeGateway(m), lagSeriesLine) {
		t.Fatal("a follower published the mirror lag series")
	}

	elector.acquire <- struct{}{}
	waitUntil(t, "the leader to publish the lag series", func() bool { return strings.Contains(scrapeGateway(m), lagSeriesLine) })

	elector.lose <- struct{}{}
	waitUntil(t, "the lag series to clear after the lease is lost", func() bool {
		return !strings.Contains(scrapeGateway(m), "lenny_agent_pod_state_mirror_lag_seconds{")
	})
	time.Sleep(30 * time.Millisecond)
	if strings.Contains(scrapeGateway(m), lagSeriesLine) {
		t.Fatal("the former leader published the lag series after losing the lease")
	}

	elector.acquire <- struct{}{}
	waitUntil(t, "the re-elected leader to publish the lag series", func() bool { return strings.Contains(scrapeGateway(m), lagSeriesLine) })
}
