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

	tenant := "sessiondriver-smoke-tenant"
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	sess, err := d.CreateAndStart(ctx, tenant, sessiondriver.EchoRuntimeSidecar)
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

	tenant := "sessiondriver-get-tenant"
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	sess, err := d.CreateAndStart(ctx, tenant, sessiondriver.EchoRuntimeSidecar)
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
