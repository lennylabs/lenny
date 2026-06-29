// SPDX-License-Identifier: MIT

package openapi_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
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
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
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
// The comparison is exact, including path-parameter casing: after the
// snake_case → camelCase route-parameter rename (C9), the live mux and the
// document both use the camelCase form (`{userId}`, `{jobId}`,
// `{toolCallId}`, `{elicitationId}`, `{credentialRef}`), matching the
// §15.1 tables. A casing divergence between a registered route and its
// documented template fails the test rather than being normalized away.
//
// spec: §15.1 (OpenAPI completeness, path-parameter casing).
// diagnosis: A route the gateway serve mux registers is absent from the
// served openapi.json (or documented under a different parameter casing),
// so the §13 openapi-to-mcp generator emits no MCP tool, scope entry, or
// SDK surface for it. The named route is registered but undocumented; add
// its path entry to openapi.json or align its parameter casing.
func TestDocumentMatchesRegisteredEndpoints(t *testing.T) {
	doc := openapi.Document()
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	paths, _ := parsed["paths"].(map[string]any)
	documented := map[string]bool{}
	for p := range paths {
		documented[stripWildcard(p)] = true
	}

	registered := registeredRouteTemplates(t)
	if len(registered) == 0 {
		t.Fatal("no registered route templates enumerated — the live mux walk returned nothing")
	}
	for _, route := range registered {
		if !documented[stripWildcard(route)] {
			t.Errorf("registered route %q is absent from openapi.json (check path-parameter casing)", route)
		}
	}

	// A handful of routes register only on the main gateway mux
	// (cmd/lenny-gateway) and depend on production wiring (the OpenAI-
	// compatible adapter, the Token Service reverse proxy, the health and
	// metrics handlers, the top-level MCP adapter) that the unit test cannot
	// stand up in-process. Those are enumerated from mainMuxRoutes and
	// asserted present; every constructible surface (admin, credential, and
	// session) is walked live above rather than listed.
	for _, route := range mainMuxRoutes {
		if !documented[stripWildcard(route)] {
			t.Errorf("main-mux route %q is absent from openapi.json (check path-parameter casing)", route)
		}
	}
}

// stripWildcard removes the trailing `...` catch-all marker Go's
// http.ServeMux uses on a multi-segment wildcard (`{ref...}`), which
// OpenAPI documents as a plain `{ref}` template. The comparison stays
// case-sensitive on the parameter name so a snake_case/camelCase casing
// divergence still fails the test; only the wildcard-syntax difference is
// normalized away.
func stripWildcard(path string) string {
	return strings.ReplaceAll(path, "...}", "}")
}

// renamedRouteTemplates are the five route templates whose path parameter
// was renamed from snake_case to camelCase (C9). The casing-agreement test
// asserts each renders identically in the §15.1 spec table, openapi.json,
// and the live mux, and that the retired snake_case template no longer
// appears in any of the three.
var renamedRouteTemplates = []struct {
	camel string // the renamed camelCase route template
	snake string // the retired snake_case form
}{
	{"/v1/admin/users/{userId}", "/v1/admin/users/{user_id}"},
	{"/v1/admin/erasure-jobs/{jobId}", "/v1/admin/erasure-jobs/{job_id}"},
	{"/v1/sessions/{id}/tool-use/{toolCallId}/approve", "/v1/sessions/{id}/tool-use/{tool_call_id}/approve"},
	{"/v1/sessions/{id}/elicitations/{elicitationId}/respond", "/v1/sessions/{id}/elicitations/{elicitation_id}/respond"},
	{"/v1/credentials/{credentialRef}", "/v1/credentials/{credential_ref}"},
}

// TestRenamedRoutesAgreeOnCasing pins the C9 casing reconciliation: every
// renamed camelCase route resolves on the live mux, is documented in
// openapi.json under the camelCase template, and the §15.1 spec table no
// longer spells any of the retired snake_case path parameters. The three
// surfaces (live mux, openapi.json, and the §15.1 table) therefore agree on
// path-parameter casing.
//
// spec: §15.1 (path-parameter casing convention), §15.2.1 (REST/MCP
// consistency contract).
// diagnosis: A renamed route is documented or templated under a casing the
// live mux no longer registers, so a generated MCP tool, SDK call, or doc
// example targets a path the gateway will not match. Align the named
// surface's casing with the live mux.
func TestRenamedRoutesAgreeOnCasing(t *testing.T) {
	doc := openapi.Document()
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	paths, _ := parsed["paths"].(map[string]any)
	documented := map[string]bool{}
	for p := range paths {
		documented[p] = true
	}
	registered := map[string]bool{}
	for _, r := range registeredRouteTemplates(t) {
		registered[stripWildcard(r)] = true
	}

	for _, rt := range renamedRouteTemplates {
		if !registered[rt.camel] {
			t.Errorf("renamed route %q does not resolve on the live mux", rt.camel)
		}
		if !documented[rt.camel] {
			t.Errorf("renamed route %q is absent from openapi.json", rt.camel)
		}
		if documented[rt.snake] {
			t.Errorf("retired snake_case route %q is still present in openapi.json", rt.snake)
		}
	}

	// The §15.1 spec table is the contract source. After C9 + S6 it must
	// not spell any retired snake_case path parameter as a route template.
	spec := readSpec(t, "15_external-api-surface.md")
	for _, snake := range []string{
		"{user_id}", "{job_id}", "{tool_call_id}", "{elicitation_id}", "{credential_ref}",
	} {
		// A route template embeds the parameter between path slashes; the
		// audit-payload/JSON field references (`Returns user_id`, `details.user_id`)
		// are not bracketed, so the bracketed form isolates route templates.
		if strings.Contains(spec, snake) {
			t.Errorf("§15.1 spec table still spells the retired snake_case route parameter %q; it must be camelCase", snake)
		}
	}
}

// readSpec loads a spec markdown file from the module's spec/ directory.
func readSpec(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "spec", name))
	if err != nil {
		t.Fatalf("read spec %s: %v", name, err)
	}
	return string(b)
}

// repoRoot walks up from the working directory to the module root (the
// directory containing go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for d := wd; ; {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatalf("no go.mod found above %s", wd)
		}
		d = parent
	}
}

// mainMuxRoutes are the routes that register only on the main gateway mux
// (cmd/lenny-gateway) with production dependencies — the OpenAI-compatible
// adapter mounts, the Token Service OAuth proxy, the top-level MCP adapter,
// and the health and metrics endpoints — so neither the admin router, the
// credential server, nor the session server constructed in-process by
// registeredRouteTemplates registers them. The session, runtime, pool,
// blob, usage, and metering routes are no longer listed here: the live
// session-server mux walk enumerates them, so a register-or-deregister
// drift on those surfaces fails the test rather than passing under a stale
// list.
var mainMuxRoutes = []string{
	"/healthz",
	"/metrics",
	"/mcp",
	"/v1/chat/completions",
	"/v1/responses",
	"/v1/responses/{id}",
	"/v1/oauth/token",
}

// registeredRouteTemplates builds the admin router (fully wired so every
// conditional route registers), the user-facing credential server, and the
// session-facing server, then walks each live serve mux to extract the path
// templates it registers.
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

	// The session-facing serve mux is constructible in-process: New only
	// stores the injected dependencies, and Handler() registers handler
	// method values without invoking them, so a memstore-backed server with
	// an otherwise-zero Options registers the full session route table. This
	// enumerates the non-admin session routes live the same way the admin and
	// credential muxes are walked, rather than from a hand-maintained list.
	sess := sessionserver.New(memstore.New(), sessionserver.Options{Clock: clock})

	var out []string
	out = append(out, walkServeMux(t, router.Handler())...)
	out = append(out, walkServeMux(t, cred.Handler())...)
	out = append(out, walkServeMux(t, sess.Handler())...)
	sort.Strings(out)
	return out
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
	// The admin Router wraps its serve mux in the §25.1 scope-enforcement
	// gate, which embeds and exposes the underlying mux through
	// admin.MuxUnwrapper so route introspection reaches the registered
	// patterns past the gate. Unwrap before the type-assert.
	if u, ok := h.(admin.MuxUnwrapper); ok {
		h = u.Mux()
	}
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
