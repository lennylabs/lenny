// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §10.7 ExperimentRouter — variant assignment at session creation.

// routedExperiment seeds an active percentage-mode experiment whose
// single variant has a weight near 1, so every session on the base
// runtime is routed to the variant.
func routedExperiment(t *testing.T, exps experimentstore.Store, baseRuntime, variantRuntime, variantPool string) {
	t.Helper()
	if err := exps.Create(context.Background(), experimentstore.Experiment{
		ID: "exp_1", TenantID: "default", Status: experiment.StatusActive,
		BaseRuntime: baseRuntime,
		Variants: []experimentstore.Variant{
			{ID: "treatment", Runtime: variantRuntime, Pool: variantPool, Weight: 0.999999},
		},
		TargetingMode: experiment.TargetingPercentage,
		Sticky:        experiment.StickySession,
		Propagation:   experiment.PropagationInherit,
	}); err != nil {
		t.Fatalf("seed experiment: %v", err)
	}
}

func routerServer(t *testing.T, sessionID string, exps experimentstore.Store) (http.Handler, *memstore.Store) {
	t.Helper()
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Experiments: exps,
		IDFunc:      func() string { return sessionID },
	})
	return srv.Handler(), store
}

func TestExperimentRouterEnrollsSessionAtCreation(t *testing.T) {
	exps := experimentstore.NewMemory()
	routedExperiment(t, exps, "claude-code", "claude-code-v2", "cc-v2-pool")
	h, store := routerServer(t, "sess_routed", exps)

	rr := postSession(t, h, "/v1/sessions", "alice", "default")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, err := store.Get(context.Background(), "default", "sess_routed")
	if err != nil {
		t.Fatalf("Get created session: %v", err)
	}
	if got.ExperimentContext == nil {
		t.Fatalf("session was not enrolled in the experiment")
	}
	if got.ExperimentContext.ExperimentID != "exp_1" || got.ExperimentContext.VariantID != "treatment" {
		t.Errorf("experimentContext = %+v, want exp_1/treatment", got.ExperimentContext)
	}
	// §10.7: an enrolled session is routed onto the variant's runtime and pool.
	if got.RuntimeRef != "claude-code-v2" || got.PoolRef != "cc-v2-pool" {
		t.Errorf("session not routed to the variant: runtime=%q pool=%q", got.RuntimeRef, got.PoolRef)
	}
}

func TestExperimentRouterSkipsNonMatchingBaseRuntime(t *testing.T) {
	exps := experimentstore.NewMemory()
	// The experiment targets a different base runtime than the session.
	routedExperiment(t, exps, "some-other-runtime", "v2", "v2-pool")
	h, store := routerServer(t, "sess_unrouted", exps)

	rr := postSession(t, h, "/v1/sessions", "alice", "default")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d", rr.Code)
	}
	got, _ := store.Get(context.Background(), "default", "sess_unrouted")
	if got.ExperimentContext != nil {
		t.Errorf("session enrolled in a non-matching experiment: %+v", got.ExperimentContext)
	}
	if got.RuntimeRef != "claude-code" {
		t.Errorf("runtime = %q, want the unchanged base runtime claude-code", got.RuntimeRef)
	}
}

func TestExperimentRouterNoExperimentsLeavesSessionUnenrolled(t *testing.T) {
	h, store := routerServer(t, "sess_none", experimentstore.NewMemory())
	rr := postSession(t, h, "/v1/sessions", "alice", "default")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d", rr.Code)
	}
	got, _ := store.Get(context.Background(), "default", "sess_none")
	if got.ExperimentContext != nil {
		t.Errorf("session enrolled with no experiments defined: %+v", got.ExperimentContext)
	}
}

// seedRoutableExperiment registers an active percentage-mode experiment
// on the claude-code base runtime whose single variant has a weight
// near 1, so every session is routed to the variant. createdAt fixes
// the §10.7 first-match evaluation order.
func seedRoutableExperiment(t *testing.T, exps experimentstore.Store, id string, createdAt time.Time) {
	t.Helper()
	if err := exps.Create(context.Background(), experimentstore.Experiment{
		ID: id, TenantID: "default", Status: experiment.StatusActive,
		BaseRuntime: "claude-code",
		Variants: []experimentstore.Variant{
			{ID: "treatment", Weight: 0.999999},
		},
		TargetingMode: experiment.TargetingPercentage,
		Sticky:        experiment.StickySession,
		Propagation:   experiment.PropagationInherit,
		CreatedAt:     createdAt,
	}); err != nil {
		t.Fatalf("seed experiment %s: %v", id, err)
	}
}

// externalExperiment seeds an active mode:external experiment on the
// claude-code base runtime with a single `treatment` variant.
func externalExperiment(t *testing.T, exps experimentstore.Store, id, variantRuntime string) {
	t.Helper()
	if err := exps.Create(context.Background(), experimentstore.Experiment{
		ID: id, TenantID: "default", Status: experiment.StatusActive,
		BaseRuntime: "claude-code",
		Variants: []experimentstore.Variant{
			{ID: "treatment", Runtime: variantRuntime, Weight: 0.5},
		},
		TargetingMode: experiment.TargetingExternal,
		Sticky:        experiment.StickySession,
		Propagation:   experiment.PropagationInherit,
	}); err != nil {
		t.Fatalf("seed external experiment: %v", err)
	}
}

// ofrepTenant seeds tenant `default` with an OFREP experimentTargeting
// block pointing at endpoint.
func ofrepTenant(t *testing.T, endpoint string) tenantstore.Store {
	t.Helper()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{
		ID: "default",
		ExperimentTargeting: experiment.TargetingConfig{
			Provider: experiment.TargetingProviderOFREP,
			OFREP:    &experiment.OFREPConfig{Endpoint: endpoint},
		},
	}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return tenants
}

func TestExperimentRouterEnrollsViaOFREP(t *testing.T) {
	ofrepSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"variant":"treatment","reason":"TARGETING_MATCH"}`))
	}))
	defer ofrepSrv.Close()

	exps := experimentstore.NewMemory()
	externalExperiment(t, exps, "exp_ext", "claude-code-v2")
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Experiments: exps,
		Tenants:     ofrepTenant(t, ofrepSrv.URL),
		IDFunc:      func() string { return "sess_ext" },
	})

	rr := postSession(t, srv.Handler(), "/v1/sessions", "alice", "default")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, _ := store.Get(context.Background(), "default", "sess_ext")
	if got.ExperimentContext == nil || got.ExperimentContext.ExperimentID != "exp_ext" {
		t.Fatalf("session not enrolled via OFREP: %+v", got.ExperimentContext)
	}
	if got.ExperimentContext.VariantID != "treatment" {
		t.Errorf("variantId = %q, want treatment", got.ExperimentContext.VariantID)
	}
	if got.RuntimeRef != "claude-code-v2" {
		t.Errorf("runtime = %q, want the variant runtime claude-code-v2", got.RuntimeRef)
	}
}

func TestExperimentRouterOFREPFailureEmitsTargetingFailed(t *testing.T) {
	// A provider error makes no external assignment and emits the §16.6
	// targeting_failed event.
	ofrepSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ofrepSrv.Close()

	exps := experimentstore.NewMemory()
	externalExperiment(t, exps, "exp_ext", "claude-code-v2")
	emitter := eventbuffer.NewEmitter(eventbuffer.NewEventBuffer(0), "test")
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Experiments: exps,
		Tenants:     ofrepTenant(t, ofrepSrv.URL),
		OpsEmitter:  emitter,
		IDFunc:      func() string { return "sess_fail" },
	})

	rr := postSession(t, srv.Handler(), "/v1/sessions", "alice", "default")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, _ := store.Get(context.Background(), "default", "sess_fail")
	if got.ExperimentContext != nil {
		t.Errorf("session enrolled despite an OFREP failure: %+v", got.ExperimentContext)
	}
	page := emitter.Buffer().Query(0, events.EventFilter{
		EventType: string(events.EventExperimentTargetingFailed),
	}, 0)
	if len(page.Events) != 1 {
		t.Errorf("targeting_failed events = %d, want 1", len(page.Events))
	}
}

func TestExperimentRouterEmitsMultiEligibleSkipped(t *testing.T) {
	// Two routable percentage-mode experiments target the session's base
	// runtime. The session enrolls in the earlier-created one; §16.6
	// requires a multi_eligible_skipped event naming the later one.
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	exps := experimentstore.NewMemory()
	seedRoutableExperiment(t, exps, "exp_first", createdAt)
	seedRoutableExperiment(t, exps, "exp_second", createdAt.Add(time.Hour))

	emitter := eventbuffer.NewEmitter(eventbuffer.NewEventBuffer(0), "replica-test")
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Experiments: exps,
		OpsEmitter:  emitter,
		IDFunc:      func() string { return "sess_multi" },
	})

	rr := postSession(t, srv.Handler(), "/v1/sessions", "alice", "default")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, _ := store.Get(context.Background(), "default", "sess_multi")
	if got.ExperimentContext == nil || got.ExperimentContext.ExperimentID != "exp_first" {
		t.Fatalf("session enrolled in %+v, want exp_first", got.ExperimentContext)
	}

	page := emitter.Buffer().Query(0, events.EventFilter{
		EventType: string(events.EventExperimentMultiEligibleSkipped),
	}, 0)
	if len(page.Events) != 1 {
		t.Fatalf("buffer holds %d multi_eligible_skipped events, want 1", len(page.Events))
	}
	ev := page.Events[0].Event
	if ev.Type != "dev.lenny.experiment.multi_eligible_skipped" {
		t.Errorf("event type = %q", ev.Type)
	}
	if ev.Severity != "info" {
		t.Errorf("severity = %q, want info", ev.Severity)
	}
	var data struct {
		TenantID   string   `json:"tenant_id"`
		UserID     string   `json:"user_id"`
		EnrolledID string   `json:"enrolled_experiment_id"`
		SkippedIDs []string `json:"skipped_experiment_ids"`
	}
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatalf("event data: %v", err)
	}
	if data.EnrolledID != "exp_first" {
		t.Errorf("enrolled_experiment_id = %q, want exp_first", data.EnrolledID)
	}
	if len(data.SkippedIDs) != 1 || data.SkippedIDs[0] != "exp_second" {
		t.Errorf("skipped_experiment_ids = %v, want [exp_second]", data.SkippedIDs)
	}
}

func TestExperimentRouterNoSkipEmitsNothing(t *testing.T) {
	// A session enrolled in the only experiment has no skipped peers, so
	// no multi_eligible_skipped event is emitted.
	exps := experimentstore.NewMemory()
	seedRoutableExperiment(t, exps, "exp_only", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	emitter := eventbuffer.NewEmitter(eventbuffer.NewEventBuffer(0), "replica-test")
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		Experiments: exps,
		OpsEmitter:  emitter,
		IDFunc:      func() string { return "sess_one" },
	})

	rr := postSession(t, srv.Handler(), "/v1/sessions", "alice", "default")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d", rr.Code)
	}
	page := emitter.Buffer().Query(0, events.EventFilter{
		EventType: string(events.EventExperimentMultiEligibleSkipped),
	}, 0)
	if len(page.Events) != 0 {
		t.Errorf("emitted %d events, want 0 — the session had no skipped experiments", len(page.Events))
	}
}
