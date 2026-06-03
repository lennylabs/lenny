// SPDX-License-Identifier: MIT

package diagnostics_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
)

// fakeSource is a test DataSource returning fixed records.
type fakeSource struct {
	session diagnostics.SessionRecord
	pool    diagnostics.PoolRecord
	creds   diagnostics.CredentialPoolRecord
	deps    []diagnostics.ConnectivityDependency
}

func (f fakeSource) Session(context.Context, string) (diagnostics.SessionRecord, error) {
	return f.session, nil
}

func (f fakeSource) Pool(context.Context, string) (diagnostics.PoolRecord, error) {
	return f.pool, nil
}

func (f fakeSource) CredentialPool(context.Context, string) (diagnostics.CredentialPoolRecord, error) {
	return f.creds, nil
}

func (f fakeSource) Connectivity(context.Context) ([]diagnostics.ConnectivityDependency, error) {
	return f.deps, nil
}

func TestDiagnoseSessionBuildsCauseChain(t *testing.T) {
	svc := diagnostics.NewService(fakeSource{session: diagnostics.SessionRecord{
		SessionID: "sess-1", State: "failed", Runtime: "python", Pool: "default-gvisor",
		Signals: diagnostics.Signals{ExitCode: 137, OOMKilled: true},
		Found:   true,
	}})
	diag, err := svc.DiagnoseSession(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if len(diag.CauseChain) != 1 {
		t.Fatalf("cause chain has %d entries, want 1", len(diag.CauseChain))
	}
	// §25.6: exit 137 + OOM flag classifies as OOM_KILLED.
	if diag.CauseChain[0].Category != diagnostics.CategoryOOMKilled {
		t.Errorf("cause category = %q, want OOM_KILLED", diag.CauseChain[0].Category)
	}
}

// TestDiagnoseSessionAppendsSessionStateCause — a pod crash that the
// session also classifies as a budget failure yields a two-level cause
// chain: the proximate pod exit (level 0) followed by the session-state
// terminal reason (level 1). spec: §25.6 line 2890. F-25.6.6.
func TestDiagnoseSessionAppendsSessionStateCause_spec_25_6_2890(t *testing.T) {
	svc := diagnostics.NewService(fakeSource{session: diagnostics.SessionRecord{
		SessionID: "sess-1", State: "failed",
		Signals:       diagnostics.Signals{ExitCode: 1},
		FailureReason: "budget_exceeded",
		Found:         true,
	}})
	diag, err := svc.DiagnoseSession(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if len(diag.CauseChain) != 2 {
		t.Fatalf("cause chain has %d entries, want 2: %+v", len(diag.CauseChain), diag.CauseChain)
	}
	if diag.CauseChain[0].Category != diagnostics.CategoryPodCrash {
		t.Errorf("level 0 = %q, want POD_CRASH", diag.CauseChain[0].Category)
	}
	if diag.CauseChain[1].Category != diagnostics.CategoryBudgetExpired || diag.CauseChain[1].Level != 1 {
		t.Errorf("level 1 = %+v, want BUDGET_EXPIRED at level 1", diag.CauseChain[1])
	}
}

// TestDiagnoseSessionCredentialFailureWithoutPod — a session that failed
// for a credential reason with no pod signal yields a single
// session-state cause level. spec: §25.6 line 2890. F-25.6.6.
func TestDiagnoseSessionCredentialFailureWithoutPod_spec_25_6_2890(t *testing.T) {
	svc := diagnostics.NewService(fakeSource{session: diagnostics.SessionRecord{
		SessionID: "sess-1", State: "failed",
		FailureClass: "credential_error",
		Found:        true,
	}})
	diag, err := svc.DiagnoseSession(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if len(diag.CauseChain) != 1 || diag.CauseChain[0].Category != diagnostics.CategoryCredentialFailure {
		t.Fatalf("want a single CREDENTIAL_FAILURE level, got %+v", diag.CauseChain)
	}
}

// TestDiagnoseSessionCopiesDegradation — a degradation envelope on the
// record flows onto the diagnosis so the handler can return 207. spec:
// §25.6 lines 2908-2920. F-25.6.1.
func TestDiagnoseSessionCopiesDegradation_spec_25_6_2908(t *testing.T) {
	deg := &conventions.Degradation{Level: conventions.DegradationDegraded, ActualSource: "postgres"}
	svc := diagnostics.NewService(fakeSource{session: diagnostics.SessionRecord{
		SessionID: "sess-1", State: "failed", Found: true, Degradation: deg,
	}})
	diag, err := svc.DiagnoseSession(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if diag.Degradation == nil || diag.Degradation.ActualSource != "postgres" {
		t.Fatalf("want degradation copied onto diagnosis, got %+v", diag.Degradation)
	}
}

// TestSessionStateCause covers the §25.6 session-state cause mapping for
// budget and credential failures and the no-match case. spec: §25.6 line
// 2890. F-25.6.6.
func TestSessionStateCause_spec_25_6_2890(t *testing.T) {
	cases := []struct {
		class, reason string
		want          diagnostics.Category
		ok            bool
	}{
		{"", "budget_exceeded", diagnostics.CategoryBudgetExpired, true},
		{"", "delegation_budget_exceeded", diagnostics.CategoryBudgetExpired, true},
		{"credential_error", "", diagnostics.CategoryCredentialFailure, true},
		{"", "credential_pool_exhausted", diagnostics.CategoryCredentialFailure, true},
		{"runtime_crash", "exit_137", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		got, ok := diagnostics.SessionStateCause(c.class, c.reason)
		if ok != c.ok || got != c.want {
			t.Errorf("SessionStateCause(%q,%q) = (%q,%v), want (%q,%v)", c.class, c.reason, got, ok, c.want, c.ok)
		}
	}
}

func TestDiagnoseSessionNotFound(t *testing.T) {
	svc := diagnostics.NewService(fakeSource{session: diagnostics.SessionRecord{Found: false}})
	_, err := svc.DiagnoseSession(context.Background(), "sess-x")
	if diagnostics.CodeOf(err) != diagnostics.ErrCodeSessionNotFound {
		t.Errorf("err code = %q, want SESSION_NOT_FOUND", diagnostics.CodeOf(err))
	}
}

func TestDiagnosePoolClassifiesBottleneck(t *testing.T) {
	svc := diagnostics.NewService(fakeSource{pool: diagnostics.PoolRecord{
		Name:      "default-gvisor",
		PodCounts: diagnostics.PodCountBreakdown{Idle: 0, Claimed: 18},
		Config:    diagnostics.PoolConfigSummary{MinWarm: 5},
		Signals:   diagnostics.PoolSignals{ImagePullFailures: 3},
		CRDSynced: true,
		Found:     true,
	}})
	diag, err := svc.DiagnosePool(context.Background(), "default-gvisor")
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if diag.Bottleneck == nil {
		t.Fatal("bottleneck is nil, want IMAGE_PULL")
	}
	if diag.Bottleneck.Category != diagnostics.BottleneckImagePull {
		t.Errorf("bottleneck = %q, want IMAGE_PULL", diag.Bottleneck.Category)
	}
	if diag.Status != "unhealthy" {
		t.Errorf("status = %q, want unhealthy when a bottleneck is present", diag.Status)
	}
}

func TestDiagnosePoolHealthyHasNoBottleneck(t *testing.T) {
	svc := diagnostics.NewService(fakeSource{pool: diagnostics.PoolRecord{
		Name:      "default-gvisor",
		Signals:   diagnostics.PoolSignals{ReplenishmentRate: 5, ClaimRate: 2},
		CRDSynced: true,
		Found:     true,
	}})
	diag, _ := svc.DiagnosePool(context.Background(), "default-gvisor")
	if diag.Bottleneck != nil {
		t.Errorf("bottleneck = %v, want nil for a healthy pool", diag.Bottleneck)
	}
	if diag.Status != "healthy" {
		t.Errorf("status = %q, want healthy", diag.Status)
	}
}

// TestDiagnosePoolClassifiesCRDSyncLag covers §25.6 line 2865 —
// DiagnosePool derives the CRD_SYNC_LAG bottleneck from a
// PoolRecord whose CRDSynced flag is false, without the DataSource
// having to set the PoolSignals.CRDSyncLag field directly.
func TestDiagnosePoolClassifiesCRDSyncLag_spec_25_6_2865(t *testing.T) {
	svc := diagnostics.NewService(fakeSource{pool: diagnostics.PoolRecord{
		Name:      "default-gvisor",
		Signals:   diagnostics.PoolSignals{ReplenishmentRate: 5, ClaimRate: 2},
		CRDSynced: false,
		CRDDetail: "lenny.dev/Pool generation lag",
		Found:     true,
	}})
	diag, err := svc.DiagnosePool(context.Background(), "default-gvisor")
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if diag.Bottleneck == nil {
		t.Fatal("bottleneck is nil, want CRD_SYNC_LAG")
	}
	if diag.Bottleneck.Category != diagnostics.BottleneckCRDSyncLag {
		t.Errorf("bottleneck = %q, want CRD_SYNC_LAG", diag.Bottleneck.Category)
	}
	if diag.Status != "unhealthy" {
		t.Errorf("status = %q, want unhealthy", diag.Status)
	}
}

func TestDiagnosePoolNotFound(t *testing.T) {
	svc := diagnostics.NewService(fakeSource{pool: diagnostics.PoolRecord{Found: false}})
	_, err := svc.DiagnosePool(context.Background(), "ghost")
	if diagnostics.CodeOf(err) != diagnostics.ErrCodePoolNotFound {
		t.Errorf("err code = %q, want POOL_NOT_FOUND", diagnostics.CodeOf(err))
	}
}

// TestDiagnosePoolDemandSuggestsScale covers §25.17 lines 5199-5216 — a
// DEMAND_EXCEEDS_SUPPLY bottleneck populates bottleneck.details with the
// two compared rates and a SCALE_WARM_POOL suggestedAction pointing at the
// warm-count sub-route with minWarm = current + 10.
func TestDiagnosePoolDemandSuggestsScale_spec_25_17(t *testing.T) {
	svc := diagnostics.NewService(fakeSource{pool: diagnostics.PoolRecord{
		Name:      "default-gvisor",
		Config:    diagnostics.PoolConfigSummary{MinWarm: 5},
		Signals:   diagnostics.PoolSignals{ReplenishmentRate: 0.8, ClaimRate: 4.2},
		CRDSynced: true,
		Found:     true,
	}})
	diag, err := svc.DiagnosePool(context.Background(), "default-gvisor")
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if diag.Bottleneck == nil || diag.Bottleneck.Category != diagnostics.BottleneckDemandExceedsSupply {
		t.Fatalf("bottleneck = %+v, want DEMAND_EXCEEDS_SUPPLY", diag.Bottleneck)
	}
	var details map[string]any
	if err := json.Unmarshal(diag.Bottleneck.Details, &details); err != nil {
		t.Fatalf("details unmarshal: %v", err)
	}
	if details["claimRate"] != 4.2 || details["replenishmentRate"] != 0.8 {
		t.Errorf("details = %v, want claimRate=4.2 replenishmentRate=0.8", details)
	}
	if len(diag.SuggestedActions) != 1 {
		t.Fatalf("suggestedActions has %d entries, want 1", len(diag.SuggestedActions))
	}
	act := diag.SuggestedActions[0]
	if act.Action != "SCALE_WARM_POOL" {
		t.Errorf("action = %q, want SCALE_WARM_POOL", act.Action)
	}
	if act.Endpoint != "PUT /v1/admin/pools/default-gvisor/warm-count" {
		t.Errorf("endpoint = %q, want the warm-count sub-route", act.Endpoint)
	}
	if act.Runbook != "warm-pool-exhaustion" {
		t.Errorf("runbook = %q, want warm-pool-exhaustion", act.Runbook)
	}
	var body map[string]any
	if err := json.Unmarshal(act.Body, &body); err != nil {
		t.Fatalf("action body unmarshal: %v", err)
	}
	if body["minWarm"] != float64(15) {
		t.Errorf("body.minWarm = %v, want 15 (current 5 + 10)", body["minWarm"])
	}
}

// TestDiagnosePoolFailureBottleneckHasNoScaleAction covers §25.6 — an
// infrastructure bottleneck (image pull) reports its failure count in
// details but carries no auto-applicable suggestedAction, since the fix
// is cluster-side rather than an API scale.
func TestDiagnosePoolFailureBottleneckHasNoScaleAction_spec_25_6(t *testing.T) {
	svc := diagnostics.NewService(fakeSource{pool: diagnostics.PoolRecord{
		Name:      "default-gvisor",
		Config:    diagnostics.PoolConfigSummary{MinWarm: 5, MaxPods: 50, Image: "ghcr.io/acme/agent:1"},
		Signals:   diagnostics.PoolSignals{ImagePullFailures: 3},
		CRDSynced: true,
		Found:     true,
	}})
	diag, err := svc.DiagnosePool(context.Background(), "default-gvisor")
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if len(diag.SuggestedActions) != 0 {
		t.Errorf("suggestedActions = %v, want empty for an infrastructure bottleneck", diag.SuggestedActions)
	}
	var details map[string]any
	if err := json.Unmarshal(diag.Bottleneck.Details, &details); err != nil {
		t.Fatalf("details unmarshal: %v", err)
	}
	if details["imagePullFailures"] != float64(3) {
		t.Errorf("details = %v, want imagePullFailures=3", details)
	}
	// §25.17 line 5195 — config carries maxPods and image alongside minWarm.
	if diag.Config.MaxPods != 50 || diag.Config.Image != "ghcr.io/acme/agent:1" {
		t.Errorf("config = %+v, want maxPods=50 image set", diag.Config)
	}
}

func TestDiagnoseCredentialPoolStatus(t *testing.T) {
	cases := []struct {
		util        float64
		rateLimited bool
		want        string
	}{
		{0.4, false, "healthy"},
		{0.75, false, "degraded"},
		{0.95, false, "unhealthy"},
		{0.3, true, "unhealthy"},
	}
	for _, tc := range cases {
		svc := diagnostics.NewService(fakeSource{creds: diagnostics.CredentialPoolRecord{
			Name: "anthropic", Utilization: tc.util, RateLimited: tc.rateLimited, Found: true,
		}})
		diag, err := svc.DiagnoseCredentialPool(context.Background(), "anthropic")
		if err != nil {
			t.Fatalf("diagnose util=%v: %v", tc.util, err)
		}
		if diag.Status != tc.want {
			t.Errorf("util=%v rateLimited=%v: status = %q, want %q", tc.util, tc.rateLimited, diag.Status, tc.want)
		}
	}
}

func TestDiagnoseCredentialPoolNotFound(t *testing.T) {
	svc := diagnostics.NewService(fakeSource{creds: diagnostics.CredentialPoolRecord{Found: false}})
	_, err := svc.DiagnoseCredentialPool(context.Background(), "ghost")
	if diagnostics.CodeOf(err) != diagnostics.ErrCodeCredentialPoolNotFound {
		t.Errorf("err code = %q, want CREDENTIAL_POOL_NOT_FOUND", diagnostics.CodeOf(err))
	}
}

func TestCheckConnectivityHealthyWhenAllReachable(t *testing.T) {
	svc := diagnostics.NewService(fakeSource{deps: []diagnostics.ConnectivityDependency{
		{Name: "postgres", Reachable: true},
		{Name: "redis", Reachable: true},
	}})
	report, err := svc.CheckConnectivity(context.Background())
	if err != nil {
		t.Fatalf("connectivity: %v", err)
	}
	if !report.Healthy {
		t.Error("healthy = false when every dependency is reachable")
	}
}

func TestCheckConnectivityUnhealthyWhenADependencyDown(t *testing.T) {
	svc := diagnostics.NewService(fakeSource{deps: []diagnostics.ConnectivityDependency{
		{Name: "postgres", Reachable: true},
		{Name: "minio", Reachable: false, Detail: "dial timeout"},
	}})
	report, _ := svc.CheckConnectivity(context.Background())
	if report.Healthy {
		t.Error("healthy = true when a dependency is unreachable")
	}
}
