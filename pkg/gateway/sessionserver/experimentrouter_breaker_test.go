// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §10.7 lines 835-844 (SCL-023) — once the per-tenant targeting
// circuit breaker opens, the gateway skips the OpenFeature call entirely
// and sets lenny_experiment_targeting_circuit_open to 1. With a fixed
// clock every failure lands in one window, so the 5th consecutive
// failure opens the circuit and the 6th routing never reaches OFREP.
func TestExperimentTargetingCircuitBreakerSkipsOFREPWhenOpen_spec_10_7_835(t *testing.T) {
	var hits int64
	ofrepSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ofrepSrv.Close()

	exps := experimentstore.NewMemory()
	externalExperiment(t, exps, "exp_ext", "claude-code-v2")

	type gaugeEvent struct {
		tenant, provider string
		open             bool
	}
	var gauge []gaugeEvent
	at := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		Experiments: exps,
		Tenants:     ofrepTenant(t, ofrepSrv.URL),
		Clock:       func() time.Time { return at },
		SetExperimentTargetingCircuitOpen: func(tn, p string, open bool) {
			gauge = append(gauge, gaugeEvent{tn, p, open})
		},
	})

	route := func(id string) {
		row := &sessionstore.Session{ID: id, TenantID: "default", UserID: "alice", RuntimeRef: "claude-code"}
		if err := srv.ApplyExperimentRouting(context.Background(), row); err != nil {
			t.Fatalf("routing %s: %v", id, err)
		}
	}

	// Five failing routings within one window open the breaker.
	for i := 0; i < 5; i++ {
		route("s" + strconv.Itoa(i))
	}
	if got := atomic.LoadInt64(&hits); got != 5 {
		t.Fatalf("OFREP hits after 5 routings = %d, want 5", got)
	}
	// The sixth routing must be served by the open breaker — no OFREP call.
	route("s5")
	if got := atomic.LoadInt64(&hits); got != 5 {
		t.Errorf("OFREP was called after the breaker opened: hits = %d, want 5", got)
	}

	wantHost, _ := url.Parse(ofrepSrv.URL)
	open := false
	for _, g := range gauge {
		if g.open && g.tenant == "default" && g.provider == wantHost.Hostname() {
			open = true
		}
	}
	if !open {
		t.Errorf("expected an open circuit gauge for (default, %s), got %+v", wantHost.Hostname(), gauge)
	}
}
