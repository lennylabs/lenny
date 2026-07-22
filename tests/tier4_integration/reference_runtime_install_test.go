// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §26.1 reference-runtime catalog install and
// default-tenant auto-grant, driven through the real embedded-stack install
// path (pkg/embedded/stack.InstallReferenceRuntimes, the function the local
// profile `lenny up` bring-up calls via installRuntimesFn) against a real
// cmd/lenny-gateway binary and a live Postgres container. The only existing
// coverage of this install path (pkg/embedded/stack/runtimes_test.go) drives
// it against an in-process httptest mock; nothing confirms the tenant-access
// grants the install issues actually persist and take effect against a real
// gateway backed by Postgres.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/embedded/stack"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §26.1 — "Reference runtimes are registered by `lenny-ctl install`
// ... as platform-global records with no default tenant access grants.
// Operators grant access per tenant via `POST
// /v1/admin/runtimes/{name}/tenant-access` ... after install. For `local`
// profile installations, `lenny up` auto-grants access to the `default`
// tenant for every reference runtime it installs and the credential-free
// `echo` runtime ... runs out of the box." §4 (04_system-components.md) —
// "A `tenant-admin`'s calls to `/v1/admin/runtimes` and `/v1/admin/pools`
// are filtered to the rows in these access tables for their tenant;
// `platform-admin` calls are unfiltered." Together these two sections say
// which endpoint the tenant-access grant governs (the tenant-admin-scoped
// `GET /v1/admin/runtimes` view) and what `lenny up` does to it (grants the
// default tenant, and only the default tenant, access to the catalog).
//
// diagnosis: a failure means the local-profile `lenny up` install path
// (pkg/embedded/stack.InstallReferenceRuntimes) does not durably grant the
// default tenant access to the §26 reference-runtime catalog against a real
// gateway and Postgres, or a tenant that received no grant can still see
// catalog runtimes through the tenant-scoped admin listing — either defect
// would let an operator believe access is properly scoped when it is not,
// or leave the documented day-one `lenny up` experience broken against real
// infrastructure even though the in-process mock test in
// pkg/embedded/stack/runtimes_test.go stays green.
func TestReferenceRuntimeInstallGrantsOnlyDefaultTenant_spec_26_1(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	ctx := context.Background()

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	// The CREATE-privileged Postgres container DSN doubles as the
	// billing/audit DDL DSN so tenant creation can provision the per-tenant
	// audit sequence, matching the other tier-4 tests that bootstrap a
	// tenant against a live Postgres container.
	gw := gateway.StartWith(t, "--dev-mode",
		"--postgres-dsn="+pg.DSN,
		"--postgres-billing-audit-ddl-dsn="+pg.DSN)
	do := refRuntimeAdminReq(t, gw.BaseURL())

	// Run the exact install-and-auto-grant sequence `lenny up` runs for a
	// local-profile bring-up: register the §26 catalog plus the §15.4.4 echo
	// exemplar as platform-global records and grant the `default` tenant
	// access to each.
	if err := stack.InstallReferenceRuntimes(ctx, gw.BaseURL(), io.Discard); err != nil {
		t.Fatalf("InstallReferenceRuntimes: %v", err)
	}

	// A second tenant the install never touched. Nothing grants it access
	// to any catalog runtime.
	code, _ := do(http.MethodPost, "/v1/admin/bootstrap", "default", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "globex", "displayName": "Globex Corp"}},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap second tenant: status %d", code)
	}

	catalogNames := make([]string, 0, len(stack.ReferenceRuntimes()))
	for _, rt := range stack.ReferenceRuntimes() {
		catalogNames = append(catalogNames, rt.Name)
	}

	// The default tenant, which the install auto-granted, sees every §26
	// catalog runtime through the tenant-scoped admin listing.
	defaultNames := refRuntimeAdminNames(t, do, "default")
	for _, name := range catalogNames {
		if !refRuntimeNameIn(defaultNames, name) {
			t.Errorf("default tenant does not see reference runtime %q after install; tenant-admin view = %v", name, defaultNames)
		}
	}

	// The freshly created, ungranted globex tenant sees none of the §26
	// catalog runtimes — the install grants only the default tenant.
	globexNames := refRuntimeAdminNames(t, do, "globex")
	for _, name := range catalogNames {
		if refRuntimeNameIn(globexNames, name) {
			t.Errorf("ungranted globex tenant sees reference runtime %q; tenant-admin view = %v", name, globexNames)
		}
	}

	// Granting globex access to one catalog runtime makes exactly that
	// runtime visible to it, confirming the emptiness above reflects the
	// absence of a grant rather than the admin listing being broken for a
	// tenant-admin caller in general.
	grantTarget := catalogNames[0]
	code, _ = do(http.MethodPost, "/v1/admin/runtimes/"+grantTarget+"/tenant-access", "default", "platform-admin",
		map[string]any{"tenantId": "globex"})
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("grant globex access to %q: status %d", grantTarget, code)
	}
	globexAfterGrant := refRuntimeAdminNames(t, do, "globex")
	if !refRuntimeNameIn(globexAfterGrant, grantTarget) {
		t.Fatalf("globex tenant does not see %q after an explicit grant; tenant-admin view = %v", grantTarget, globexAfterGrant)
	}
	for _, name := range catalogNames {
		if name == grantTarget {
			continue
		}
		if refRuntimeNameIn(globexAfterGrant, name) {
			t.Errorf("globex tenant sees ungranted reference runtime %q after a grant for a different runtime; tenant-admin view = %v", name, globexAfterGrant)
		}
	}
}

// spec: §26.2 — "The four coding-agent runtimes (`claude-code`,
// `gemini-cli`, `codex`, `cursor-cli`) share a common workspace shape. ...
// This section defines the shared pattern so individual entries stay
// focused on the differences." The section then declares the shared
// `limits`, `setupCommandPolicy`, `capabilities`, and `egressProfile`
// blocks (spec/26_reference-runtime-catalog.md:38-92); §26.1 line 22 /
// §26.7 declare `chat`'s smaller Full-level posture (single resource
// class, immediate-only injection). pkg/embedded/stack/runtimes_test.go
// and pkg/embedded/stack/bootstrap_seed_admin_test.go already pin these
// fields at the in-memory-store level; this test is the tier-4 owner that
// confirms the same fields survive a real Postgres round trip, since
// runtimestore.Runtime's §26.2 blocks are stored as JSON columns
// (pkg/gateway/runtime/runtimestore/pgstore) and a column-mapping or
// (de)serialization regression there would not surface in an in-memory
// store.
//
// diagnosis: a failure means the §26.2 shared coding-agent fields (or the
// §26.1/§26.7 chat posture) registered by the local-profile `lenny up`
// install path do not survive a real Postgres-backed store, so an operator
// reading a runtime's fields back from GET /v1/admin/runtimes/{name} after
// a real bring-up would see a value that diverges from what the catalog
// declared, even though the in-memory-store unit tests stay green.
func TestReferenceRuntimeInstallPersistsSharedCodingAgentFieldsThroughPostgres_spec_26_2(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	ctx := context.Background()

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	gw := gateway.StartWith(t, "--dev-mode",
		"--postgres-dsn="+pg.DSN,
		"--postgres-billing-audit-ddl-dsn="+pg.DSN)
	do := refRuntimeAdminReq(t, gw.BaseURL())

	if err := stack.InstallReferenceRuntimes(ctx, gw.BaseURL(), io.Discard); err != nil {
		t.Fatalf("InstallReferenceRuntimes: %v", err)
	}

	// claude-code carries the §26.2 shared coding-agent blocks. The
	// default tenant was auto-granted access by the install, so the
	// tenant-scoped GET reaches the record.
	cc := refRuntimeAdminGet(t, do, "default", "claude-code")

	limits, _ := cc["limits"].(map[string]any)
	if maxAge, _ := limits["maxSessionAgeSeconds"].(float64); maxAge != 14400 {
		t.Errorf("claude-code limits.maxSessionAgeSeconds via Postgres = %v, want 14400: %+v", limits["maxSessionAgeSeconds"], cc)
	}

	setupPolicy, _ := cc["setupCommandPolicy"].(map[string]any)
	if mode, _ := setupPolicy["mode"].(string); mode != "allowlist" {
		t.Errorf("claude-code setupCommandPolicy.mode via Postgres = %q, want allowlist: %+v", mode, cc)
	}

	pool, _ := cc["defaultPoolConfig"].(map[string]any)
	if profile, _ := pool["egressProfile"].(string); profile != "restricted" {
		t.Errorf("claude-code defaultPoolConfig.egressProfile via Postgres = %q, want restricted: %+v", profile, cc)
	}

	credCaps, _ := cc["credentialCapabilities"].(map[string]any)
	dialect, _ := credCaps["proxyDialect"].([]any)
	if len(dialect) != 1 || dialect[0] != "anthropic" {
		t.Errorf("claude-code credentialCapabilities.proxyDialect via Postgres = %v, want [anthropic]: %+v", dialect, cc)
	}

	caps, _ := cc["capabilities"].(map[string]any)
	if interaction, _ := caps["interaction"].(string); interaction != "multi_turn" {
		t.Errorf("claude-code capabilities.interaction via Postgres = %q, want multi_turn: %+v", interaction, cc)
	}

	resourceClasses, _ := cc["allowedResourceClasses"].([]any)
	if len(resourceClasses) != 3 {
		t.Errorf("claude-code allowedResourceClasses via Postgres = %v, want 3 entries: %+v", resourceClasses, cc)
	}

	// chat is Full (hotRotation: true requires the Full-only lifecycle
	// channel) and carries the small resource class only with
	// immediate-only injection.
	chat := refRuntimeAdminGet(t, do, "default", "chat")
	if level, _ := chat["integrationLevel"].(string); level != "full" {
		t.Errorf("chat integrationLevel via Postgres = %q, want full: %+v", level, chat)
	}
	chatClasses, _ := chat["allowedResourceClasses"].([]any)
	if len(chatClasses) != 1 || chatClasses[0] != "small" {
		t.Errorf("chat allowedResourceClasses via Postgres = %v, want [small]: %+v", chatClasses, chat)
	}
	chatCaps, _ := chat["capabilities"].(map[string]any)
	chatInjection, _ := chatCaps["injection"].(map[string]any)
	chatModes, _ := chatInjection["modes"].([]any)
	if supported, _ := chatInjection["supported"].(bool); !supported || len(chatModes) != 1 {
		t.Errorf("chat capabilities.injection via Postgres not stored as expected (supported+1 mode): %+v", chatCaps)
	}
}

// refRuntimeAdminGet issues GET /v1/admin/runtimes/{name} as a
// tenant-admin for tenantID and returns the decoded RuntimePayload body.
func refRuntimeAdminGet(t *testing.T, do func(method, path, tenant, roles string, body any) (int, map[string]any), tenantID, name string) map[string]any {
	t.Helper()
	code, body := do(http.MethodGet, "/v1/admin/runtimes/"+name, tenantID, "tenant-admin", nil)
	if code != http.StatusOK {
		t.Fatalf("get runtime %q for tenant %q: status %d (%v)", name, tenantID, code, body)
	}
	return body
}

// refRuntimeAdminReq issues an HTTP request against a running gateway with
// a dev-header identity carrying the caller-supplied roles under the given
// tenant.
func refRuntimeAdminReq(t *testing.T, base string) func(method, path, tenant, roles string, body any) (int, map[string]any) {
	t.Helper()
	client := http.DefaultClient
	return func(method, path, tenant, roles string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", tenant)
		req.Header.Set("X-Lenny-User-ID", "ops@"+tenant)
		if roles != "" {
			req.Header.Set("X-Lenny-Roles", roles)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatalf("%s %s: decode response %q: %v", method, path, raw, err)
			}
		}
		return resp.StatusCode, out
	}
}

// refRuntimeAdminNames issues GET /v1/admin/runtimes as a tenant-admin for
// tenantID and returns the runtime names the tenant-scoped view returns.
func refRuntimeAdminNames(t *testing.T, do func(method, path, tenant, roles string, body any) (int, map[string]any), tenantID string) []string {
	t.Helper()
	code, body := do(http.MethodGet, "/v1/admin/runtimes", tenantID, "tenant-admin", nil)
	if code != http.StatusOK {
		t.Fatalf("list runtimes for tenant %q: status %d (%v)", tenantID, code, body)
	}
	items, _ := body["items"].([]any)
	names := make([]string, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			if n, _ := m["name"].(string); n != "" {
				names = append(names, n)
			}
		}
	}
	return names
}

func refRuntimeNameIn(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
