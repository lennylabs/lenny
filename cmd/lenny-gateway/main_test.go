// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/tenantkms"
)

// spec: §4.9 — the gateway's LLM reverse-proxy listener wiring.

func TestNewLLMProxyServerDisabledWhenAddrEmpty(t *testing.T) {
	if srv := newLLMProxyServer("", buildLLMTranslatorRegistry(llmTranslatorConfig{anthropicVersion: "2023-06-01"}), credleasestore.New(), credcache.New(), denylist.New(), interceptor.NewChain(), nil, nil, nil, nil, llmFallbackWiring{}); srv != nil {
		t.Errorf("newLLMProxyServer with an empty address returned %v, want nil", srv)
	}
}

func TestNewLLMProxyServerBindsConfiguredAddress(t *testing.T) {
	srv := newLLMProxyServer(":8443", buildLLMTranslatorRegistry(llmTranslatorConfig{anthropicVersion: "2023-06-01"}), credleasestore.New(), credcache.New(), denylist.New(), interceptor.NewChain(), nil, nil, nil, nil, llmFallbackWiring{})
	if srv == nil {
		t.Fatal("newLLMProxyServer returned nil for a configured address")
	}
	if srv.Addr != ":8443" {
		t.Errorf("Addr = %q, want :8443", srv.Addr)
	}
}

func TestNewLLMProxyServerRoutesTheMessagesEndpoint(t *testing.T) {
	srv := newLLMProxyServer(":8443", buildLLMTranslatorRegistry(llmTranslatorConfig{anthropicVersion: "2023-06-01"}), credleasestore.New(), credcache.New(), denylist.New(), interceptor.NewChain(), nil, nil, nil, nil, llmFallbackWiring{})
	if srv == nil {
		t.Fatal("newLLMProxyServer returned nil")
	}

	// A POST with no lease token reaches the proxy handler, which
	// rejects it — proof the route is wired to the §4.9 handler.
	req := httptest.NewRequest(http.MethodPost, "/llm-proxy/v1/messages",
		strings.NewReader(`{"model":"claude-3-5-sonnet","messages":[]}`))
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("POST /llm-proxy/v1/messages = %d, want 401 (no lease token)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "LEASE_TOKEN_MISSING") {
		t.Errorf("body = %q, want a LEASE_TOKEN_MISSING rejection", rr.Body.String())
	}
}

func TestNewLLMProxyServerRejectsUnknownPath(t *testing.T) {
	srv := newLLMProxyServer(":8443", buildLLMTranslatorRegistry(llmTranslatorConfig{anthropicVersion: "2023-06-01"}), credleasestore.New(), credcache.New(), denylist.New(), interceptor.NewChain(), nil, nil, nil, nil, llmFallbackWiring{})
	req := httptest.NewRequest(http.MethodPost, "/llm-proxy/v1/no-such-endpoint", nil)
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("POST to an unknown proxy path = %d, want 404", rr.Code)
	}
}

func TestNewLLMProxyServerRejectsNonPost(t *testing.T) {
	srv := newLLMProxyServer(":8443", buildLLMTranslatorRegistry(llmTranslatorConfig{anthropicVersion: "2023-06-01"}), credleasestore.New(), credcache.New(), denylist.New(), interceptor.NewChain(), nil, nil, nil, nil, llmFallbackWiring{})
	req := httptest.NewRequest(http.MethodGet, "/llm-proxy/v1/messages", nil)
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /llm-proxy/v1/messages = %d, want 405", rr.Code)
	}
}

// spec: §16.6 — a §10.7 fail-closed isolation rejection is an
// operational event.

func TestExperimentRejectionReporterEmitsOperationalEvent(t *testing.T) {
	emitter := eventbuffer.NewEmitter(eventbuffer.NewEventBuffer(0), "replica-test")
	reporter := experimentRejectionReporter{emitter: emitter}

	reporter.ReportExperimentIsolationRejection(context.Background(), sessionserver.ExperimentIsolationRejection{
		TenantID:             "acme",
		UserID:               "alice",
		ExperimentID:         "exp_1",
		VariantID:            "treatment",
		SessionMinIsolation:  "microvm",
		VariantPoolIsolation: "sandboxed",
	})

	page := emitter.Buffer().Query(0, events.EventFilter{
		EventType: string(events.EventExperimentIsolationMismatch),
	}, 0)
	if len(page.Events) != 1 {
		t.Fatalf("buffer holds %d isolation_mismatch events, want 1", len(page.Events))
	}
	ev := page.Events[0].Event
	if ev.Type != "dev.lenny.experiment.isolation_mismatch" {
		t.Errorf("event type = %q", ev.Type)
	}
	if ev.Severity != "warning" {
		t.Errorf("severity = %q, want warning", ev.Severity)
	}
	var data struct {
		TenantID     string `json:"tenant_id"`
		ExperimentID string `json:"experiment_id"`
		SessionMin   string `json:"sessionMinIsolation"`
		VariantPool  string `json:"variantPoolIsolation"`
	}
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatalf("event data: %v", err)
	}
	if data.TenantID != "acme" || data.ExperimentID != "exp_1" {
		t.Errorf("tenant/experiment = %q/%q, want acme/exp_1", data.TenantID, data.ExperimentID)
	}
	if data.SessionMin != "microvm" || data.VariantPool != "sandboxed" {
		t.Errorf("isolation fields = %q/%q, want microvm/sandboxed", data.SessionMin, data.VariantPool)
	}
}

func TestExperimentRejectionReporterNilDependenciesAreSafe(t *testing.T) {
	// A reporter with no audit, metrics, or emitter wired must not
	// panic — every sink is best-effort.
	reporter := experimentRejectionReporter{}
	reporter.ReportExperimentIsolationRejection(context.Background(), sessionserver.ExperimentIsolationRejection{
		TenantID: "acme", ExperimentID: "exp_1",
	})
}

// spec: §4.1 / §16.1 — exportHPAGauges populates the
// lenny_gateway_active_sessions, _active_streams, and
// _request_queue_depth gauges from the live in-process state.
func TestExportHPAGaugesUpdatesAllGauges(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Seed two active sessions and one terminal session under the
	// "default" tenant; the exporter must count two.
	for _, s := range []sessionstore.Session{
		{ID: "s1", TenantID: "default", State: session.StateRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "s2", TenantID: "default", State: session.StateCreated, CreatedAt: now, UpdatedAt: now},
		{ID: "s3", TenantID: "default", State: session.StateCompleted, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.Create(context.Background(), s); err != nil {
			t.Fatalf("seed %s: %v", s.ID, err)
		}
	}

	bus := sessionevents.NewBus(0)
	sub := bus.Subscribe("s1", 0, 4)
	defer sub.Close()

	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}

	// Drive a request through the metrics middleware so InflightRequests
	// reports a non-zero value across the exporter call.
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	h := m.Middleware(inner, func(*http.Request) string { return "/test" })
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}()
	<-started

	// Wrap the sessionstore in a minimal tenantsLister that only
	// returns the seeded tenant; the production lister adds "default"
	// and that's all this fixture has.
	lister := staticTenantLister{[]string{"default"}}
	exportHPAGauges(context.Background(), store, lister, bus, m)

	// Now drive a second poll after the in-flight request finishes
	// so we can verify the queue-depth gauge tracks both directions.
	close(release)
	<-done

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()
	for _, want := range []string{
		"lenny_gateway_active_sessions 2",
		"lenny_gateway_active_streams 1",
		"lenny_gateway_request_queue_depth 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics output missing %q\n---\n%s", want, body)
		}
	}

	// Second poll: the in-flight request has exited, so queue depth
	// drops to 0; active sessions stay at 2 and active streams remain
	// at 1 (the subscriber is still open).
	exportHPAGauges(context.Background(), store, lister, bus, m)
	rr = httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body = rr.Body.String()
	if !strings.Contains(body, "lenny_gateway_request_queue_depth 0") {
		t.Errorf("after handler exit, queue_depth gauge did not drop to 0\n%s", body)
	}
}

// spec: §4.1 — a cancelled context returns from exportHPAGauges
// promptly without spuriously updating the gauges.
func TestExportHPAGaugesContextCancellation(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lister := staticTenantLister{[]string{"default"}}
	// Cancelled context: the lister call returns an empty/nil result
	// without panic.
	exportHPAGauges(ctx, store, lister, bus, m)
}

// spec: §16.5 line 460 — exportElicitationIntegrityWeakened counts the
// active tenants whose §9.2 effective elicitation content-integrity
// mode is weaker than enforce. With no platform floor, a stored
// detect-only or off is weakened; an unset value resolves to the
// enforce default and is not. A soft-deleted tenant is excluded.
// Raising the floor to enforce clamps every tenant up, resolving the
// gauge to zero. F-9.2.5.
func TestExportElicitationIntegrityWeakenedCountsActiveTenants(t *testing.T) {
	tenants := tenantstore.NewMemory()
	ctx := context.Background()
	seed := []tenantstore.Tenant{
		{ID: "acme", ElicitationContentIntegrity: "enforce"},       // not weakened
		{ID: "globex", ElicitationContentIntegrity: "detect-only"}, // weakened
		{ID: "initech", ElicitationContentIntegrity: "off"},        // weakened
		{ID: "umbrella", ElicitationContentIntegrity: ""},          // unset → enforce default, not weakened
		{ID: "deleted-co", ElicitationContentIntegrity: "off"},     // soft-deleted → excluded
	}
	for _, tn := range seed {
		if err := tenants.Create(ctx, tn); err != nil {
			t.Fatalf("seed tenant %s: %v", tn.ID, err)
		}
	}
	if err := tenants.SoftDelete(ctx, "deleted-co", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}

	// No platform floor: globex (detect-only) + initech (off) are
	// weakened; the soft-deleted off tenant is not counted.
	exportElicitationIntegrityWeakened(ctx, tenants, "", m)
	body := scrapeGatewayMetrics(t, m)
	if !strings.Contains(body, "lenny_elicitation_content_integrity_effective_mode_weaker_than_enforce 2") {
		t.Errorf("with no floor, want weakened gauge = 2, got:\n%s", body)
	}

	// Raise the platform floor to enforce: every active tenant clamps up
	// to enforce, so the standing alert resolves to zero.
	exportElicitationIntegrityWeakened(ctx, tenants, "enforce", m)
	body = scrapeGatewayMetrics(t, m)
	if !strings.Contains(body, "lenny_elicitation_content_integrity_effective_mode_weaker_than_enforce 0") {
		t.Errorf("with enforce floor, want weakened gauge = 0, got:\n%s", body)
	}
}

// scrapeGatewayMetrics renders the /metrics body for assertions.
func scrapeGatewayMetrics(t *testing.T, m *gatewaymetrics.Metrics) string {
	t.Helper()
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d", rr.Code)
	}
	return rr.Body.String()
}

// staticTenantLister is a test-only TenantLister that returns a
// fixed slice. It is wired through tenantsLister's interface so the
// exportHPAGauges signature matches the production code path.
type staticTenantLister struct {
	tenants []string
}

func (s staticTenantLister) ListTenants(_ context.Context) ([]string, error) {
	return s.tenants, nil
}

// spec: §12.5 ll. 297-303 — the SSEKeyResolver returns
// (alias, requireKey=true, nil) for a T4 tenant, ("", false, nil) for
// any other tier, and ("", true, err) for a tenant lookup failure
// (fail-closed posture). The blobstore consumes the three branches
// and the gateway's MinIO Put dispatch wraps a T4 object under the
// per-tenant alias.
func TestNewSSEKeyResolverPicksT4AliasOrFallsBack(t *testing.T) {
	store := tenantstore.NewMemory()
	ctx := context.Background()
	if err := store.Create(ctx, tenantstore.Tenant{ID: "acme-t4", WorkspaceTier: "T4"}); err != nil {
		t.Fatalf("Create T4 tenant: %v", err)
	}
	if err := store.Create(ctx, tenantstore.Tenant{ID: "globex-t3", WorkspaceTier: "T3"}); err != nil {
		t.Fatalf("Create T3 tenant: %v", err)
	}
	if err := store.Create(ctx, tenantstore.Tenant{ID: "initech-untiered"}); err != nil {
		t.Fatalf("Create untiered tenant: %v", err)
	}

	resolver := newSSEKeyResolver(store)

	cases := []struct {
		name       string
		tenantID   string
		wantAlias  string
		wantNeed   bool
		wantErrMsg string // substring; empty means no error
	}{
		{
			name:      "T4 tenant returns per-tenant alias and requireKey=true",
			tenantID:  "acme-t4",
			wantAlias: tenantkms.AliasFor("acme-t4"),
			wantNeed:  true,
		},
		{
			name:      "T3 tenant returns empty alias and requireKey=false",
			tenantID:  "globex-t3",
			wantAlias: "",
			wantNeed:  false,
		},
		{
			name:      "untiered tenant returns empty alias and requireKey=false",
			tenantID:  "initech-untiered",
			wantAlias: "",
			wantNeed:  false,
		},
		{
			name:       "missing tenant returns error and requireKey=true (fail-closed)",
			tenantID:   "ghost",
			wantAlias:  "",
			wantNeed:   true,
			wantErrMsg: "tenant not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			alias, need, err := resolver(tc.tenantID)
			if alias != tc.wantAlias {
				t.Errorf("alias = %q, want %q", alias, tc.wantAlias)
			}
			if need != tc.wantNeed {
				t.Errorf("requireKey = %v, want %v", need, tc.wantNeed)
			}
			if tc.wantErrMsg == "" {
				if err != nil {
					t.Errorf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrMsg) {
				t.Errorf("err = %v, want containing %q", err, tc.wantErrMsg)
			}
			// And the error wraps tenantstore.ErrNotFound so callers
			// can match on errors.Is.
			if err != nil && !errors.Is(err, tenantstore.ErrNotFound) {
				t.Errorf("err = %v, want errors.Is(tenantstore.ErrNotFound)", err)
			}
		})
	}
}

// TestGatewayConfigValidation_spec_11_1 asserts the §10.3 configuration
// validation table contract for the noEnvironmentPolicy startup key:
// outside dev mode an unset value is a fatal startup error carrying the
// `LENNY_CONFIG_MISSING config_key=noEnvironmentPolicy scope=platform`
// log marker §10.3 mandates; under dev mode an unset value derives
// allow-all (the §17.4 dev-mode escape hatch); explicit deny-all and
// allow-all both pass through; an unrecognised value is rejected.
// spec: §11.1 line 13; §10.3 configuration validation table; §10.6
// line 646; §17.4 dev mode.
func TestGatewayConfigValidation_spec_11_1(t *testing.T) {
	cases := []struct {
		name       string
		value      string
		devMode    bool
		want       string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:       "missing outside dev mode emits LENNY_CONFIG_MISSING",
			value:      "",
			devMode:    false,
			wantErr:    true,
			wantErrMsg: "LENNY_CONFIG_MISSING config_key=noEnvironmentPolicy scope=platform",
		},
		{
			name:    "missing under dev mode derives allow-all",
			value:   "",
			devMode: true,
			want:    tenantstore.NoEnvPolicyAllowAll,
		},
		{
			name:  "explicit deny-all passes through",
			value: tenantstore.NoEnvPolicyDenyAll,
			want:  tenantstore.NoEnvPolicyDenyAll,
		},
		{
			name:  "explicit allow-all passes through",
			value: tenantstore.NoEnvPolicyAllowAll,
			want:  tenantstore.NoEnvPolicyAllowAll,
		},
		{
			name:       "dev mode does not override an explicit unknown value",
			value:      "permit-everything",
			devMode:    true,
			wantErr:    true,
			wantErrMsg: "must be deny-all or allow-all",
		},
		{
			name:       "unrecognised value rejected outside dev mode",
			value:      "audit-only",
			wantErr:    true,
			wantErrMsg: "must be deny-all or allow-all",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveNoEnvironmentPolicy(tc.value, tc.devMode)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("err = nil, want one containing %q", tc.wantErrMsg)
				}
				if !strings.Contains(err.Error(), tc.wantErrMsg) {
					t.Errorf("err = %q, want containing %q", err.Error(), tc.wantErrMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Errorf("resolved = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGatewayConfigValidationRequiredKeys_spec_10_3 asserts the §10.3
// line 361 required-key table contract for the keys gated by
// validatePlatformConfig: outside dev mode an empty or non-URL
// auth.oidc.issuerUrl, an empty auth.oidc.clientId, and a non-positive
// defaultMaxSessionDuration each produce a LENNY_CONFIG_MISSING
// violation carrying the correct config_key; the OIDC keys are exempt
// when dev mode is on (the §10.3 line 373 / §17.4 dev-mode symmetry);
// the session-duration key is gated even in dev mode (no dev exemption
// in the table). spec: §10.3 lines 361-373; §17.4.
func TestGatewayConfigValidationRequiredKeys_spec_10_3(t *testing.T) {
	const validIssuer = "https://idp.acme.example/realms/lenny"
	const validClient = "lenny-gateway"
	const validMaxSession = 7200

	keysOf := func(ms []platformConfigMissing) []string {
		out := make([]string, len(ms))
		for i, m := range ms {
			out[i] = m.configKey
			if m.scope != "platform" {
				t.Errorf("scope = %q, want platform", m.scope)
			}
			if m.remediation == "" {
				t.Errorf("config_key %q has an empty remediation", m.configKey)
			}
		}
		return out
	}
	contains := func(keys []string, want string) bool {
		for _, k := range keys {
			if k == want {
				return true
			}
		}
		return false
	}

	cases := []struct {
		name      string
		devMode   bool
		issuer    string
		clientID  string
		maxSecs   int
		wantKeys  []string
		wantEmpty bool
	}{
		{
			name:      "all present passes outside dev mode",
			issuer:    validIssuer,
			clientID:  validClient,
			maxSecs:   validMaxSession,
			wantEmpty: true,
		},
		{
			name:     "empty issuer outside dev mode flags auth.oidc.issuerUrl",
			issuer:   "",
			clientID: validClient,
			maxSecs:  validMaxSession,
			wantKeys: []string{"auth.oidc.issuerUrl"},
		},
		{
			name:     "non-URL issuer outside dev mode flags auth.oidc.issuerUrl",
			issuer:   "not-a-url",
			clientID: validClient,
			maxSecs:  validMaxSession,
			wantKeys: []string{"auth.oidc.issuerUrl"},
		},
		{
			name:     "empty clientId outside dev mode flags auth.oidc.clientId",
			issuer:   validIssuer,
			clientID: "",
			maxSecs:  validMaxSession,
			wantKeys: []string{"auth.oidc.clientId"},
		},
		{
			name:     "non-positive session duration flags defaultMaxSessionDuration",
			issuer:   validIssuer,
			clientID: validClient,
			maxSecs:  0,
			wantKeys: []string{"defaultMaxSessionDuration"},
		},
		{
			name:     "all three keys missing reports all three",
			issuer:   "",
			clientID: "",
			maxSecs:  -1,
			wantKeys: []string{"auth.oidc.issuerUrl", "auth.oidc.clientId", "defaultMaxSessionDuration"},
		},
		{
			name:      "dev mode exempts the OIDC keys",
			devMode:   true,
			issuer:    "",
			clientID:  "",
			maxSecs:   validMaxSession,
			wantEmpty: true,
		},
		{
			name:     "dev mode does not exempt the session-duration key",
			devMode:  true,
			issuer:   "",
			clientID: "",
			maxSecs:  0,
			wantKeys: []string{"defaultMaxSessionDuration"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := keysOf(validatePlatformConfig(tc.devMode, tc.issuer, tc.clientID, tc.maxSecs))
			if tc.wantEmpty {
				if len(got) != 0 {
					t.Fatalf("want no violations, got %v", got)
				}
				return
			}
			if len(got) != len(tc.wantKeys) {
				t.Fatalf("violations = %v, want %v", got, tc.wantKeys)
			}
			for _, k := range tc.wantKeys {
				if !contains(got, k) {
					t.Errorf("missing expected config_key %q in %v", k, got)
				}
			}
		})
	}
}

// TestBuildStartupProbeTLSConfig_spec_10_3 asserts the §10.3 line 359
// probe TLS config builder: no material yields system roots with no
// client cert, a missing CA file is a hard error, and a malformed CA
// bundle is rejected. spec: §10.3 line 359.
func TestBuildStartupProbeTLSConfig_spec_10_3(t *testing.T) {
	cfg, err := buildStartupProbeTLSConfig("", "", "")
	if err != nil {
		t.Fatalf("empty material should succeed, got %v", err)
	}
	if cfg.RootCAs != nil {
		t.Error("no CA file should leave RootCAs nil (system trust store)")
	}
	if len(cfg.Certificates) != 0 {
		t.Error("no cert/key should present no client certificate")
	}

	if _, err := buildStartupProbeTLSConfig("/nonexistent/ca.pem", "", ""); err == nil {
		t.Error("a missing CA file must be a hard error")
	}

	bad := t.TempDir() + "/bad-ca.pem"
	if werr := os.WriteFile(bad, []byte("not a pem"), 0o600); werr != nil {
		t.Fatalf("write fixture: %v", werr)
	}
	if _, err := buildStartupProbeTLSConfig(bad, "", ""); err == nil {
		t.Error("a CA bundle with no certificates must be rejected")
	}
}

// TestParseWindowOverrides_spec_25_3_596 covers the §25.3 recommendations
// window-override flag parser: well-formed category=duration pairs map
// through, malformed pairs and unparseable or non-positive durations are
// skipped, and an all-empty input yields a nil map. F-25.3.12.
func TestParseWindowOverrides_spec_25_3_596(t *testing.T) {
	got := parseWindowOverrides("warm_pool_sizing=12h, credential_pool_sizing=72h ,bogus,gateway_scaling=0s,resource_limits=notaduration")
	if len(got) != 2 {
		t.Fatalf("parsed %d overrides, want 2: %v", len(got), got)
	}
	if got["warm_pool_sizing"] != 12*time.Hour {
		t.Errorf("warm_pool_sizing = %v, want 12h", got["warm_pool_sizing"])
	}
	if got["credential_pool_sizing"] != 72*time.Hour {
		t.Errorf("credential_pool_sizing = %v, want 72h", got["credential_pool_sizing"])
	}
	if _, ok := got["gateway_scaling"]; ok {
		t.Error("a non-positive (0s) duration must be skipped")
	}
	if _, ok := got["resource_limits"]; ok {
		t.Error("an unparseable duration must be skipped")
	}
	if parseWindowOverrides("") != nil {
		t.Error("empty input must yield a nil map")
	}
}

// spec: §12.5 ll. 316, 341 — the tombstone hard-prune pass physically
// removes partial-manifest rows whose soft-delete tombstone predates the
// retention cutoff and leaves active and not-yet-expired rows intact.
func TestHardPrunePartialManifests(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	store := partialmanifeststore.NewMemoryStore(func() time.Time { return base })
	ctx := context.Background()

	put := func(session string, gen int64) {
		if err := store.Put(ctx, partialmanifeststore.Record{
			TenantID:               "acme",
			SessionID:              session,
			Generation:             gen,
			PartialObjectKeyPrefix: "/acme/checkpoints/" + session + "/partial/cp/",
			ChunkEncoding:          partialmanifeststore.ChunkEncodingTar,
		}); err != nil {
			t.Fatalf("Put %s/%d: %v", session, gen, err)
		}
	}
	put("sess-a", 1)
	put("sess-b", 1)
	put("sess-c", 1) // stays active

	// Soft-delete two rows at base; the third remains active.
	if err := store.SoftDelete(ctx, "acme", "sess-a", 1); err != nil {
		t.Fatalf("SoftDelete a: %v", err)
	}
	if err := store.SoftDelete(ctx, "acme", "sess-b", 1); err != nil {
		t.Fatalf("SoftDelete b: %v", err)
	}

	// Cutoff at the tombstone instant: nothing is past retention yet
	// (DeletedAt.Before(cutoff) is false), so no row is pruned.
	if n, err := hardPrunePartialManifests(ctx, store, base); err != nil || n != 0 {
		t.Fatalf("hardPrunePartialManifests(cutoff=base) = (%d, %v); want (0, nil)", n, err)
	}

	// Cutoff after the tombstone instant: both soft-deleted rows are past
	// retention and physically removed; the active row is untouched.
	n, err := hardPrunePartialManifests(ctx, store, base.Add(time.Minute))
	if err != nil {
		t.Fatalf("hardPrunePartialManifests: %v", err)
	}
	if n != 2 {
		t.Fatalf("pruned = %d; want 2", n)
	}
	if _, err := store.Get(ctx, "acme", "sess-a", 1); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Errorf("sess-a still present after hard-prune: %v", err)
	}
	if _, err := store.Get(ctx, "acme", "sess-b", 1); !errors.Is(err, partialmanifeststore.ErrNotFound) {
		t.Errorf("sess-b still present after hard-prune: %v", err)
	}
	if _, err := store.Get(ctx, "acme", "sess-c", 1); err != nil {
		t.Errorf("active sess-c was pruned: %v", err)
	}
}
