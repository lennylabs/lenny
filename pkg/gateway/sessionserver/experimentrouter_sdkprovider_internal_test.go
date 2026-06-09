// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/experimentprovider"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// fakeExternalClient is an externalEvalClient returning a fixed result
// or error so the §10.7 SDK-provider routing path is exercised without a
// live LaunchDarkly/Statsig/Unleash service.
type fakeExternalClient struct {
	variant string
	value   any
	key     string
	err     error
	calls   int
}

func (f *fakeExternalClient) Evaluate(context.Context, string, map[string]any) (externalEvalResult, error) {
	f.calls++
	if f.err != nil {
		return externalEvalResult{}, f.err
	}
	return externalEvalResult{Variant: f.variant, Value: f.value, Key: f.key}, nil
}

// fakeResolver implements ExternalProviderResolver. A non-nil err makes
// provider construction itself fail (the resolver returns no client).
type fakeResolver struct {
	client externalEvalClient
	err    error
}

func (r fakeResolver) For(context.Context, experiment.TargetingConfig) (externalEvalClient, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.client, nil
}

// sdkRoutingServer builds a minimal Server whose tenant `default`
// targets a launchdarkly SDK provider and that carries one active
// mode:external experiment on the claude-code runtime.
func sdkRoutingServer(t *testing.T, resolver ExternalProviderResolver, emitter *events.Emitter) *Server {
	t.Helper()
	ctx := context.Background()
	exps := experimentstore.NewMemory()
	if err := exps.Create(ctx, experimentstore.Experiment{
		ID: "exp_ext", TenantID: "default", Status: experiment.StatusActive,
		BaseRuntime: "claude-code",
		Variants: []experimentstore.Variant{
			{ID: "treatment", Runtime: "claude-code-v2", Weight: 0.5},
		},
		TargetingMode: experiment.TargetingExternal,
		Sticky:        experiment.StickySession,
		Propagation:   experiment.PropagationInherit,
	}); err != nil {
		t.Fatalf("seed experiment: %v", err)
	}
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{
		ID: "default",
		ExperimentTargeting: experiment.TargetingConfig{
			Provider:     experiment.TargetingProviderLaunchDarkly,
			LaunchDarkly: &experiment.LaunchDarklyConfig{SDKKey: "sdk-key"},
		},
	}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	s := &Server{
		experiments:       exps,
		tenants:           tenants,
		externalProviders: resolver,
		opsEmitter:        emitter,
		clock:             func() time.Time { return time.Now().UTC() },
	}
	s.targetingBreaker = newTargetingBreaker(s.clock, nil)
	return s
}

func targetingFailedCount(emitter *events.Emitter) int {
	page := emitter.Buffer().Query(0, events.EventFilter{
		EventType: string(events.EventExperimentTargetingFailed),
	}, 0)
	return len(page.Events)
}

// spec: §10.7 lines 779-782, 825 — a tenant configured for a built-in
// OpenFeature SDK provider (launchdarkly) now actually evaluates its
// mode:external experiments and enrolls the session, instead of the
// prior silent route-to-control. F-10.7.3.
func TestApplyExperimentRoutingEnrollsViaSDKProvider_spec_10_7(t *testing.T) {
	emitter := events.NewEmitter(events.NewEventBuffer(0), "sdk-test")
	resolver := fakeResolver{client: &fakeExternalClient{variant: "treatment"}}
	s := sdkRoutingServer(t, resolver, emitter)
	row := &sessionstore.Session{ID: "sess1", TenantID: "default", UserID: "alice", RuntimeRef: "claude-code"}
	if err := s.ApplyExperimentRouting(context.Background(), row); err != nil {
		t.Fatalf("ApplyExperimentRouting: %v", err)
	}
	if row.ExperimentContext == nil || row.ExperimentContext.ExperimentID != "exp_ext" {
		t.Fatalf("session not enrolled via SDK provider: %+v", row.ExperimentContext)
	}
	if row.RuntimeRef != "claude-code-v2" {
		t.Errorf("variant runtime not applied: RuntimeRef = %q, want claude-code-v2", row.RuntimeRef)
	}
	if targetingFailedCount(emitter) != 0 {
		t.Errorf("targeting_failed emitted on a successful SDK evaluation")
	}
}

// spec: §10.7 line 833 — an SDK-provider evaluation error is the
// targeting_failed condition: no enrollment is made and the
// experiment.targeting_failed event is emitted. The prior code never
// reached this path for launchdarkly/statsig/unleash (F-10.7.3 gap).
func TestApplyExperimentRoutingSDKProviderErrorEmitsTargetingFailed_spec_10_7_833(t *testing.T) {
	emitter := events.NewEmitter(events.NewEventBuffer(0), "sdk-test")
	resolver := fakeResolver{client: &fakeExternalClient{err: &experimentprovider.EvalError{FlagKey: "exp_ext", Code: "PROVIDER_NOT_READY", Detail: "not ready"}}}
	s := sdkRoutingServer(t, resolver, emitter)
	row := &sessionstore.Session{ID: "sess1", TenantID: "default", UserID: "alice", RuntimeRef: "claude-code"}
	if err := s.ApplyExperimentRouting(context.Background(), row); err != nil {
		t.Fatalf("ApplyExperimentRouting: %v", err)
	}
	if row.ExperimentContext != nil {
		t.Errorf("session enrolled despite SDK provider error: %+v", row.ExperimentContext)
	}
	if got := targetingFailedCount(emitter); got != 1 {
		t.Errorf("targeting_failed events = %d, want 1", got)
	}
}

// A failure to construct the SDK provider itself (resolver returns an
// error) is surfaced through the same targeting_failed path rather than
// as a silent skip. This is the exact gap F-10.7.3 named: a misconfigured
// vendor SDK no longer routes every experiment to control without signal.
func TestApplyExperimentRoutingSDKConstructionFailureEmitsTargetingFailed_spec_10_7_833(t *testing.T) {
	emitter := events.NewEmitter(events.NewEventBuffer(0), "sdk-test")
	resolver := fakeResolver{err: errors.New("provider construction failed")}
	s := sdkRoutingServer(t, resolver, emitter)
	row := &sessionstore.Session{ID: "sess1", TenantID: "default", UserID: "alice", RuntimeRef: "claude-code"}
	if err := s.ApplyExperimentRouting(context.Background(), row); err != nil {
		t.Fatalf("ApplyExperimentRouting: %v", err)
	}
	if row.ExperimentContext != nil {
		t.Errorf("session enrolled despite construction failure: %+v", row.ExperimentContext)
	}
	if got := targetingFailedCount(emitter); got != 1 {
		t.Errorf("targeting_failed events = %d, want 1", got)
	}
}

// A nil externalProviders resolver disables the SDK-provider path: the
// experiment is skipped (no enrollment) exactly as a tenant with no
// targeting, and no spurious targeting_failed is emitted.
func TestApplyExperimentRoutingNilResolverSkips(t *testing.T) {
	emitter := events.NewEmitter(events.NewEventBuffer(0), "sdk-test")
	s := sdkRoutingServer(t, nil, emitter)
	row := &sessionstore.Session{ID: "sess1", TenantID: "default", UserID: "alice", RuntimeRef: "claude-code"}
	if err := s.ApplyExperimentRouting(context.Background(), row); err != nil {
		t.Fatalf("ApplyExperimentRouting: %v", err)
	}
	if row.ExperimentContext != nil {
		t.Errorf("session enrolled with no resolver wired: %+v", row.ExperimentContext)
	}
	if got := targetingFailedCount(emitter); got != 0 {
		t.Errorf("targeting_failed events = %d, want 0 with a nil resolver", got)
	}
}
