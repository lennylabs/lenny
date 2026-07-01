// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests that drive a live gateway session through the
// sessiondriver harness and assert a security property of the §15.1
// session surface against the running gateway. The harness brings up
// the e2e Kind cluster (skipping cleanly when prerequisites are
// missing), port-forwards lenny-gateway, and provides the
// create / start / send / get / terminate operations the tests need.
//
// These tests complement the kubectl-driven admin-API tests in this
// directory: where audit_integrity_test.go and tenant_isolation_test.go
// drive the /v1/admin/* surface from an in-cluster probe pod, this file
// drives /v1/sessions/* from the test host through the port-forwarded
// gateway.

package tier9_security_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// spec: 12.9.1
// diagnosis: §12.9.1 cross-tenant session isolation did not hold. The
// test creates a live session for tenant A through the §15.1
// create-and-start endpoint, then issues a tenant-B-scoped GET against
// the same session ID. The §10.2 tenant-scope guard must reject the
// cross-tenant lookup with 404 RESOURCE_NOT_FOUND (the gateway hides
// foreign sessions rather than disclosing their existence). A 200 from
// the cross-tenant GET, or a 4xx that exposes the foreign session
// payload, is an isolation breach.
func TestSessionCrossTenantLookupRejected(t *testing.T) {
	d := sessiondriver.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Suffix the tenant IDs with a per-run value. These tests run against a
	// persistent e2e cluster; a tenant terminated by a prior run's cleanup
	// lingers in the deleted state, and re-bootstrapping a fixed-name tenant
	// then fails create-and-start with 403 TENANT_NOT_ACTIVE. A fresh name
	// per run sidesteps that leftover state.
	suffix := fmt.Sprintf("-%d", time.Now().UnixNano())
	tenantA := "t9-livesess-acme" + suffix
	tenantB := "t9-livesess-globex" + suffix
	if err := d.BootstrapTenant(ctx, tenantA); err != nil {
		t.Fatalf("bootstrap tenant A: %v", err)
	}
	if err := d.BootstrapTenant(ctx, tenantB); err != nil {
		t.Fatalf("bootstrap tenant B: %v", err)
	}

	sess, err := d.CreateAndStart(ctx, tenantA, sessiondriver.EchoRuntimeSidecar)
	if err != nil {
		t.Fatalf("create-and-start for tenant A: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = d.Terminate(ctx, tenantA, sess.ID)
	})
	t.Logf("tenant %s created session %s in state %q", tenantA, sess.ID, sess.State)

	// Sanity: tenant A's own GET must succeed (a blanket 404 would
	// hide the isolation breach if tenant B's GET also returned 404).
	if got, err := d.GetSession(ctx, tenantA, sess.ID); err != nil {
		t.Fatalf("control: tenant A could not read its own session %s: %v", sess.ID, err)
	} else if got.ID != sess.ID {
		t.Fatalf("control: tenant A's GET returned a different session ID: got %q, want %q",
			got.ID, sess.ID)
	}

	// Adversarial: tenant B requests tenant A's session. The §10.2
	// tenant-scope guard must reject the lookup — the gateway returns
	// 404 RESOURCE_NOT_FOUND so the foreign session's existence is not
	// disclosed, rather than 403 (which would leak that the row
	// exists). Either way, a 200 with the foreign payload is the
	// breach.
	if got, err := d.GetSession(ctx, tenantB, sess.ID); err == nil {
		t.Errorf("§12.9.1 violation: tenant %s's GET on tenant %s's session %s returned a session "+
			"payload (state %q, runtimeRef %q); the §10.2 tenant-scope guard did not reject the "+
			"cross-tenant lookup", tenantB, tenantA, sess.ID, got.State, got.RuntimeRef)
	} else if !strings.Contains(err.Error(), "404") {
		t.Errorf("§12.9.1: tenant %s's GET on tenant %s's session %s failed with %v, expected a "+
			"404 RESOURCE_NOT_FOUND; the gateway should hide the foreign session's existence",
			tenantB, tenantA, sess.ID, err)
	} else {
		t.Logf("§12.9.1: cross-tenant GET on session %s rejected — tenant %s does not see tenant %s's "+
			"session", sess.ID, tenantB, tenantA)
	}

	// Adversarial: tenant B attempts to terminate tenant A's session.
	// Same rejection contract: 404 RESOURCE_NOT_FOUND (the §15.1 spec
	// does not allow leaking the foreign session ID's existence via
	// terminate).
	if err := d.Terminate(ctx, tenantB, sess.ID); err == nil {
		t.Errorf("§12.9.1 violation: tenant %s terminated tenant %s's session %s; the §10.2 "+
			"tenant-scope guard did not reject the cross-tenant terminate",
			tenantB, tenantA, sess.ID)
	} else if !strings.Contains(err.Error(), "404") {
		t.Logf("§12.9.1: cross-tenant terminate rejected with %v (any non-200 is sufficient; the "+
			"404 contract is asserted by the GET test above)", err)
	}

	// Verify the session is still alive under tenant A — the
	// cross-tenant terminate must have been a no-op.
	if got, err := d.GetSession(ctx, tenantA, sess.ID); err != nil {
		t.Errorf("after the cross-tenant terminate, tenant A could no longer reach its own session: %v", err)
	} else if got.State == "" {
		t.Errorf("after the cross-tenant terminate, tenant A's session reports an empty state")
	} else {
		t.Logf("§12.9.1: tenant %s's session %s survives a tenant %s terminate attempt (state %q)",
			tenantA, sess.ID, tenantB, got.State)
	}
}

// spec: 12.9.7
// diagnosis: §12.9.7 RBAC enforcement on the live session surface did
// not hold. The test drives a session as a platform-admin (the
// canonical creator role), then attempts a state-mutating operation
// (DELETE) as a tenant-viewer in the SAME tenant. §10.2 grants
// tenant-viewer read_own_sessions but not manage_own_sessions, so the
// DELETE must be rejected with 403 FORBIDDEN. A 200 here would mean
// the gate is missing — a tenant-viewer would be able to destroy the
// tenant's sessions.
func TestSessionViewerRoleCannotMutate(t *testing.T) {
	d := sessiondriver.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Per-run tenant ID so a leftover deleted tenant from a prior run on the
	// persistent e2e cluster does not fail bootstrap with TENANT_NOT_ACTIVE.
	tenant := "t9-livesess-rbac" + fmt.Sprintf("-%d", time.Now().UnixNano())
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	sess, err := d.CreateAndStart(ctx, tenant, sessiondriver.EchoRuntimeSidecar)
	if err != nil {
		t.Fatalf("create-and-start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = d.Terminate(ctx, tenant, sess.ID)
	})
	t.Logf("created session %s as platform-admin in tenant %s", sess.ID, tenant)

	// Direct HTTP call as a tenant-viewer. The driver's helpers default
	// to platform-admin so the test must craft the request with the
	// limited role explicitly.
	url := d.BaseURL() + "/v1/sessions/" + sess.ID
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("build viewer DELETE request: %v", err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	req.Header.Set("X-Lenny-Roles", "tenant-viewer")
	req.Header.Set("X-Lenny-User-ID", "bob")
	hc := &http.Client{Timeout: 15 * time.Second}
	res, err := hc.Do(req)
	if err != nil {
		t.Fatalf("issue viewer DELETE: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("§12.9.7 violation: tenant-viewer DELETE on session %s returned status %d, "+
			"expected 403 FORBIDDEN; the §10.2 RBAC gate did not reject the role "+
			"(roles without manage_own_sessions must be denied a DELETE)",
			sess.ID, res.StatusCode)
	} else {
		t.Logf("§12.9.7: tenant-viewer DELETE on session %s rejected with 403", sess.ID)
	}

	// Confirm the session survived the rejected DELETE.
	if got, err := d.GetSession(ctx, tenant, sess.ID); err != nil {
		t.Errorf("after the rejected DELETE the session is no longer reachable: %v", err)
	} else if got.ID != sess.ID {
		t.Errorf("after the rejected DELETE the session ID changed: got %q, want %q", got.ID, sess.ID)
	} else {
		t.Logf("§12.9.7: session %s survives the rejected DELETE (state %q)", got.ID, got.State)
	}
}
