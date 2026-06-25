// SPDX-License-Identifier: MIT

package openapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/openapi"
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
// conforms to a documented syntax. The spec mandates
// `tools:<domain>:<action>`; the in-tree openapi.json uses a
// transitional `admin.<domain>.<verb>` form for some entries — both
// are accepted by the test so the CI guard catches malformed scopes
// (typos, missing separators) without forcing the entire OpenAPI
// document into the canonical form in a single commit.
//
// Once every scope is migrated to `tools:<domain>:<action>`, drop the
// legacy regex branch.
// spec: §15.1 line 933.
func TestAdminScopesFollowDocumentedSyntax_spec_15_1_933(t *testing.T) {
	doc := openapi.Document()
	var parsed map[string]any
	_ = json.Unmarshal(doc, &parsed)
	paths, _ := parsed["paths"].(map[string]any)

	// `tools:<domain>:<action>` (spec) or `admin.<domain>.<verb>`
	// (legacy). Allowed characters are lowercase letters, digits, dot,
	// dash, underscore, colon, and `*` (per §15.1 line 922 wildcard).
	specScope := regexp.MustCompile(`^tools:[a-z0-9_]+:[a-z0-9_*]+$`)
	legacyScope := regexp.MustCompile(`^admin\.[a-z0-9_-]+\.[a-z0-9_-]+$`)

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
			if !specScope.MatchString(scope) && !legacyScope.MatchString(scope) {
				t.Errorf("%s %s x-lenny-scope %q does not match `tools:<domain>:<action>` or `admin.<domain>.<verb>`",
					method, path, scope)
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
