// SPDX-License-Identifier: MIT

package rules

import "testing"

// spec: §25.3 lines 443-451 — every alert the §25.3 health derivation maps
// to a component must name a real rule in the §16.5 catalogue, or the
// overlay silently never fires when the rule is renamed.
func TestHealthComponentMapReferencesRealRules_spec_25_3_443(t *testing.T) {
	known := map[string]bool{}
	for _, r := range Catalog() {
		known[r.Name] = true
	}
	for alertName := range healthComponents {
		if !known[alertName] {
			t.Errorf("healthComponents maps %q which is not a rule in Catalog()", alertName)
		}
	}
}

// spec: §25.3 lines 443-451 — the mapping must resolve to one of the
// gateway's registered health-component names.
func TestHealthComponentMapTargetsAreKnownComponents_spec_25_3_443(t *testing.T) {
	valid := map[string]bool{
		HealthComponentPostgres:            true,
		HealthComponentRedis:               true,
		HealthComponentObjectStore:         true,
		HealthComponentCertManager:         true,
		HealthComponentGateway:             true,
		HealthComponentCircuitBreakerCache: true,
	}
	for alertName, comp := range healthComponents {
		if !valid[comp] {
			t.Errorf("alert %q maps to unknown component %q", alertName, comp)
		}
	}
}

// spec: §25.7 ("`components` — maps to the component names used in the
// health API response (`warmPools`, `postgres`, `redis`, `gateway`,
// `objectStore`, `certManager`, `credentialPools`, `controllers`,
// `circuitBreakers`)."); §25.8 ("The `certManager` component in the
// health response reports: ..."). Both sections name the §25.3
// GET /v1/admin/health component key in camelCase. HealthComponentCertManager
// is the single source of truth the gateway wiring (cmd/lenny-gateway) and
// the runbook `components:` front matter (docs/runbooks/cert-manager-outage.md,
// docs/runbooks/ca-rotation.md — both already spelled camelCase) key off; a
// value that drifts from the spec's `certManager` breaks the §25.7 Path B
// component lookup (`GET /v1/admin/runbooks?component=<health key>`) for
// this component specifically, because the runbook index matches by exact
// string equality against the front matter (pkg/ops/runbooks/index.go).
func TestHealthComponentCertManagerMatchesSpecName_spec_25_7(t *testing.T) {
	const wantSpecName = "certManager"
	if HealthComponentCertManager != wantSpecName {
		t.Errorf("HealthComponentCertManager = %q, want %q (spec §25.7/§25.8 component name)", HealthComponentCertManager, wantSpecName)
	}
}

func TestHealthComponentFor_spec_25_3_443(t *testing.T) {
	// A mapped critical dependency alert resolves to its component.
	if got, ok := HealthComponentFor("SessionStoreUnavailable"); !ok || got != HealthComponentPostgres {
		t.Errorf("SessionStoreUnavailable => (%q, %v), want (%q, true)", got, ok, HealthComponentPostgres)
	}
	if got, ok := HealthComponentFor("MinIOUnavailable"); !ok || got != HealthComponentObjectStore {
		t.Errorf("MinIOUnavailable => (%q, %v), want (%q, true)", got, ok, HealthComponentObjectStore)
	}
	if got, ok := HealthComponentFor("CertExpiryImminent"); !ok || got != HealthComponentCertManager {
		t.Errorf("CertExpiryImminent => (%q, %v), want (%q, true)", got, ok, HealthComponentCertManager)
	}
	// A capacity/policy alert is deliberately unmapped so it does not flip
	// a dependency component's verdict.
	if got, ok := HealthComponentFor("WarmPoolExhausted"); ok {
		t.Errorf("WarmPoolExhausted should be unmapped, got component %q", got)
	}
	if _, ok := HealthComponentFor("NoSuchAlert"); ok {
		t.Error("unknown alert name should not resolve a component")
	}
}
