// SPDX-License-Identifier: MIT

// White-box tier-1 tests for the harness's per-run tenant provisioning.
// They run in-process against an httptest gateway stub, so they need
// neither the Kind cluster nor a warm pool. The behavior they pin is what
// keeps the live-session suites usable against a long-lived, reused
// cluster: a run must never inherit a previous run's tenant record, and
// the tenant it provisions must accept a session that names no
// environment.

package sessiondriver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// tenantIDFormat is the §10.2 tenant-id format a provisioned id must
// satisfy.
var tenantIDFormat = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// spec: 12.8 (tenant deletion lifecycle; the tenant-record tombstone
// prevents tenant ID reuse), 10.2 (tenant id format)
//
// §12.8 states of the Phase-4 tombstone: "This tombstone prevents tenant
// ID reuse (which would violate audit trail referential integrity)". A
// harness that hard-codes a tenant id therefore cannot re-provision that
// tenant after its own teardown deleted it — the row is left non-active
// and every later session create against it is rejected. FreshTenantID is
// the harness's answer, so it must yield a distinct, well-formed id on
// every call, including from concurrent callers.
func TestFreshTenantIDIsUniquePerCall(t *testing.T) {
	t.Parallel()

	const (
		workers = 8
		perTask = 64
	)
	var (
		mu   sync.Mutex
		seen = make(map[string]struct{}, workers*perTask)
		wg   sync.WaitGroup
	)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids := make([]string, 0, perTask)
			for i := 0; i < perTask; i++ {
				ids = append(ids, FreshTenantID("sessiondriver-unit"))
			}
			mu.Lock()
			defer mu.Unlock()
			for _, id := range ids {
				seen[id] = struct{}{}
			}
		}()
	}
	wg.Wait()

	if got, want := len(seen), workers*perTask; got != want {
		t.Fatalf("FreshTenantID produced %d distinct ids across %d calls; every call must yield a fresh id", got, want)
	}
	for id := range seen {
		if !strings.HasPrefix(id, "sessiondriver-unit-") {
			t.Fatalf("id %q does not carry the caller's prefix", id)
		}
		if !tenantIDFormat.MatchString(id) {
			t.Fatalf("id %q does not satisfy the §10.2 tenant-id format", id)
		}
	}

	// An empty prefix still has to yield a usable id rather than one that
	// opens with the separator.
	if id := FreshTenantID(""); !tenantIDFormat.MatchString(id) || strings.HasPrefix(id, "-") {
		t.Fatalf("FreshTenantID(\"\") = %q, want a well-formed §10.2 tenant id", id)
	}
}

// rbacConfigStub is an httptest gateway stub covering the two admin calls
// BootstrapFreshTenant makes: POST /v1/admin/bootstrap and the
// GET/PUT rbac-config pair. It records what the driver sent so a test can
// assert on it.
type rbacConfigStub struct {
	mu sync.Mutex

	bootstrapped []string
	putBodies    []map[string]any
	putIfMatch   []string
	// defaultPolicy is what a GET reports for a tenant no PUT has touched.
	defaultPolicy string
	// policy holds the per-tenant override a PUT installed, keyed by
	// request path so two tenants do not share one row.
	policy map[string]string
}

func (s *rbacConfigStub) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/bootstrap":
			var body struct {
				Tenants []struct {
					ID string `json:"id"`
				} `json:"tenants"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			s.mu.Lock()
			for _, tn := range body.Tenants {
				s.bootstrapped = append(s.bootstrapped, tn.ID)
			}
			s.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"tenants":{"createdCount":1}}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/rbac-config"):
			s.mu.Lock()
			policy, ok := s.policy[r.URL.Path]
			if !ok {
				policy = s.defaultPolicy
			}
			s.mu.Unlock()
			w.Header().Set("ETag", `"7"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"noEnvironmentPolicy":"` + policy +
				`","identityProvider":{"issuer":"https://idp.acme.com"}}`))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/rbac-config"):
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			s.mu.Lock()
			s.putBodies = append(s.putBodies, body)
			s.putIfMatch = append(s.putIfMatch, r.Header.Get("If-Match"))
			if p, ok := body["noEnvironmentPolicy"].(string); ok {
				if s.policy == nil {
					s.policy = map[string]string{}
				}
				s.policy[r.URL.Path] = p
			}
			s.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(raw)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// spec: 12.8 (tenant deletion lifecycle; tombstone prevents tenant ID
// reuse), 10.6 (noEnvironmentPolicy), 11.1 (environment admission gate),
// 15.1 (admin rbac-config endpoint, If-Match precondition)
//
// Two independent gates reject a session create on a tenant the harness
// reuses or leaves at its defaults: the §12.8 lifecycle gate
// (TENANT_NOT_ACTIVE, once a prior run's teardown moved the row out of
// `active`) and the §11.1 environment gate (FORBIDDEN
// no_environment_policy_deny_all, because §10.6 resolves an unset
// noEnvironmentPolicy to deny-all). BootstrapFreshTenant has to clear
// both: bootstrap a never-before-used id, then PUT allow-all with the
// GET's ETag as If-Match while preserving the rest of the rbac-config.
func TestBootstrapFreshTenantProvisionsAnUnusedTenantThatAcceptsSessions(t *testing.T) {
	t.Parallel()

	stub := &rbacConfigStub{defaultPolicy: "deny-all"}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	d := newTestDriver(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first, err := d.BootstrapFreshTenant(ctx, "sessiondriver-smoke")
	if err != nil {
		t.Fatalf("BootstrapFreshTenant: %v", err)
	}
	second, err := d.BootstrapFreshTenant(ctx, "sessiondriver-smoke")
	if err != nil {
		t.Fatalf("BootstrapFreshTenant (second run): %v", err)
	}
	if first == second {
		t.Fatalf("two runs provisioned the same tenant id %q; the second inherits the first's deleted record", first)
	}

	stub.mu.Lock()
	bootstrapped := append([]string(nil), stub.bootstrapped...)
	putBodies := append([]map[string]any(nil), stub.putBodies...)
	putIfMatch := append([]string(nil), stub.putIfMatch...)
	stub.mu.Unlock()

	if len(bootstrapped) != 2 || bootstrapped[0] != first || bootstrapped[1] != second {
		t.Fatalf("bootstrap calls = %v, want [%s %s]", bootstrapped, first, second)
	}
	if len(putBodies) != 2 {
		t.Fatalf("rbac-config PUT count = %d, want 2 (one per provisioned tenant)", len(putBodies))
	}
	for i, body := range putBodies {
		if got := body["noEnvironmentPolicy"]; got != "allow-all" {
			t.Errorf("PUT %d set noEnvironmentPolicy = %v, want allow-all", i, got)
		}
		// Every other field the GET returned must survive the round-trip;
		// dropping identityProvider would silently reconfigure the tenant.
		if _, ok := body["identityProvider"]; !ok {
			t.Errorf("PUT %d dropped identityProvider from the rbac-config payload: %v", i, body)
		}
		if putIfMatch[i] != `"7"` {
			t.Errorf("PUT %d sent If-Match %q, want the ETag the GET returned", i, putIfMatch[i])
		}
	}

	// The driver must own the cleanup of both tenants it created.
	d.mu.Lock()
	tracked := len(d.bootstrappedTenants)
	_, haveFirst := d.bootstrappedTenants[first]
	_, haveSecond := d.bootstrappedTenants[second]
	d.mu.Unlock()
	if tracked != 2 || !haveFirst || !haveSecond {
		t.Fatalf("driver tracked %d tenants for cleanup (first=%v second=%v), want both", tracked, haveFirst, haveSecond)
	}
}

// spec: 10.6 (noEnvironmentPolicy), 15.1 (admin rbac-config endpoint)
//
// A tenant already carrying allow-all needs no write. Issuing a PUT anyway
// would burn the row's version on every run and make an If-Match race
// possible between the concurrently-running live-session suites.
func TestAllowSessionsWithNoEnvironmentSkipsAnAlreadyAllowingTenant(t *testing.T) {
	t.Parallel()

	stub := &rbacConfigStub{defaultPolicy: "allow-all"}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	d := newTestDriver(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := d.AllowSessionsWithNoEnvironment(ctx, "acme"); err != nil {
		t.Fatalf("AllowSessionsWithNoEnvironment: %v", err)
	}
	stub.mu.Lock()
	puts := len(stub.putBodies)
	stub.mu.Unlock()
	if puts != 0 {
		t.Fatalf("rbac-config PUT count = %d, want 0 for a tenant already at allow-all", puts)
	}
}

// spec: 10.6 (noEnvironmentPolicy), 15.1 (admin rbac-config endpoint)
//
// A rejected rbac-config write must surface as an error from
// BootstrapFreshTenant rather than returning a tenant that then fails
// every session create with FORBIDDEN no_environment_policy_deny_all,
// which reads as a session-surface defect instead of a setup failure.
func TestBootstrapFreshTenantReportsARejectedPolicyWrite(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/admin/bootstrap" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"tenants":{"createdCount":1}}`))
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("ETag", `"3"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"noEnvironmentPolicy":"deny-all"}`))
			return
		}
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"error":{"code":"PRECONDITION_FAILED"}}`))
	}))
	defer srv.Close()

	d := newTestDriver(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tenant, err := d.BootstrapFreshTenant(ctx, "sessiondriver-smoke")
	if err == nil {
		t.Fatalf("BootstrapFreshTenant returned tenant %q and no error on a rejected rbac-config write", tenant)
	}
	if tenant != "" {
		t.Errorf("BootstrapFreshTenant returned tenant %q alongside an error; want an empty id", tenant)
	}
	if !strings.Contains(err.Error(), "rbac-config") {
		t.Errorf("error %q does not name the failing rbac-config write", err)
	}
}
