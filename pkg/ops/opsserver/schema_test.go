// SPDX-License-Identifier: MIT

package opsserver

import (
	"context"
	"sort"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/configservice"
	"github.com/lennylabs/lenny/pkg/ops/driftservice"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	opsevents "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/registryservice"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
)

// stubGatewayConfig is a no-op configservice.GatewayConfig so the platform
// config routes register in the fully-wired mux the drift guard walks.
type stubGatewayConfig struct{}

func (stubGatewayConfig) GetConfig(context.Context) (map[string]any, error) {
	return map[string]any{}, nil
}

func (stubGatewayConfig) ApplyConfig(context.Context, map[string]any) (bool, error) {
	return false, nil
}

// fullyWiredServer constructs a Server with every dependency that gates
// route registration wired to its memory/stub constructor, so the mux
// registers the complete §25 operability surface. It is the source of truth
// TestRouteSchemasMatchLiveMux pins the RouteSchemas registry against.
func fullyWiredServer(t *testing.T) *Server {
	t.Helper()
	return New(Options{
		Drift:              driftservice.NewService(driftservice.NewMemSnapshotStore(), nil),
		Escalations:        escalation.NewService(nil),
		EventStream:        opsevents.New(opsevents.Options{}),
		EventSubscriptions: eventsubscription.NewService(eventsubscription.NewMemoryStore()),
		Upgrade:            upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore()}),
		UpgradeChecker:     upgradeservice.NewChecker(upgradeservice.CheckerOptions{}),
		VersionAggregator:  upgradeservice.NewVersionAggregator(upgradeservice.VersionAggregatorOptions{}),
		UpgradePreflighter: upgradeservice.NewPreflighter(upgradeservice.PreflighterOptions{Store: upgradeservice.NewMemoryStore()}),
		PlatformConfig:     configservice.New(configservice.Options{Gateway: stubGatewayConfig{}}),
		Registry:           registryservice.New(registryservice.Options{Store: registryservice.NewMemoryStore()}),
		Inventory:          operations.New(),
	})
}

// TestRouteSchemasMatchLiveMux pins the RouteSchemas registry to the routes
// a fully-wired opsserver mux registers: the registry lists exactly the
// /v1/admin/* operability routes the server serves, in both directions. A
// route added to the opsserver without a registry entry, or a stale registry
// entry for a route the server no longer serves, fails the test. This is the
// F-COV-3 guarantee that the schema-emission surface the openapi.json merge
// reads cannot drift from the served lenny-ops surface, mirroring the
// gateway-mux completeness guarantee.
//
// spec: 25.12 (operability surface, build-time OpenAPI→MCP generation),
// 15.1 (OpenAPI completeness).
// diagnosis: The RouteSchemas registry has drifted from the opsserver mux.
// A lenny-ops route was added or removed without updating opsRouteSchemas,
// so the F-COV-3 openapi.json merge would omit a served route or document a
// route the server no longer serves. Reconcile opsRouteSchemas with the
// register* functions.
func TestRouteSchemasMatchLiveMux(t *testing.T) {
	live := adminRoutePatterns(fullyWiredServer(t).mux)

	registered := map[string]bool{}
	for _, r := range RouteSchemas() {
		key := r.Method + " " + r.Path
		if registered[key] {
			t.Errorf("duplicate registry entry %q", key)
		}
		registered[key] = true
	}

	for key := range live {
		if !registered[key] {
			t.Errorf("opsserver serves %q but RouteSchemas has no entry for it", key)
		}
	}
	for key := range registered {
		if !live[key] {
			t.Errorf("RouteSchemas lists %q but the opsserver mux does not serve it", key)
		}
	}
	if t.Failed() {
		t.Logf("live=%v", sortedKeys(live))
		t.Logf("registry=%v", sortedKeys(registered))
	}
}

// TestRouteSchemasCarryMandatoryExtensions asserts every registry entry
// carries the four 15.1 / 25.12 x-lenny-* fields the merged OpenAPI
// document requires on every documented admin path: a non-empty scope in
// canonical tools:<domain>:<action> form, a non-empty required role, a
// non-empty category, and a success status. x-lenny-mcp-tool MAY be empty
// (a non-tool endpoint), mirroring the gateway-path rule.
//
// spec: 15.1 (x-lenny-* extension contract), 25.12 (operability tools).
// diagnosis: A lenny-ops route schema is missing a mandatory x-lenny-*
// field, so the F-COV-3 merge would emit an admin path that fails the
// gateway per-path invariant test. Fill in the missing field in
// opsRouteSchemas.
func TestRouteSchemasCarryMandatoryExtensions(t *testing.T) {
	for _, r := range RouteSchemas() {
		where := r.Method + " " + r.Path
		if r.Scope == "" {
			t.Errorf("%s: empty x-lenny-scope", where)
		}
		if r.RequiredRole == "" {
			t.Errorf("%s: empty x-lenny-required-role", where)
		}
		if r.RequiredCategory == "" {
			t.Errorf("%s: empty x-lenny-category", where)
		}
		if r.SuccessStatus == "" {
			t.Errorf("%s: empty success status", where)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
