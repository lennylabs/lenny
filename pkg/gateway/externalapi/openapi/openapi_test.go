// SPDX-License-Identifier: MIT

package openapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/common/scopes"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/openapi"
)

// spec: §15.1 OpenAPI document discovery.

func TestServesYAMLEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	rr := httptest.NewRecorder()
	openapi.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Header().Get("Content-Type") != "application/yaml" {
		t.Errorf("Content-Type: got %q", rr.Header().Get("Content-Type"))
	}
	if rr.Body.Len() == 0 {
		t.Error("body empty")
	}
}

func TestServesJSONEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil)
	rr := httptest.NewRecorder()
	openapi.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if rr.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type: got %q", rr.Header().Get("Content-Type"))
	}
	// Body must parse as JSON.
	var doc map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi version: got %v", doc["openapi"])
	}
}

// spec: §15.1 line 589 — the gateway also serves the JSON form at
// `/openapi.json` (no `/v1` prefix). The same document must come back
// byte-for-byte from both JSON mounts. F-15.1.17.
func TestServesJSONAtCanonicalSpecPath_spec_15_1_589(t *testing.T) {
	for _, path := range []string{"/openapi.json", "/v1/openapi.json"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		openapi.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status: %d", path, rr.Code)
		}
		if got := rr.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("%s Content-Type: got %q", path, got)
		}
		var doc map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
			t.Fatalf("%s body is not valid JSON: %v", path, err)
		}
		if doc["openapi"] != "3.1.0" {
			t.Errorf("%s openapi version: got %v", path, doc["openapi"])
		}
	}
}

// spec: §15.1 (OpenAPI generation and discovery) — F-COV-1. §15.1 lists
// both `/v1/openapi.json` and `/v1/openapi.yaml` as the admin-API
// discovery paths, but the gateway previously served only the JSON form,
// so a consumer following the §25.4 `/me` `links.openApi` YAML variant or
// the §15.1 two-path claim hit a 404. This asserts the gateway now serves
// the `/v1/openapi.yaml` alias with the YAML content type and the same
// document bytes as `/openapi.yaml`; it fails against the pre-fix handler,
// which routed `/v1/openapi.yaml` to the ServeMux 404.
func TestServesYAMLAtV1DiscoveryPath_spec_15_1(t *testing.T) {
	rootReq := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	rootRR := httptest.NewRecorder()
	openapi.Handler().ServeHTTP(rootRR, rootReq)

	req := httptest.NewRequest(http.MethodGet, "/v1/openapi.yaml", nil)
	rr := httptest.NewRecorder()
	openapi.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/v1/openapi.yaml status: %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/yaml" {
		t.Errorf("Content-Type: got %q, want application/yaml", got)
	}
	if rr.Body.Len() == 0 {
		t.Error("body empty")
	}
	if rr.Body.String() != rootRR.Body.String() {
		t.Error("/v1/openapi.yaml must serve the same document bytes as /openapi.yaml")
	}
	// The YAML form is the same document, so it parses as JSON (JSON is a
	// strict subset of YAML 1.2 and the served bytes are JSON).
	var doc map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body is not valid JSON/YAML: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi version: got %v", doc["openapi"])
	}
}

// spec: §15.1 line 589 — the canonical `/openapi.json` mount preserves
// the gateway release version stamped via HandlerWithVersion. F-15.1.17.
func TestCanonicalJSONMountStampsReleaseVersion_spec_15_1_589(t *testing.T) {
	const release = "2.7.0"
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rr := httptest.NewRecorder()
	openapi.HandlerWithVersion(release).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	info, _ := doc["info"].(map[string]any)
	if got := info["version"]; got != release {
		t.Errorf("info.version: got %v, want %q", got, release)
	}
}

// TestAdminEndpointsCarryMCPExtensions implements the §15.1 line 933
// build-time CI check: every admin-API endpoint MUST carry the four
// mandatory `x-lenny-*` extensions (`x-lenny-mcp-tool`,
// `x-lenny-scope`, `x-lenny-required-role`, `x-lenny-category`).
//
// Spec note: `x-lenny-mcp-tool` MAY be set to `null` for endpoints
// purely used for internal component-to-component communication;
// `null` counts as "present" per the spec's parenthetical. We use a
// key-exists probe so the strict check accepts the null sentinel.
// F-15.1.26.
//
// spec: §15.1 lines 923-933 (admin-API MCP extension contract).
func TestAdminEndpointsCarryMCPExtensions_spec_15_1_933(t *testing.T) {
	doc := openapi.Document()
	var parsed map[string]any
	_ = json.Unmarshal(doc, &parsed)
	paths, _ := parsed["paths"].(map[string]any)
	for path, op := range paths {
		if !strings.HasPrefix(path, "/v1/admin/") {
			continue
		}
		methods, _ := op.(map[string]any)
		for method, m := range methods {
			body, _ := m.(map[string]any)
			for _, ext := range []string{
				"x-lenny-mcp-tool", "x-lenny-scope",
				"x-lenny-required-role", "x-lenny-category",
			} {
				if _, exists := body[ext]; !exists {
					t.Errorf("%s %s missing %s (spec §15.1 line 933)", method, path, ext)
					continue
				}
				// `x-lenny-mcp-tool` is the only extension the spec
				// explicitly allows to be `null`. The other three
				// must carry a non-empty string value.
				if ext == "x-lenny-mcp-tool" {
					continue
				}
				v, ok := body[ext].(string)
				if !ok || v == "" {
					t.Errorf("%s %s %s must be a non-empty string, got %v",
						method, path, ext, body[ext])
				}
			}
		}
	}
}

// TestAdminScopesFollowDocumentedSyntax_spec_15_1_933 asserts the
// §15.1 line 933 "additional check" that every `x-lenny-scope` value
// conforms to the canonical `tools:<domain>:<action>` syntax and names
// a domain in the closed §15.1 line 919 taxonomy. The served document
// carries the canonical form for every admin endpoint; the legacy
// `admin.<domain>.<verb>` form is rejected outright. Membership is
// asserted through scopes.ParseScope, which both validates the
// `tools:<domain>:<action>` syntax and rejects any domain absent from
// the closed scopes.Domains taxonomy, so the test doubles as the
// verification that the taxonomy enumerates every served domain.
// spec: §15.1 line 919 (closed scope taxonomy), line 933 (CI syntax contract).
func TestAdminScopesFollowDocumentedSyntax_spec_15_1_933(t *testing.T) {
	doc := openapi.Document()
	var parsed map[string]any
	_ = json.Unmarshal(doc, &parsed)
	paths, _ := parsed["paths"].(map[string]any)

	// The canonical syntax is `tools:<domain>:<action>` with lowercase
	// letters, digits, and underscore in the domain and action, and `*`
	// permitted in the action position (per §15.1 line 922 wildcard).
	// scopes.ParseScope enforces the same grammar plus taxonomy
	// membership; the regex pins the exact admitted character set so a
	// malformed value reports a precise syntax error before the
	// taxonomy check.
	specScope := regexp.MustCompile(`^tools:[a-z0-9_]+:[a-z0-9_*]+$`)

	for path, op := range paths {
		if !strings.HasPrefix(path, "/v1/admin/") {
			continue
		}
		methods, _ := op.(map[string]any)
		for method, m := range methods {
			body, _ := m.(map[string]any)
			raw, ok := body["x-lenny-scope"]
			if !ok || raw == nil {
				// Presence is enforced by the sibling test.
				continue
			}
			scope, ok := raw.(string)
			if !ok {
				t.Errorf("%s %s x-lenny-scope must be a string, got %T",
					method, path, raw)
				continue
			}
			if !specScope.MatchString(scope) {
				t.Errorf("%s %s x-lenny-scope %q does not match canonical `tools:<domain>:<action>`",
					method, path, scope)
				continue
			}
			// The wildcard form `tools:*` is not a route-level scope; a
			// served admin endpoint declares a concrete domain and
			// action. ParseScope rejects every domain absent from the
			// closed taxonomy, so a served scope that parses proves its
			// domain is enumerated.
			if _, err := scopes.ParseScope(scope); err != nil {
				t.Errorf("%s %s x-lenny-scope %q is not in the closed §15.1 taxonomy: %v",
					method, path, scope, err)
			}
		}
	}
}

// TestAdminRequiredRolesAreFromAuthEnum_spec_15_1_933 asserts every
// `x-lenny-required-role` is one of the §10.2 roles the gateway
// admits. The spec line 928 documents only `platform-admin` and
// `tenant-admin`, but in-tree operability endpoints legitimately
// grant `tenant-viewer` (health), `user` (caller-self-view), and
// `billing-viewer` (`/v1/metering/events`). The §10.2 role taxonomy
// is the source-of-truth so the CI guard accepts that broader set.
// spec: §15.1 line 933; §10.2 (Roles).
func TestAdminRequiredRolesAreFromAuthEnum_spec_15_1_933(t *testing.T) {
	allowed := map[string]struct{}{
		"platform-admin": {}, "tenant-admin": {},
		"tenant-viewer": {}, "billing-viewer": {}, "user": {},
	}
	doc := openapi.Document()
	var parsed map[string]any
	_ = json.Unmarshal(doc, &parsed)
	paths, _ := parsed["paths"].(map[string]any)
	for path, op := range paths {
		if !strings.HasPrefix(path, "/v1/admin/") {
			continue
		}
		methods, _ := op.(map[string]any)
		for method, m := range methods {
			body, _ := m.(map[string]any)
			role, _ := body["x-lenny-required-role"].(string)
			if role == "" {
				continue
			}
			if _, ok := allowed[role]; !ok {
				t.Errorf("%s %s x-lenny-required-role %q not in §10.2 enum",
					method, path, role)
			}
		}
	}
}

// spec: §15.1 line 972 / §15.2.1 line 1376 — the published Error
// envelope must require `code`, `category`, `message`, and `retryable`
// so SDK generators surface every field the Go handler emits. F-15.5.7,
// F-15.1.5.
func TestErrorEnvelopeRequiresCategoryAndRetryable_spec_15_1_972(t *testing.T) {
	doc := openapi.Document()
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	schemas, _ := parsed["components"].(map[string]any)["schemas"].(map[string]any)
	errSchema, _ := schemas["Error"].(map[string]any)
	if errSchema == nil {
		t.Fatal("components.schemas.Error missing")
	}
	inner, _ := errSchema["properties"].(map[string]any)["error"].(map[string]any)
	if inner == nil {
		t.Fatal("Error.properties.error missing")
	}
	requiredAny, _ := inner["required"].([]any)
	required := map[string]bool{}
	for _, r := range requiredAny {
		if s, ok := r.(string); ok {
			required[s] = true
		}
	}
	for _, want := range []string{"code", "category", "message", "retryable"} {
		if !required[want] {
			t.Errorf("Error.error.required missing %q (spec §15.1 line 972)", want)
		}
	}
	props, _ := inner["properties"].(map[string]any)
	category, _ := props["category"].(map[string]any)
	if category == nil {
		t.Fatal("Error.error.properties.category missing")
	}
	if category["type"] != "string" {
		t.Errorf("category.type: got %v, want \"string\"", category["type"])
	}
	enumAny, _ := category["enum"].([]any)
	if len(enumAny) == 0 {
		t.Error("category.enum must list the §15.1 canonical category set")
	}
	wantCats := map[string]bool{"VALIDATION": false, "AUTH": false, "TRANSIENT": false, "INTERNAL": false}
	for _, e := range enumAny {
		if s, ok := e.(string); ok {
			if _, ok := wantCats[s]; ok {
				wantCats[s] = true
			}
		}
	}
	for k, present := range wantCats {
		if !present {
			t.Errorf("category.enum missing canonical value %q", k)
		}
	}
	retryable, _ := props["retryable"].(map[string]any)
	if retryable == nil {
		t.Fatal("Error.error.properties.retryable missing")
	}
	if retryable["type"] != "boolean" {
		t.Errorf("retryable.type: got %v, want \"boolean\"", retryable["type"])
	}
}

// spec: §15.1 lines 1228-1253 — the canonical cursor-paginated list
// envelope `{items, cursor, hasMore, total}` is the SDK-generator input
// for GET /v1/sessions, and the cursor/limit/sort query params are
// advertised. F-15.1.6.
func TestSessionsListUsesCanonicalPaginationEnvelope_spec_15_1_1228(t *testing.T) {
	doc := openapi.Document()
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	get, _ := parsed["paths"].(map[string]any)["/v1/sessions"].(map[string]any)["get"].(map[string]any)
	if get == nil {
		t.Fatal("paths./v1/sessions.get missing")
	}
	paramNames := map[string]bool{}
	for _, p := range get["parameters"].([]any) {
		paramNames[p.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"cursor", "limit", "sort"} {
		if !paramNames[want] {
			t.Errorf("GET /v1/sessions must advertise the %q query param", want)
		}
	}
	schema := get["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	required := map[string]bool{}
	for _, r := range schema["required"].([]any) {
		required[r.(string)] = true
	}
	if !required["items"] || !required["hasMore"] {
		t.Errorf("200 schema.required must include items and hasMore, got %v", schema["required"])
	}
	props, _ := schema["properties"].(map[string]any)
	for _, want := range []string{"items", "cursor", "hasMore", "total"} {
		if _, ok := props[want]; !ok {
			t.Errorf("200 schema.properties missing canonical field %q", want)
		}
	}
	if _, legacy := props["sessions"]; legacy {
		t.Error("200 schema must not carry the legacy `sessions` field")
	}
}

func TestDocumentReturnsCopy(t *testing.T) {
	a := openapi.Document()
	b := openapi.Document()
	if &a[0] == &b[0] {
		t.Error("Document must return defensive copies")
	}
}

// spec: §15.1 line 589 — `info.version` field in the spec matches the
// gateway's release version. F-15.1.18.
func TestHandlerStampsGatewayReleaseVersionIntoInfoVersion_spec_15_1_589(t *testing.T) {
	const release = "1.4.2"
	req := httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil)
	rr := httptest.NewRecorder()
	openapi.HandlerWithVersion(release).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	info, _ := doc["info"].(map[string]any)
	if got := info["version"]; got != release {
		t.Errorf("info.version: got %v, want %q", got, release)
	}
}

// spec: §15.1 line 589 — empty / "dev" build version leaves the
// embedded default in place so unconfigured deployments still serve a
// non-empty version string.
func TestHandlerKeepsEmbeddedVersionWhenBuildVersionIsEmptyOrDev_spec_15_1_589(t *testing.T) {
	for _, v := range []string{"", "dev"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/openapi.json", nil)
		rr := httptest.NewRecorder()
		openapi.HandlerWithVersion(v).ServeHTTP(rr, req)
		var doc map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
			t.Fatalf("decode for %q: %v", v, err)
		}
		info, _ := doc["info"].(map[string]any)
		ver, _ := info["version"].(string)
		if ver == "" {
			t.Errorf("info.version unexpectedly empty for buildVersion %q", v)
		}
	}
}

// spec: §9.3 lines 144-158 — the §13 openapi-to-mcp tool generator
// consumes the OpenAPI document; the connector OAuth authorize +
// callback routes must be declared so the OAuth flow surfaces as a
// generated tool and external SDK consumers can see it. F-9.3.14.
func TestConnectorOAuthEndpointsDeclaredInDocument_spec_9_3_157(t *testing.T) {
	doc := openapi.Document()
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	paths, _ := parsed["paths"].(map[string]any)
	authorize, ok := paths["/v1/admin/connectors/{name}/oauth/authorize"].(map[string]any)
	if !ok {
		t.Fatal("missing /v1/admin/connectors/{name}/oauth/authorize")
	}
	if _, ok := authorize["post"]; !ok {
		t.Error("authorize endpoint missing POST verb")
	}
	callback, ok := paths["/v1/admin/connectors/oauth/callback"].(map[string]any)
	if !ok {
		t.Fatal("missing /v1/admin/connectors/oauth/callback")
	}
	if _, ok := callback["get"]; !ok {
		t.Error("callback endpoint missing GET verb")
	}
}

// spec: §15.1 line 589 — version templating preserves the rest of the
// document (paths and MCP-extensions remain intact).
func TestHandlerVersionTemplatingPreservesPaths_spec_15_1_589(t *testing.T) {
	doc := openapi.DocumentWithVersion("9.9.9")
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	paths, _ := parsed["paths"].(map[string]any)
	if _, ok := paths["/v1/sessions"]; !ok {
		t.Error("versioned document dropped /v1/sessions path")
	}
}

// spec: §15.5 item 6 — every operation MUST carry x-lenny-stability so
// consumers can programmatically discover which endpoints are covered
// by the platform's versioning guarantees and which may change without
// notice. The handler stamps `stable` on every operation that lacks an
// explicit override in openapi.json. F-15.5.10.
func TestEveryOperationCarriesStabilityTier_spec_15_5_2447(t *testing.T) {
	doc := openapi.DocumentWithVersion("1.0.0")
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	paths, _ := parsed["paths"].(map[string]any)
	if len(paths) == 0 {
		t.Fatal("paths block is empty")
	}
	verbs := map[string]struct{}{
		"get": {}, "put": {}, "post": {}, "delete": {},
		"options": {}, "head": {}, "patch": {}, "trace": {},
	}
	allowed := map[string]struct{}{
		string(openapi.StabilityStable): {},
		string(openapi.StabilityBeta):   {},
		string(openapi.StabilityAlpha):  {},
	}
	for path, raw := range paths {
		methods, _ := raw.(map[string]any)
		for verb, m := range methods {
			if _, ok := verbs[verb]; !ok {
				continue
			}
			body, _ := m.(map[string]any)
			val, ok := body["x-lenny-stability"]
			if !ok {
				t.Errorf("%s %s missing x-lenny-stability", verb, path)
				continue
			}
			s, _ := val.(string)
			if _, ok := allowed[s]; !ok {
				t.Errorf("%s %s x-lenny-stability %q not in {stable, beta, alpha}", verb, path, s)
			}
		}
	}
}

// enumValues extracts the `enum` array of the schema at the given
// dotted/keyed path under components.schemas and returns it as a set.
// It fatals if any path segment is missing so a structural drift in the
// document surfaces as a clear failure rather than a nil map.
func enumValues(t *testing.T, schema map[string]any) map[string]bool {
	t.Helper()
	raw, ok := schema["enum"].([]any)
	if !ok {
		t.Fatalf("schema carries no enum array: %v", schema)
	}
	out := map[string]bool{}
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("non-string enum value %v", v)
		}
		out[s] = true
	}
	return out
}

// schemas decodes the served document and returns components.schemas.
func schemas(t *testing.T) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal(openapi.Document(), &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	comps, _ := parsed["components"].(map[string]any)
	s, _ := comps["schemas"].(map[string]any)
	if s == nil {
		t.Fatal("components.schemas missing")
	}
	return s
}

// TestExecutionModeEnumIsSessionAndService_spec_5_2 asserts every
// executionMode enum in the served document carries exactly {session,
// service} and never the removed task/concurrent modes, so SDK
// generators and the §13 openapi-to-mcp generator surface only the two
// modes §5.2 defines. spec: §5.2 (execution modes); §7.1; §15.1.
func TestExecutionModeEnumIsSessionAndService_spec_5_2(t *testing.T) {
	want := map[string]bool{"session": true, "service": true}
	s := schemas(t)

	// Runtime.executionMode.
	runtimeProps, _ := s["Runtime"].(map[string]any)["properties"].(map[string]any)
	runtimeMode, _ := runtimeProps["executionMode"].(map[string]any)
	if got := enumValues(t, runtimeMode); !equalStringSet(got, want) {
		t.Errorf("Runtime.executionMode enum: got %v, want %v", got, want)
	}

	// CreateSessionResponse.sessionIsolationLevel.executionMode (nested
	// in the allOf override block).
	isoMode := isolationLevelProp(t, "executionMode")
	if got := enumValues(t, isoMode); !equalStringSet(got, want) {
		t.Errorf("sessionIsolationLevel.executionMode enum: got %v, want %v", got, want)
	}
}

// TestNoRemovedExecutionModeSurfaces_spec_5_2 asserts the document does
// not retain the removed concurrencyStyle property or the task/concurrent
// enum values anywhere, so the removal is complete rather than leaving a
// dead enum a generator could emit. spec: §5.2; §7.1.
func TestNoRemovedExecutionModeSurfaces_spec_5_2(t *testing.T) {
	doc := string(openapi.Document())
	for _, dead := range []string{
		`"concurrencyStyle"`,
		`"taskPolicy"`,
		`"maxTasksPerPod"`,
		`"maxTaskRetries"`,
		`"microvmScrubMode"`,
		`"onCleanupFailure"`,
		`"session","task","concurrent"`,
	} {
		if strings.Contains(doc, dead) {
			t.Errorf("served document still contains removed surface %s", dead)
		}
	}
}

// TestSessionIsolationLevelCarriesConversationContinuity_spec_7_1 asserts
// the §7.1 sessionIsolationLevel object requires and types the new
// conversationContinuity field as a {platform,none} enum, so clients can
// read the no-continuity contract of service mode from the create
// response. spec: §7.1 (session isolation response); §5.2; §15.1.
func TestSessionIsolationLevelCarriesConversationContinuity_spec_7_1(t *testing.T) {
	s := schemas(t)
	resp, _ := s["CreateSessionResponse"].(map[string]any)
	allOf, _ := resp["allOf"].([]any)
	var iso map[string]any
	for _, raw := range allOf {
		block, _ := raw.(map[string]any)
		props, _ := block["properties"].(map[string]any)
		if level, ok := props["sessionIsolationLevel"].(map[string]any); ok {
			iso = level
		}
	}
	if iso == nil {
		t.Fatal("sessionIsolationLevel block not found in CreateSessionResponse.allOf")
	}
	requiredAny, _ := iso["required"].([]any)
	hasRequired := false
	for _, r := range requiredAny {
		if r == "conversationContinuity" {
			hasRequired = true
		}
	}
	if !hasRequired {
		t.Errorf("conversationContinuity must be required, got %v", requiredAny)
	}
	props, _ := iso["properties"].(map[string]any)
	cont, _ := props["conversationContinuity"].(map[string]any)
	if cont == nil {
		t.Fatal("conversationContinuity property missing")
	}
	if cont["type"] != "string" {
		t.Errorf("conversationContinuity.type: got %v, want string", cont["type"])
	}
	if got := enumValues(t, cont); !equalStringSet(got, map[string]bool{"platform": true, "none": true}) {
		t.Errorf("conversationContinuity enum: got %v, want {platform,none}", got)
	}
}

// TestRuntimeSessionPolicyStructure_spec_5_2 asserts the Runtime schema
// carries the §5.2 sessionPolicy block (replacing the removed taskPolicy)
// with the recycle sub-object, the renamed scrubProfile/onScrubFailure/
// maxSessionRetries knobs, and the relocated concurrency knobs, so the
// external API surface mirrors the §5.2 configuration the CRD admits.
// spec: §5.2 (sessionPolicy block); §15.1.
func TestRuntimeSessionPolicyStructure_spec_5_2(t *testing.T) {
	s := schemas(t)
	runtimeProps, _ := s["Runtime"].(map[string]any)["properties"].(map[string]any)
	if _, ok := runtimeProps["taskPolicy"]; ok {
		t.Error("Runtime.taskPolicy must be removed")
	}
	sp, _ := runtimeProps["sessionPolicy"].(map[string]any)
	if sp == nil {
		t.Fatal("Runtime.sessionPolicy missing")
	}
	spProps, _ := sp["properties"].(map[string]any)
	for _, field := range []string{
		"maxConcurrentSessions", "acknowledgeProcessLevelIsolation",
		"recycle", "cleanupCommands", "cleanupTimeoutSeconds",
		"maxSessionRetries", "maxSessionAgeSeconds", "maxClientIdleSeconds",
		"slotRetries", "onPoolExhausted", "maxQueueWaitSeconds",
	} {
		if _, ok := spProps[field]; !ok {
			t.Errorf("sessionPolicy missing relocated knob %q", field)
		}
	}
	// The removed task-mode knob must not survive at the top level.
	if _, ok := spProps["maxTasksPerPod"]; ok {
		t.Error("sessionPolicy must not carry the removed maxTasksPerPod")
	}
	if _, ok := spProps["maxTaskRetries"]; ok {
		t.Error("sessionPolicy must not carry the removed maxTaskRetries")
	}

	recycle, _ := spProps["recycle"].(map[string]any)
	if recycle == nil {
		t.Fatal("sessionPolicy.recycle missing")
	}
	recProps, _ := recycle["properties"].(map[string]any)
	for _, field := range []string{
		"enabled", "acknowledgeBestEffortScrub", "allowCrossTenantReuse",
		"scrubProfile", "acknowledgeMicrovmResidualState", "onScrubFailure",
		"maxScrubFailures", "maxSessionsPerPod", "maxPodUptimeSeconds",
	} {
		if _, ok := recProps[field]; !ok {
			t.Errorf("sessionPolicy.recycle missing knob %q", field)
		}
	}
	scrub, _ := recProps["scrubProfile"].(map[string]any)
	if got := enumValues(t, scrub); !equalStringSet(got, map[string]bool{"standard": true, "vm-restart": true, "in-place": true}) {
		t.Errorf("scrubProfile enum: got %v, want {standard,vm-restart,in-place}", got)
	}
	onScrub, _ := recProps["onScrubFailure"].(map[string]any)
	if got := enumValues(t, onScrub); !equalStringSet(got, map[string]bool{"warn": true, "fail": true}) {
		t.Errorf("onScrubFailure enum: got %v, want {warn,fail}", got)
	}
}

// isolationLevelProp returns the named property schema from the
// CreateSessionResponse.allOf sessionIsolationLevel block.
func isolationLevelProp(t *testing.T, name string) map[string]any {
	t.Helper()
	s := schemas(t)
	resp, _ := s["CreateSessionResponse"].(map[string]any)
	allOf, _ := resp["allOf"].([]any)
	for _, raw := range allOf {
		block, _ := raw.(map[string]any)
		props, _ := block["properties"].(map[string]any)
		level, ok := props["sessionIsolationLevel"].(map[string]any)
		if !ok {
			continue
		}
		levelProps, _ := level["properties"].(map[string]any)
		if p, ok := levelProps[name].(map[string]any); ok {
			return p
		}
	}
	t.Fatalf("sessionIsolationLevel.%s not found", name)
	return nil
}

// equalStringSet reports whether two string sets are equal.
func equalStringSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// TestRouteScopesResolvesCanonicalScope_spec_15_1_914 asserts the
// route-to-scope lookup the §25.1 scope-enforcement middleware consumes
// resolves the canonical `tools:<domain>:<action>` scope a matched admin
// route requires, including a newly added admin domain (`legal_hold`), so
// the taxonomy expansion (S1/S2) is exercised end to end through the
// document-derived registry. The matcher reuses http.ServeMux pattern
// routing, the same engine the admin router routes on, so a templated route
// (`/v1/admin/connectors/{name}`) resolves through a concrete request path.
//
// spec: §15.1 (scope enforcement before routing, line 914,920),
// §25.1 (middleware checks scopes before routing, line 94).
func TestRouteScopesResolvesCanonicalScope_spec_15_1_914(t *testing.T) {
	rs := openapi.NewRouteScopes()

	cases := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{
			// A newly added admin domain: legal_hold was absent from the
			// scopes.Domains map and the §15.1 taxonomy before this change,
			// so resolving it proves the expansion reached the registry.
			name:   "newly added legal_hold domain",
			method: http.MethodPost,
			path:   "/v1/admin/legal-hold",
			want:   "tools:legal_hold:write",
		},
		{
			// A static route on a pre-existing domain.
			name:   "static circuit_breaker read",
			method: http.MethodGet,
			path:   "/v1/admin/circuit-breakers",
			want:   "tools:circuit_breaker:read",
		},
		{
			// A templated route resolves through a concrete request path,
			// proving the {param} template matches the same way the live
			// admin mux would route the request.
			name:   "templated connector route",
			method: http.MethodGet,
			path:   "/v1/admin/connectors/acme-connector",
			want:   "tools:connector:read",
		},
		{
			// The destructive audit sub-route carries the fine-grained
			// scope the handler enforces, reconciled in S2.
			name:   "fine-grained audit partition drop",
			method: http.MethodPost,
			path:   "/v1/admin/audit-partitions/p-2026-06/drop",
			want:   "tools:audit:partition_drop",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rs.RequiredScope(tc.method, tc.path)
			if !ok {
				t.Fatalf("RequiredScope(%s, %s): no scope resolved, want %q",
					tc.method, tc.path, tc.want)
			}
			if got != tc.want {
				t.Errorf("RequiredScope(%s, %s) = %q, want %q",
					tc.method, tc.path, got, tc.want)
			}
			// Every resolved route-level scope must parse through the
			// canonical matcher; a value the matcher rejects could never
			// be compared against a caller's claim and would silently fail
			// open.
			if _, err := scopes.ParseScope(got); err != nil {
				t.Errorf("resolved scope %q does not parse through scopes.ParseScope: %v", got, err)
			}
		})
	}
}

// TestRouteScopesUnmatchedRouteHasNoScope_spec_15_1_914 asserts the lookup
// reports no required scope for a path the document does not declare and for
// a declared path under a method it does not carry, so the middleware defers
// to the role ceiling rather than matching against an empty scope. This pins
// the §25.1 absent-route-scope behavior the enforcement step relies on.
//
// spec: §15.1 (x-lenny-scope per operation, line 920),
// §25.1 (absent claim defers to the role ceiling, line 90).
func TestRouteScopesUnmatchedRouteHasNoScope_spec_15_1_914(t *testing.T) {
	rs := openapi.NewRouteScopes()

	if scope, ok := rs.RequiredScope(http.MethodGet, "/v1/admin/does-not-exist"); ok {
		t.Errorf("unmatched route resolved scope %q, want none", scope)
	}
	// DELETE is not registered for /v1/admin/legal-hold (only POST), so a
	// method miss on an otherwise-known path resolves no scope.
	if scope, ok := rs.RequiredScope(http.MethodDelete, "/v1/admin/legal-hold"); ok {
		t.Errorf("method miss resolved scope %q, want none", scope)
	}
	// A nil receiver fails closed by reporting no scope rather than
	// panicking, so a caller that never built the matcher cannot match a
	// destructive route against an empty scope.
	var nilRS *openapi.RouteScopes
	if scope, ok := nilRS.RequiredScope(http.MethodPost, "/v1/admin/legal-hold"); ok {
		t.Errorf("nil RouteScopes resolved scope %q, want none", scope)
	}
	// An un-buildable request (a method containing an HTTP-illegal space)
	// makes http.NewRequest fail; the lookup must report no scope rather
	// than panicking or matching, so a malformed method on a destructive
	// route defers to the role ceiling instead of failing open.
	if scope, ok := rs.RequiredScope("BAD METHOD", "/v1/admin/legal-hold"); ok {
		t.Errorf("malformed method resolved scope %q, want none", scope)
	}
}

// spec: §15.5 item 6 — the default tier is `stable` so an unannotated
// operation reads as covered by the §15.5 items 1–5 guarantees.
// F-15.5.10.
func TestStabilityDefaultsToStable_spec_15_5_2447(t *testing.T) {
	doc := openapi.Document()
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	paths, _ := parsed["paths"].(map[string]any)
	sessions, _ := paths["/v1/sessions"].(map[string]any)
	if sessions == nil {
		t.Fatal("/v1/sessions missing")
	}
	post, _ := sessions["post"].(map[string]any)
	if post == nil {
		t.Fatal("/v1/sessions POST missing")
	}
	if got, _ := post["x-lenny-stability"].(string); got != string(openapi.StabilityStable) {
		t.Errorf("/v1/sessions POST stability: got %q, want %q",
			got, openapi.StabilityStable)
	}
}
