// SPDX-License-Identifier: MIT

package openapi_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/correctionstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/deploymentconfigstore"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/erasurejob"
	"github.com/lennylabs/lenny/pkg/gateway/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/externaladapterstore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptorstore"
	"github.com/lennylabs/lenny/pkg/gateway/openapi"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
)

// TestDocumentMatchesRegisteredEndpoints enforces the §15.1 OpenAPI
// completeness requirement (§15.1: every RBAC admin endpoint MUST appear
// in the served document, from which the `/mcp/management` tool inventory
// is generated). Rather than a hardcoded allowlist that drifts silently
// when a route is added, it enumerates the live serve mux's registered
// route templates — admin and non-admin — and asserts each one is present
// in openapi.json. A registered-but-undocumented route fails the test.
//
// The C9 snake_case → camelCase route-parameter rename of the live mux is
// a later build step; until it lands the live mux still registers
// snake_case parameter names (`{user_id}`, `{credential_ref}`, ...) while
// the document already uses the renamed camelCase form (the spec §15.1
// tables were renamed in S6). The comparison therefore normalizes a
// path's parameter-name casing so it matches structurally on the route
// rather than the casing. C9's tier-3 assertion pins the exact casing
// agreement once the live routes are renamed.
//
// spec: §15.1 (OpenAPI completeness).
// diagnosis: A route the gateway serve mux registers is absent from the
// served openapi.json, so the §13 openapi-to-mcp generator emits no MCP
// tool, scope entry, or SDK surface for it. The named route is registered
// but undocumented; add its path entry to openapi.json.
func TestDocumentMatchesRegisteredEndpoints(t *testing.T) {
	doc := openapi.Document()
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	paths, _ := parsed["paths"].(map[string]any)
	documented := map[string]bool{}
	for p := range paths {
		documented[normalizePathParams(p)] = true
	}

	registered := registeredRouteTemplates(t)
	if len(registered) == 0 {
		t.Fatal("no registered route templates enumerated — the live mux walk returned nothing")
	}
	for _, route := range registered {
		if !documented[normalizePathParams(route)] {
			t.Errorf("registered route %q is absent from openapi.json", route)
		}
	}

	// The session, blob, usage, metering, OAuth, and OpenAI-compatible
	// routes register on the main gateway mux (cmd/lenny-gateway) with
	// production dependencies the unit test cannot construct, so the mux
	// walk above does not reach them. Assert their presence explicitly so
	// the document stays complete for the top-level surfaces, matching the
	// coverage the prior allowlist gave these routes.
	for _, route := range topLevelRoutes {
		if !documented[normalizePathParams(route)] {
			t.Errorf("top-level route %q is absent from openapi.json", route)
		}
	}
}

// topLevelRoutes are the non-admin gateway routes registered on the main
// mux (cmd/lenny-gateway) rather than the admin router or credential
// server, so the serve-mux walk does not enumerate them.
var topLevelRoutes = []string{
	"/healthz",
	"/metrics",
	"/mcp",
	"/v1/sessions",
	"/v1/sessions/start",
	"/v1/sessions/{id}",
	"/v1/sessions/{id}/finalize",
	"/v1/sessions/{id}/start",
	"/v1/sessions/{id}/interrupt",
	"/v1/sessions/{id}/terminate",
	"/v1/sessions/{id}/resume",
	"/v1/sessions/{id}/derive",
	"/v1/sessions/{id}/upload",
	"/v1/sessions/{id}/messages",
	"/v1/sessions/{id}/transcript",
	"/v1/sessions/{id}/tree",
	"/v1/sessions/{id}/workspace",
	"/v1/sessions/{id}/setup-output",
	"/v1/sessions/{id}/events",
	"/v1/sessions/{id}/extend-retention",
	"/v1/sessions/{id}/eval",
	"/v1/blobs/{ref}",
	"/v1/usage",
	"/v1/metering/events",
	"/v1/chat/completions",
	"/v1/responses",
	"/v1/responses/{id}",
	"/v1/oauth/token",
}

// registeredRouteTemplates builds the admin router (fully wired so every
// conditional route registers) and the user-facing credential server, then
// walks each live serve mux to extract the path templates it registers.
func registeredRouteTemplates(t *testing.T) []string {
	t.Helper()
	clock := func() time.Time { return time.Unix(0, 0).UTC() }

	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: clock}).
		WithRuntimes(runtimestore.NewMemory()).
		WithPools(poolstore.NewMemory()).
		WithUsers(userstore.NewMemory()).
		WithConnectors(connectorstore.NewMemory()).
		WithEnvironments(environmentstore.NewMemory()).
		WithExperiments(experimentstore.NewMemory()).
		WithEvalResults(evalstore.NewMemory(0, clock)).
		WithDelegationPolicies(delegationpolicystore.NewMemory()).
		WithInterceptors(interceptorstore.NewMemory(), 0).
		WithCustomRoles(customrolestore.NewMemory()).
		WithCredentialPools(credentialpoolstore.NewMemory()).
		WithExternalAdapters(externaladapterstore.NewMemory(), nil).
		WithTenantAccess(tenantaccessstore.NewMemory()).
		WithBreakers(breakerstore.NewMemory()).
		WithRuntimeCapabilityOverrides(runtimecapoverride.NewMemory()).
		WithDeploymentConfig(deploymentconfigstore.NewMemory()).
		WithErasureSaltRotation(saltRotatorStub{}).
		WithErasure(erasureRunnerStub{}, erasurejob.NewMemory()).
		WithImpersonation(impersonationStub{}).
		WithBillingCorrections(billingstore.NewMemory(), correctionstore.NewMemory(), 0).
		WithMigrationManager(migrationStub{}).
		WithEventBuffer(eventBufferStub{}).
		WithOperationsInventory(operationsStub{}).
		WithSessionAdmin(sessionAdminStub{}).
		WithRecommendations(recommendationsStub{}).
		WithCARotation(caRotationStub{}).
		WithRuntimeUpgrade(runtimeUpgradeStub{}).
		WithCredentialRekey(credentialRekeyStub{}).
		WithConnectorRefresh(connectorRefreshStub{}, nil).
		WithReconciliationResumer(reconciliationResumerStub{}).
		WithLeaseDenials(leaseDenialStub{}).
		WithQuotaReconciler(quotaReconcilerStub{}).
		WithPreflight(preflighterStub{}).
		WithAdminTokenProvisioner(adminTokenStub{}).
		WithIssuedTokens(issuedTokenStub{}, revocationCacheStub{}).
		WithArtifactReplication(artifactReplicationStub{}).
		WithArtifactLegalHold(artifactLegalHoldStub{}).
		WithAuditChains(audit.NewChainSet()).
		WithAuditPruner(auditPrunerStub{}).
		WithPlatformInfo(admin.PlatformInfo{}, map[string]string{})

	cred := credentialserver.New(credentialstore.NewMemory(clock))

	var out []string
	out = append(out, walkServeMux(t, router.Handler())...)
	out = append(out, walkServeMux(t, cred.Handler())...)
	sort.Strings(out)
	return out
}

// pathParamCasing matches a `{paramName}` template segment.
var pathParamCasing = regexp.MustCompile(`\{[^}]+\}`)

// normalizePathParams replaces every `{param}` segment with a fixed `{}`
// placeholder so two route templates that differ only in their
// path-parameter names compare equal. This bridges the transient gap
// where the live mux registers snake_case parameters that the document
// already documents under their camelCase rename (see the test doc).
func normalizePathParams(path string) string {
	return pathParamCasing.ReplaceAllString(path, "{}")
}

// walkServeMux extracts the registered METHOD-and-path patterns from an
// *http.ServeMux by reflecting over its routing tree, and returns each
// pattern's path component (the method is dropped so a path that carries
// several verbs collapses to one entry). The Go standard library does not
// expose the registered patterns, so this reaches into the unexported
// routing tree. It is test-only and fails loudly if the tree structure
// shifts under a future Go release, which is the signal to update the walk
// rather than a silent miss.
func walkServeMux(t *testing.T, h http.Handler) []string {
	t.Helper()
	mux, ok := h.(*http.ServeMux)
	if !ok {
		t.Fatalf("handler is %T, not *http.ServeMux", h)
	}
	tree := reflect.ValueOf(mux).Elem().FieldByName("tree")
	if !tree.IsValid() {
		t.Fatal("http.ServeMux has no `tree` field — the routing-tree layout changed; update the walk")
	}
	patterns := map[string]bool{}
	walkRoutingNode(t, tree, patterns)
	out := make([]string, 0, len(patterns))
	for p := range patterns {
		out = append(out, p)
	}
	return out
}

// walkRoutingNode recurses the http.routingNode tree, collecting the path
// component of every node that carries a registered pattern.
func walkRoutingNode(t *testing.T, node reflect.Value, out map[string]bool) {
	if node.Kind() == reflect.Ptr {
		if node.IsNil() {
			return
		}
		node = node.Elem()
	}
	if pat := node.FieldByName("pattern"); pat.IsValid() && !pat.IsNil() {
		str := pat.Elem().FieldByName("str")
		if !str.IsValid() {
			t.Fatal("http.pattern has no `str` field — the pattern layout changed; update the walk")
		}
		// str is "METHOD /path" or "/path"; keep the path component.
		full := str.String()
		if i := strings.IndexByte(full, ' '); i >= 0 {
			full = full[i+1:]
		}
		out[full] = true
	}
	children := node.FieldByName("children")
	if children.IsValid() {
		if s := children.FieldByName("s"); s.IsValid() {
			for i := 0; i < s.Len(); i++ {
				walkRoutingNode(t, s.Index(i).FieldByName("value"), out)
			}
		}
		if m := children.FieldByName("m"); m.IsValid() && !m.IsNil() {
			for _, k := range m.MapKeys() {
				walkRoutingNode(t, m.MapIndex(k), out)
			}
		}
	}
	for _, child := range []string{"multiChild", "emptyChild"} {
		if c := node.FieldByName(child); c.IsValid() {
			walkRoutingNode(t, c, out)
		}
	}
}
