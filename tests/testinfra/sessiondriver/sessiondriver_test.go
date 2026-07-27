// SPDX-License-Identifier: MIT

// Smoke tests for the live-session harness. Both tests skip when the
// Kind cluster is not up; on a host where the install ran, they bring
// the gateway port-forward online, drive one create + terminate cycle,
// and clean up the synthetic tenant. The chaos and security suites
// import the package; these tests verify the package itself stays
// healthy under refactors.

package sessiondriver_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// spec: 15.1
// diagnosis: the live-session harness cannot complete a session
// round-trip against the e2e gateway. The test bootstraps a synthetic
// tenant, runs the §15.1 create-and-start + terminate lifecycle through
// the port-forwarded gateway, and asserts the session reports state
// "running" on create and is terminable. A failure means the port-
// forward did not establish, the dev-header auth was not honoured, or
// the warm pool did not back the session.
func TestHarnessCreateAndTerminate(t *testing.T) {
	d := sessiondriver.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// A per-run tenant id rather than a fixed one. Close issues DELETE
	// /v1/admin/tenants/{id} for every tenant it bootstrapped, which enters
	// the §12.8 deletion lifecycle and leaves the row non-active; a reused
	// id then fails every create with 403 TENANT_NOT_ACTIVE on the next run
	// against this same long-lived cluster.
	tenant, err := d.BootstrapFreshTenant(ctx, "sessiondriver-smoke")
	if err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	sess, err := d.CreateAndStart(ctx, tenant, sessiondriver.EchoRuntimeSidecar)
	if errors.Is(err, sessiondriver.ErrPoolNotReady) {
		// The §4.6 warm pool never settled an idle pod inside the retry
		// window. This test covers the §15.1 session lifecycle through the
		// harness rather than pool warm-up, so skip on the unready
		// precondition as the sibling tier-9 live-session tests do.
		t.Skipf("precondition not met: warm pool not ready, no session to drive: %v", err)
	}
	if err != nil {
		t.Fatalf("create-and-start: %v", err)
	}
	if sess.ID == "" {
		t.Fatalf("create-and-start returned an empty session ID: %+v", sess)
	}
	if sess.State != "running" {
		t.Fatalf("create-and-start returned state %q, expected \"running\" (session %+v)", sess.State, sess)
	}
	t.Logf("created session %s for tenant %s in state %q", sess.ID, tenant, sess.State)

	if err := d.Terminate(ctx, tenant, sess.ID); err != nil {
		t.Fatalf("terminate session: %v", err)
	}
	t.Logf("terminated session %s", sess.ID)
}

// spec: 15.1
// diagnosis: the live-session harness cannot observe a session row
// transition. The test creates one session, polls GET /v1/sessions/{id}
// until the row is reachable, asserts it returns the expected state,
// then terminates. A failure means GET is not honouring the dev-header
// tenant scope or the row is missing after create.
func TestHarnessGetSession(t *testing.T) {
	d := sessiondriver.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Per-run tenant id, for the same §12.8 reason as the sibling test
	// above: a fixed id inherits the prior run's non-active tenant record.
	tenant, err := d.BootstrapFreshTenant(ctx, "sessiondriver-get")
	if err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	sess, err := d.CreateAndStart(ctx, tenant, sessiondriver.EchoRuntimeSidecar)
	if errors.Is(err, sessiondriver.ErrPoolNotReady) {
		t.Skipf("precondition not met: warm pool not ready, no session to read back: %v", err)
	}
	if err != nil {
		t.Fatalf("create-and-start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = d.Terminate(ctx, tenant, sess.ID)
	})

	got, err := d.GetSession(ctx, tenant, sess.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.ID != sess.ID {
		t.Fatalf("get returned a different session ID: got %q, want %q", got.ID, sess.ID)
	}
	if got.State == "" {
		t.Fatalf("get returned an empty state for session %s", sess.ID)
	}
	t.Logf("get session %s reports state %q", got.ID, got.State)
}
