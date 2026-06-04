// SPDX-License-Identifier: MIT

package registryservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/ops/registryservice"
)

func fixedClock() func() time.Time {
	t := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func baseConfig() registryservice.EffectiveConfig {
	return registryservice.EffectiveConfig{
		URL:            "ghcr.io/lennylabs",
		PullSecretName: "lenny-pull",
		RequireDigest:  false,
	}
}

// spec: §25.8 line 3362 — with no runtime override the effective config is
// the chart base, sourced from helm.
func TestEffective_BaseWhenNoOverride_spec_25_8(t *testing.T) {
	svc := registryservice.New(registryservice.Options{Base: baseConfig(), Store: registryservice.NewMemoryStore()})
	cfg, err := svc.Effective(context.Background())
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if cfg.URL != "ghcr.io/lennylabs" || cfg.Source != "helm" {
		t.Fatalf("effective = %+v", cfg)
	}
	if cfg.PullSecretName != "lenny-pull" {
		t.Errorf("pull secret name lost: %q", cfg.PullSecretName)
	}
}

// spec: §25.8 line 3362 — a runtime PUT overlays the base and takes effect
// on the next read; the pull-secret name is returned but never a value.
func TestUpdate_OverlaysBaseAndAudits_spec_25_8(t *testing.T) {
	var events []registryservice.AuditEvent
	svc := registryservice.New(registryservice.Options{
		Base:  baseConfig(),
		Store: registryservice.NewMemoryStore(),
		Audit: func(e registryservice.AuditEvent) { events = append(events, e) },
		Now:   fixedClock(),
	})
	cfg, err := svc.Update(context.Background(), registryservice.UpdateRequest{
		URL:            "my-registry.internal/lenny",
		PullSecretName: "internal-pull",
		RequireDigest:  true,
		Actor:          "alice@acme.com",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if cfg.URL != "my-registry.internal/lenny" || cfg.Source != "postgres" || !cfg.RequireDigest {
		t.Fatalf("updated cfg = %+v", cfg)
	}
	// The next Effective read returns the override.
	got, _ := svc.Effective(context.Background())
	if got.URL != "my-registry.internal/lenny" || got.Source != "postgres" {
		t.Fatalf("post-update effective = %+v", got)
	}
	if len(events) != 1 || events[0].Type != string(audit.EventPlatformRegistryUpdated) {
		t.Fatalf("audit events = %+v", events)
	}
	if events[0].Actor != "alice@acme.com" {
		t.Errorf("audit actor = %q", events[0].Actor)
	}
}

// spec: §25.8 — an update with neither a url nor overrides is rejected.
func TestUpdate_RejectsEmpty_spec_25_8(t *testing.T) {
	svc := registryservice.New(registryservice.Options{Base: baseConfig(), Store: registryservice.NewMemoryStore()})
	if _, err := svc.Update(context.Background(), registryservice.UpdateRequest{}); err != registryservice.ErrNoBase {
		t.Fatalf("Update empty err = %v, want ErrNoBase", err)
	}
}

// spec: §25.8 — with no store the registry is read-only; PUT is rejected.
func TestUpdate_ReadOnlyWithoutStore_spec_25_8(t *testing.T) {
	svc := registryservice.New(registryservice.Options{Base: baseConfig()})
	if _, err := svc.Update(context.Background(), registryservice.UpdateRequest{URL: "x/y"}); err != registryservice.ErrReadOnly {
		t.Fatalf("Update no-store err = %v, want ErrReadOnly", err)
	}
	// Effective still returns the base.
	cfg, _ := svc.Effective(context.Background())
	if cfg.URL != "ghcr.io/lennylabs" {
		t.Errorf("read-only effective = %+v", cfg)
	}
}

// spec: §25.8 lines 3352-3358 — the image plan resolves every component
// against the base with the target version as the tag.
func TestResolveImagePlan_TagForm_spec_25_8(t *testing.T) {
	svc := registryservice.New(registryservice.Options{Base: baseConfig(), Store: registryservice.NewMemoryStore()})
	plan, err := svc.ResolveImagePlan(context.Background(), "1.6.0", nil)
	if err != nil {
		t.Fatalf("ResolveImagePlan: %v", err)
	}
	want := map[string]string{
		"gateway":     "ghcr.io/lennylabs/lenny-gateway:1.6.0",
		"ops":         "ghcr.io/lennylabs/lenny-ops:1.6.0",
		"controllers": "ghcr.io/lennylabs/lenny-controllers:1.6.0",
		"backup":      "ghcr.io/lennylabs/lenny-backup:1.6.0",
	}
	for k, v := range want {
		if plan[k] != v {
			t.Errorf("plan[%s] = %q, want %q", k, plan[k], v)
		}
	}
}

// spec: §25.8 line 3406 — when requireDigest is set the plan pins by digest
// rather than tag; a missing digest is an unresolvable plan.
func TestResolveImagePlan_DigestForm_spec_25_8(t *testing.T) {
	base := baseConfig()
	base.RequireDigest = true
	svc := registryservice.New(registryservice.Options{Base: base, Store: registryservice.NewMemoryStore()})
	digests := map[string]string{
		"gateway":     "aaa",
		"ops":         "sha256:bbb",
		"controllers": "ccc",
		"backup":      "ddd",
	}
	plan, err := svc.ResolveImagePlan(context.Background(), "1.6.0", digests)
	if err != nil {
		t.Fatalf("ResolveImagePlan digest: %v", err)
	}
	if plan["gateway"] != "ghcr.io/lennylabs/lenny-gateway@sha256:aaa" {
		t.Errorf("gateway digest ref = %q", plan["gateway"])
	}
	if plan["ops"] != "ghcr.io/lennylabs/lenny-ops@sha256:bbb" {
		t.Errorf("ops digest ref = %q (sha256 prefix should not double)", plan["ops"])
	}
	// A missing digest under requireDigest fails resolution.
	if _, err := svc.ResolveImagePlan(context.Background(), "1.6.0", map[string]string{"gateway": "aaa"}); err == nil {
		t.Errorf("ResolveImagePlan with missing digests should fail")
	}
}

// spec: §25.8 lines 3348-3349 — a per-component override wins over the base
// path.
func TestResolveImagePlan_OverrideWins_spec_25_8(t *testing.T) {
	base := baseConfig()
	base.Overrides = map[string]string{"gateway": "mirror.internal/gw:pinned"}
	svc := registryservice.New(registryservice.Options{Base: base, Store: registryservice.NewMemoryStore()})
	plan, err := svc.ResolveImagePlan(context.Background(), "1.6.0", nil)
	if err != nil {
		t.Fatalf("ResolveImagePlan: %v", err)
	}
	if plan["gateway"] != "mirror.internal/gw:pinned" {
		t.Errorf("override gateway = %q", plan["gateway"])
	}
	if plan["ops"] != "ghcr.io/lennylabs/lenny-ops:1.6.0" {
		t.Errorf("non-overridden ops = %q", plan["ops"])
	}
}

// Components is the stable component set the preflight resolves.
func TestComponents_StableSet_spec_25_8(t *testing.T) {
	got := registryservice.Components()
	want := []string{"backup", "controllers", "gateway", "ops"}
	if len(got) != len(want) {
		t.Fatalf("Components = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Components = %v, want %v", got, want)
		}
	}
}

// A store error surfaces from Effective so the handler can report the
// §25.8 line 3610 Postgres-down degradation rather than a stale base.
func TestEffective_StoreError_spec_25_8(t *testing.T) {
	svc := registryservice.New(registryservice.Options{Base: baseConfig(), Store: errStore{}})
	if _, err := svc.Effective(context.Background()); err == nil {
		t.Fatalf("Effective should surface the store error")
	}
}

type errStore struct{}

func (errStore) Load(context.Context) (registryservice.Override, bool, error) {
	return registryservice.Override{}, false, context.DeadlineExceeded
}
func (errStore) Save(context.Context, registryservice.Override) error { return nil }
