// SPDX-License-Identifier: MIT

package poolstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/egress"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func TestCreateAndGet(t *testing.T) {
	s := poolstore.NewMemory()
	p := poolstore.Pool{
		Name:                 "default-pool",
		RuntimeRef:           "echo",
		IsolationProfile:     isolation.ProfileSandboxed,
		ExecutionMode:        runtimestore.ExecutionModeSession,
		ResourceClass:        "small",
		WarmCount:            3,
		MaxSessionAgeSeconds: 3600,
	}
	if err := s.Create(context.Background(), p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(context.Background(), "default-pool")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RuntimeRef != "echo" || got.WarmCount != 3 {
		t.Errorf("Get: %+v", got)
	}
}

func TestCreateRejectsStandardWithoutAllow(t *testing.T) {
	s := poolstore.NewMemory()
	err := s.Create(context.Background(), poolstore.Pool{
		Name:             "runc-pool",
		IsolationProfile: isolation.ProfileStandard,
	})
	if err == nil {
		t.Error("standard isolation without allowStandardIsolation should fail")
	}
}

func TestCreateAdmitsStandardWithAllow(t *testing.T) {
	s := poolstore.NewMemory()
	err := s.Create(context.Background(), poolstore.Pool{
		Name:                   "runc-pool",
		IsolationProfile:       isolation.ProfileStandard,
		AllowStandardIsolation: true,
	})
	if err != nil {
		t.Errorf("standard isolation with allowStandardIsolation should succeed: %v", err)
	}
}

// TestCreateRejectsInternetEgressOnStandard covers the §13.2
// cross-control: a runc pool cannot use the `internet` egress profile.
func TestCreateRejectsInternetEgressOnStandard(t *testing.T) {
	s := poolstore.NewMemory()
	err := s.Create(context.Background(), poolstore.Pool{
		Name:                   "runc-internet",
		IsolationProfile:       isolation.ProfileStandard,
		AllowStandardIsolation: true,
		EgressProfile:          egress.ProfileInternet,
	})
	if err == nil {
		t.Fatal("internet egress on standard isolation should be rejected (§13.2)")
	}
}

func TestCreateAdmitsEgressIsolationCombinations(t *testing.T) {
	cases := []struct {
		name  string
		iso   isolation.Profile
		eg    egress.Profile
		allow bool
	}{
		{"internet+sandboxed", isolation.ProfileSandboxed, egress.ProfileInternet, false},
		{"internet+microvm", isolation.ProfileMicrovm, egress.ProfileInternet, false},
		{"provider-direct+standard", isolation.ProfileStandard, egress.ProfileProviderDirect, true},
		{"restricted+standard", isolation.ProfileStandard, egress.ProfileRestricted, true},
		{"empty-egress+standard", isolation.ProfileStandard, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := poolstore.NewMemory()
			err := s.Create(context.Background(), poolstore.Pool{
				Name:                   "pool",
				IsolationProfile:       tc.iso,
				AllowStandardIsolation: tc.allow,
				EgressProfile:          tc.eg,
			})
			if err != nil {
				t.Errorf("Create(%s) = %v, want success", tc.name, err)
			}
		})
	}
}

// TestCreateRejectsUnknownEgressProfile fails closed on a mistyped
// profile rather than silently ignoring it.
func TestCreateRejectsUnknownEgressProfile(t *testing.T) {
	s := poolstore.NewMemory()
	err := s.Create(context.Background(), poolstore.Pool{
		Name:             "bad-egress",
		IsolationProfile: isolation.ProfileSandboxed,
		EgressProfile:    egress.Profile("open"),
	})
	if err == nil {
		t.Fatal("unrecognised egress profile should be rejected")
	}
}

// TestUpdateRejectsInternetEgressOnStandard guards the §13.2
// cross-control on the mutate path, not just create.
func TestUpdateRejectsInternetEgressOnStandard(t *testing.T) {
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), poolstore.Pool{
		Name:                   "runc-pool",
		IsolationProfile:       isolation.ProfileStandard,
		AllowStandardIsolation: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := s.Update(context.Background(), "runc-pool", func(p *poolstore.Pool) error {
		p.EgressProfile = egress.ProfileInternet
		return nil
	})
	if err == nil {
		t.Fatal("updating a runc pool to internet egress should be rejected (§13.2)")
	}
}

// TestCreateRejectsDNSOptOutOnNonStandard covers the §13.2 cross-control:
// only a `standard` (runc) pool may opt out of the dedicated CoreDNS
// instance. A sandboxed or microvm pool must keep the dedicated resolver.
// spec: 13.2 (per-pool DNS opt-out)
func TestCreateRejectsDNSOptOutOnNonStandard(t *testing.T) {
	for _, iso := range []isolation.Profile{isolation.ProfileSandboxed, isolation.ProfileMicrovm} {
		s := poolstore.NewMemory()
		err := s.Create(context.Background(), poolstore.Pool{
			Name:             "dns-optout-" + string(iso),
			IsolationProfile: iso,
			DNSPolicy:        poolstore.DNSPolicyClusterDefault,
		})
		if err == nil {
			t.Fatalf("dnsPolicy=cluster-default on %s isolation should be rejected (§13.2)", iso)
		}
	}
}

// TestCreateAdmitsDNSOptOutOnStandard covers the §13.2 opt-out happy path:
// a runc pool may set dnsPolicy: cluster-default. Empty DNSPolicy always
// validates and keeps the pool on the dedicated CoreDNS instance.
// spec: 13.2 (per-pool DNS opt-out)
func TestCreateAdmitsDNSOptOutOnStandard(t *testing.T) {
	cases := []struct {
		name string
		dns  string
	}{
		{"cluster-default+standard", poolstore.DNSPolicyClusterDefault},
		{"empty-dns+standard", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := poolstore.NewMemory()
			err := s.Create(context.Background(), poolstore.Pool{
				Name:                   "pool",
				IsolationProfile:       isolation.ProfileStandard,
				AllowStandardIsolation: true,
				DNSPolicy:              tc.dns,
			})
			if err != nil {
				t.Errorf("Create(%s) = %v, want success", tc.name, err)
			}
		})
	}
}

// TestCreateRejectsUnknownDNSPolicy fails closed on a mistyped DNS policy
// rather than silently ignoring it; cluster-default is the only opt-out.
// spec: 13.2 (per-pool DNS opt-out)
func TestCreateRejectsUnknownDNSPolicy(t *testing.T) {
	s := poolstore.NewMemory()
	err := s.Create(context.Background(), poolstore.Pool{
		Name:                   "bad-dns",
		IsolationProfile:       isolation.ProfileStandard,
		AllowStandardIsolation: true,
		DNSPolicy:              "kube-system",
	})
	if err == nil {
		t.Fatal("unrecognised dnsPolicy should be rejected (§13.2)")
	}
}

// TestUpdateRejectsDNSOptOutOnNonStandard guards the §13.2 cross-control
// on the mutate path, not just create.
// spec: 13.2 (per-pool DNS opt-out)
func TestUpdateRejectsDNSOptOutOnNonStandard(t *testing.T) {
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), poolstore.Pool{
		Name:             "sandboxed-pool",
		IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := s.Update(context.Background(), "sandboxed-pool", func(p *poolstore.Pool) error {
		p.DNSPolicy = poolstore.DNSPolicyClusterDefault
		return nil
	})
	if err == nil {
		t.Fatal("opting a sandboxed pool out of dedicated DNS should be rejected (§13.2)")
	}
}

func TestCreateRejectsNegativeCounts(t *testing.T) {
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), poolstore.Pool{Name: "a", WarmCount: -1}); err == nil {
		t.Error("WarmCount=-1 should fail")
	}
	if err := s.Create(context.Background(), poolstore.Pool{Name: "b", MaxSessionAgeSeconds: -1}); err == nil {
		t.Error("MaxSessionAgeSeconds=-1 should fail")
	}
}

func TestUpdateAdvancesTimestamp(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})
	row, _ := s.Get(context.Background(), "p")
	prev := row.UpdatedAt
	updated, err := s.Update(context.Background(), "p", func(p *poolstore.Pool) error {
		p.WarmCount = 5
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.WarmCount != 5 || !updated.UpdatedAt.After(prev) {
		t.Errorf("Update: %+v", updated)
	}
}

func TestUpdateRejectsBadValues(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})
	_, err := s.Update(context.Background(), "p", func(p *poolstore.Pool) error {
		p.WarmCount = -2
		return nil
	})
	if err == nil {
		t.Error("Update with bad WarmCount should fail")
	}
}

func TestListFilterByRuntime(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "a", RuntimeRef: "echo", IsolationProfile: isolation.ProfileSandboxed})
	_ = s.Create(context.Background(), poolstore.Pool{Name: "b", RuntimeRef: "claude", IsolationProfile: isolation.ProfileSandboxed})
	rows, _ := s.List(context.Background(), poolstore.ListFilter{RuntimeRef: "echo"})
	if len(rows) != 1 || rows[0].Name != "a" {
		t.Errorf("List: %+v", rows)
	}
}

func TestSoftDeleteExcludesByDefault(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})
	_ = s.SoftDelete(context.Background(), "p", time.Now())
	rows, _ := s.List(context.Background(), poolstore.ListFilter{})
	if len(rows) != 0 {
		t.Errorf("default list should exclude deleted: %+v", rows)
	}
	all, _ := s.List(context.Background(), poolstore.ListFilter{IncludeDeleted: true})
	if len(all) != 1 {
		t.Errorf("includeDeleted list: %d", len(all))
	}
}

func TestSoftDeleteIdempotent(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})
	first := time.Now()
	if err := s.SoftDelete(context.Background(), "p", first); err != nil {
		t.Fatalf("SoftDelete 1: %v", err)
	}
	if err := s.SoftDelete(context.Background(), "p", first.Add(time.Hour)); err != nil {
		t.Errorf("SoftDelete 2: %v", err)
	}
	row, _ := s.Get(context.Background(), "p")
	if !row.DeletedAt.Equal(first) {
		t.Errorf("DeletedAt overwritten: got %v want %v", row.DeletedAt, first)
	}
}

func TestGetMissing(t *testing.T) {
	s := poolstore.NewMemory()
	if _, err := s.Get(context.Background(), "missing"); !errors.Is(err, poolstore.ErrNotFound) {
		t.Errorf("Get missing: %v", err)
	}
}

func TestValidateName(t *testing.T) {
	for _, n := range []string{"a", "default-pool", "p_1"} {
		if err := poolstore.ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q): %v", n, err)
		}
	}
	for _, n := range []string{"", "With-Caps", "-leading"} {
		if err := poolstore.ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q) should fail", n)
		}
	}
}

// spec: 5.2 (service mode)
// TestValidateServiceConfig covers the §5.2 service-mode admission rules:
// a service pool requires maxConcurrent >= 1 and must not carry a
// sessionPolicy, and maxConcurrent is rejected on a non-service pool.
func TestValidateServiceConfig(t *testing.T) {
	cases := []struct {
		name string
		pool poolstore.Pool
		ok   bool
	}{
		{
			name: "session-mode pool with no maxConcurrent is valid",
			pool: poolstore.Pool{Name: "p", ExecutionMode: runtimestore.ExecutionModeSession},
			ok:   true,
		},
		{
			name: "session-mode pool with maxConcurrent is rejected",
			pool: poolstore.Pool{Name: "p", ExecutionMode: runtimestore.ExecutionModeSession, MaxConcurrent: 4},
		},
		{
			name: "service pool with maxConcurrent below 1 is rejected",
			pool: poolstore.Pool{Name: "p", ExecutionMode: runtimestore.ExecutionModeService},
		},
		{
			name: "service pool with a sessionPolicy is rejected",
			pool: poolstore.Pool{
				Name: "p", ExecutionMode: runtimestore.ExecutionModeService, MaxConcurrent: 8,
				SessionPolicy: &runtimestore.SessionPolicy{MaxConcurrentSessions: 1},
			},
		},
		{
			name: "service pool with maxConcurrent and no sessionPolicy is valid",
			pool: poolstore.Pool{Name: "p", ExecutionMode: runtimestore.ExecutionModeService, MaxConcurrent: 8},
			ok:   true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := poolstore.ValidateServiceConfig(c.pool)
			if c.ok && err != nil {
				t.Errorf("ValidateServiceConfig: unexpected error %v", err)
			}
			if !c.ok && err == nil {
				t.Error("ValidateServiceConfig: expected a rejection, got nil")
			}
		})
	}
}

// spec: 5.2 (service mode)
// diagnosis: the in-memory poolstore did not run ValidateServiceConfig on
// Create — a service-mode pool without maxConcurrent must be rejected at
// Create time, and a valid service pool must round-trip.
func TestCreateRejectsServiceWithoutMaxConcurrent(t *testing.T) {
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), poolstore.Pool{
		Name: "svc-pool", ExecutionMode: runtimestore.ExecutionModeService,
	}); err == nil {
		t.Error("service-mode pool without maxConcurrent should fail")
	}
}

// spec: 5.2 (service mode)
// diagnosis: a valid service pool must round-trip through Create and Get
// with its maxConcurrent intact.
func TestCreateAdmitsValidServicePool(t *testing.T) {
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), poolstore.Pool{
		Name: "svc-pool", ExecutionMode: runtimestore.ExecutionModeService, MaxConcurrent: 6,
	}); err != nil {
		t.Fatalf("Create valid service pool: %v", err)
	}
	got, err := s.Get(context.Background(), "svc-pool")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.MaxConcurrent != 6 {
		t.Errorf("maxConcurrent did not round-trip: %+v", got)
	}
}

// TestValidateSessionPolicy_spec_5_2 covers the §5.2 session-mode
// sessionPolicy admission rules: the concurrent-session process-level
// acknowledgment, the categorical cross-tenant prohibition for concurrent
// sessions, the per-slot cleanup floor, the recycle best-effort-scrub
// acknowledgment, the required session-count limit, the microvm-only
// sequential cross-tenant gate, and the in-place residual-state
// acknowledgment.
//
// spec: §5.2 (sessionPolicy block, recycle lifecycle, cross-tenant
// prohibition).
func TestValidateSessionPolicy_spec_5_2(t *testing.T) {
	mt := 1
	cases := []struct {
		name    string
		pool    poolstore.Pool
		wantSub string
	}{
		{
			name: "session pool with no sessionPolicy is admitted",
			pool: poolstore.Pool{ExecutionMode: runtimestore.ExecutionModeSession},
		},
		{
			name: "concurrent sessions without the process-level acknowledgment is rejected",
			pool: poolstore.Pool{
				ExecutionMode: runtimestore.ExecutionModeSession,
				SessionPolicy: &runtimestore.SessionPolicy{MaxConcurrentSessions: 4},
			},
			wantSub: "acknowledgeProcessLevelIsolation=true",
		},
		{
			name: "concurrent sessions cleanupTimeoutSeconds below the slot floor is rejected",
			pool: poolstore.Pool{
				ExecutionMode: runtimestore.ExecutionModeSession,
				SessionPolicy: &runtimestore.SessionPolicy{
					MaxConcurrentSessions:            8,
					AcknowledgeProcessLevelIsolation: true,
					CleanupTimeoutSeconds:            30,
				},
			},
			wantSub: "cleanupTimeoutSeconds / maxConcurrentSessions",
		},
		{
			name: "concurrent sessions with cross-tenant reuse is categorically rejected",
			pool: poolstore.Pool{
				IsolationProfile: isolation.ProfileMicrovm,
				ExecutionMode:    runtimestore.ExecutionModeSession,
				SessionPolicy: &runtimestore.SessionPolicy{
					MaxConcurrentSessions:            4,
					AcknowledgeProcessLevelIsolation: true,
					Recycle:                          &runtimestore.RecyclePolicy{AllowCrossTenantReuse: true},
				},
			},
			wantSub: "not permitted when maxConcurrentSessions > 1",
		},
		{
			name: "recycle.enabled without the best-effort-scrub acknowledgment is rejected",
			pool: poolstore.Pool{
				ExecutionMode: runtimestore.ExecutionModeSession,
				SessionPolicy: &runtimestore.SessionPolicy{
					Recycle: &runtimestore.RecyclePolicy{Enabled: true, MaxSessionsPerPod: 10},
				},
			},
			wantSub: "recycle.acknowledgeBestEffortScrub: true",
		},
		{
			name: "recycle.enabled without maxSessionsPerPod is rejected",
			pool: poolstore.Pool{
				ExecutionMode: runtimestore.ExecutionModeSession,
				SessionPolicy: &runtimestore.SessionPolicy{
					Recycle: &runtimestore.RecyclePolicy{Enabled: true, AcknowledgeBestEffortScrub: true},
				},
			},
			wantSub: "recycle.maxSessionsPerPod >= 1",
		},
		{
			name: "in-place scrub without residual-state acknowledgement is rejected",
			pool: poolstore.Pool{
				IsolationProfile: isolation.ProfileMicrovm,
				ExecutionMode:    runtimestore.ExecutionModeSession,
				SessionPolicy: &runtimestore.SessionPolicy{
					Recycle: &runtimestore.RecyclePolicy{
						Enabled:                    true,
						AcknowledgeBestEffortScrub: true,
						MaxSessionsPerPod:          10,
						AllowCrossTenantReuse:      true,
						ScrubProfile:               runtimestore.MicrovmScrubInPlace,
					},
				},
			},
			wantSub: "recycle.acknowledgeMicrovmResidualState: true",
		},
		{
			name: "sequential cross-tenant reuse without microvm is rejected",
			pool: poolstore.Pool{
				IsolationProfile: isolation.ProfileSandboxed,
				ExecutionMode:    runtimestore.ExecutionModeSession,
				SessionPolicy: &runtimestore.SessionPolicy{
					Recycle: &runtimestore.RecyclePolicy{
						Enabled:                    true,
						AcknowledgeBestEffortScrub: true,
						MaxSessionsPerPod:          10,
						AllowCrossTenantReuse:      true,
					},
				},
			},
			wantSub: "permitted only when isolationProfile is microvm",
		},
		{
			// §5.2: the standard in-guest scrub is insufficient for
			// cross-tenant sequential reuse; the pool controller rejects it.
			name: "cross-tenant reuse with an unset scrubProfile is rejected",
			pool: poolstore.Pool{
				IsolationProfile: isolation.ProfileMicrovm,
				ExecutionMode:    runtimestore.ExecutionModeSession,
				SessionPolicy: &runtimestore.SessionPolicy{
					Recycle: &runtimestore.RecyclePolicy{
						Enabled:                    true,
						AcknowledgeBestEffortScrub: true,
						MaxSessionsPerPod:          10,
						AllowCrossTenantReuse:      true,
					},
				},
			},
			wantSub: "requires recycle.scrubProfile vm-restart or in-place",
		},
		{
			name: "cross-tenant reuse with the standard scrubProfile is rejected",
			pool: poolstore.Pool{
				IsolationProfile: isolation.ProfileMicrovm,
				ExecutionMode:    runtimestore.ExecutionModeSession,
				SessionPolicy: &runtimestore.SessionPolicy{
					Recycle: &runtimestore.RecyclePolicy{
						Enabled:                    true,
						AcknowledgeBestEffortScrub: true,
						MaxSessionsPerPod:          10,
						AllowCrossTenantReuse:      true,
						ScrubProfile:               runtimestore.MicrovmScrubStandard,
					},
				},
			},
			wantSub: "requires recycle.scrubProfile vm-restart or in-place",
		},
		{
			name: "cross-tenant reuse with the vm-restart scrubProfile is admitted",
			pool: poolstore.Pool{
				IsolationProfile: isolation.ProfileMicrovm,
				ExecutionMode:    runtimestore.ExecutionModeSession,
				SessionPolicy: &runtimestore.SessionPolicy{
					Recycle: &runtimestore.RecyclePolicy{
						Enabled:                    true,
						AcknowledgeBestEffortScrub: true,
						MaxSessionsPerPod:          10,
						AllowCrossTenantReuse:      true,
						ScrubProfile:               runtimestore.MicrovmScrubVMRestart,
					},
				},
			},
		},
		{
			name: "unrecognised scrubProfile is rejected",
			pool: poolstore.Pool{
				ExecutionMode: runtimestore.ExecutionModeSession,
				SessionPolicy: &runtimestore.SessionPolicy{
					Recycle: &runtimestore.RecyclePolicy{
						Enabled: true, AcknowledgeBestEffortScrub: true, MaxSessionsPerPod: 10,
						ScrubProfile: "wipe",
					},
				},
			},
			wantSub: "recycle.scrubProfile is not a recognised",
		},
		{
			name: "unrecognised onScrubFailure is rejected",
			pool: poolstore.Pool{
				ExecutionMode: runtimestore.ExecutionModeSession,
				SessionPolicy: &runtimestore.SessionPolicy{
					Recycle: &runtimestore.RecyclePolicy{
						Enabled: true, AcknowledgeBestEffortScrub: true, MaxSessionsPerPod: 10,
						OnScrubFailure: "boom",
					},
				},
			},
			wantSub: "recycle.onScrubFailure is not a recognised",
		},
		{
			name: "unrecognised onPoolExhausted is rejected",
			pool: poolstore.Pool{
				ExecutionMode: runtimestore.ExecutionModeSession,
				SessionPolicy: &runtimestore.SessionPolicy{OnPoolExhausted: "drop"},
			},
			wantSub: "onPoolExhausted is not a recognised",
		},
		{
			name: "full session policy is admitted",
			pool: poolstore.Pool{
				IsolationProfile: isolation.ProfileMicrovm,
				ExecutionMode:    runtimestore.ExecutionModeSession,
				SessionPolicy: &runtimestore.SessionPolicy{
					MaxConcurrentSessions: 1,
					CleanupCommands:       []string{"rm -rf /tmp/x"},
					CleanupTimeoutSeconds: 30,
					MaxSessionRetries:     &mt,
					OnPoolExhausted:       runtimestore.PoolExhaustedQueue,
					MaxQueueWaitSeconds:   30,
					Recycle: &runtimestore.RecyclePolicy{
						Enabled:                         true,
						AcknowledgeBestEffortScrub:      true,
						AllowCrossTenantReuse:           true,
						ScrubProfile:                    runtimestore.MicrovmScrubInPlace,
						AcknowledgeMicrovmResidualState: true,
						OnScrubFailure:                  runtimestore.CleanupFailureFail,
						MaxScrubFailures:                3,
						MaxSessionsPerPod:               50,
						MaxPodUptimeSeconds:             3600,
					},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := poolstore.ValidateSessionPolicy(tc.pool)
			if tc.wantSub == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestCreatePersistsSessionPolicy verifies Memory.Create stores a session
// policy block and Get returns the persisted shape, isolated from caller
// mutation.
//
// spec: §5.2 (sessionPolicy block, recycle lifecycle).
func TestCreatePersistsSessionPolicy(t *testing.T) {
	ctx := context.Background()
	store := poolstore.NewMemory()
	if err := store.Create(ctx, poolstore.Pool{
		Name:          "sp",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			CleanupCommands: []string{"pkill -f jupyter_kernel"},
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          20,
			},
		},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.Get(ctx, "sp")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SessionPolicy == nil || got.SessionPolicy.Recycle == nil {
		t.Fatal("session policy not persisted")
	}
	if got.SessionPolicy.Recycle.MaxSessionsPerPod != 20 {
		t.Errorf("maxSessionsPerPod: %d", got.SessionPolicy.Recycle.MaxSessionsPerPod)
	}
	if len(got.SessionPolicy.CleanupCommands) != 1 || got.SessionPolicy.CleanupCommands[0] != "pkill -f jupyter_kernel" {
		t.Errorf("cleanup commands: %#v", got.SessionPolicy.CleanupCommands)
	}
	// Mutating the returned slice must not leak into the store.
	got.SessionPolicy.CleanupCommands[0] = "MUTATED"
	again, _ := store.Get(ctx, "sp")
	if again.SessionPolicy.CleanupCommands[0] == "MUTATED" {
		t.Error("Create shared the cleanup-command slice with the caller")
	}
}

func TestValidateCrossTenantReuseTier_spec_5_2_396(t *testing.T) {
	cases := []struct {
		name string
		pool poolstore.Pool
		tier runtimestore.WorkspaceTier
		ok   bool
	}{
		{
			name: "T4 runtime with cross-tenant reuse is rejected",
			pool: crossTenantPool(true),
			tier: runtimestore.WorkspaceTierT4,
		},
		{
			name: "T4 runtime without cross-tenant reuse is allowed",
			pool: crossTenantPool(false),
			tier: runtimestore.WorkspaceTierT4,
			ok:   true,
		},
		{
			name: "T3 runtime with cross-tenant reuse is allowed",
			pool: crossTenantPool(true),
			tier: runtimestore.WorkspaceTierT3,
			ok:   true,
		},
		{
			name: "empty tier (implicit T3) with cross-tenant reuse is allowed",
			pool: crossTenantPool(true),
			tier: "",
			ok:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := poolstore.ValidateCrossTenantReuseTier(tc.pool, tc.tier)
			if tc.ok && err != nil {
				t.Fatalf("want nil, got %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("want rejection, got nil")
				}
				// The error string is verbatim from §5.2 line 396.
				want := "allowCrossTenantReuse: true is not permitted for T4-tier pools " +
					"(workspaceTier: T4); T4 workloads require dedicated node pools (Section 6.4)"
				if err.Error() != want {
					t.Errorf("error string drifted from spec:\n got:  %q\n want: %q", err.Error(), want)
				}
			}
		})
	}
}

// crossTenantPool builds a §5.2 sequential-reuse pool that does or does not
// request cross-tenant reuse, exercising the relocation of the flag onto
// sessionPolicy.recycle.
func crossTenantPool(crossTenant bool) poolstore.Pool {
	return poolstore.Pool{
		Name:          "p",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			Recycle: &runtimestore.RecyclePolicy{AllowCrossTenantReuse: crossTenant},
		},
	}
}
