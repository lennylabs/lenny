// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// TestCreatePlaygroundRejectsRuntimeOutsideAllowedRuntimes_spec_27_5_190 pins
// the §27.5 line 190 / §27.9 line 250 authorization boundary: an
// origin=playground create against a runtime that playground.allowedRuntimes
// excludes is rejected with 403 FORBIDDEN, so the §27.4 picker filter is not
// just a display affordance a caller could bypass by POSTing directly.
func TestCreatePlaygroundRejectsRuntimeOutsideAllowedRuntimes_spec_27_5_190(t *testing.T) {
	srv, _ := seedNoEnvServer(t, "sess_hidden", tenantstore.NoEnvPolicyAllowAll, tenantstore.NoEnvPolicyDenyAll)
	srv.SetPlaygroundCaps(playgroundCapsFake{idleSeconds: 300, sessionMins: 30, hidden: "claude-code"}, nil)

	rr := createRequestAs(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	}, authmw.Principal{Subject: "alice@acme.com", TenantID: "acme", Origin: "playground"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "runtime_not_playground_visible") {
		t.Errorf("rejection body must carry the playground-visibility reason, got %q", rr.Body.String())
	}
}

// TestCreateNonPlaygroundIgnoresAllowedRuntimes_spec_27_5_190 pins that the
// playground.allowedRuntimes boundary is scoped to the origin=playground claim:
// a non-playground caller creating against the same runtime is admitted, so the
// playground value never narrows the shared §15.1 create surface.
func TestCreateNonPlaygroundIgnoresAllowedRuntimes_spec_27_5_190(t *testing.T) {
	srv, _ := seedNoEnvServer(t, "sess_plain_hidden", tenantstore.NoEnvPolicyAllowAll, tenantstore.NoEnvPolicyDenyAll)
	srv.SetPlaygroundCaps(playgroundCapsFake{idleSeconds: 300, sessionMins: 30, hidden: "claude-code"}, nil)

	rr := createRequestAs(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	}, authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("non-playground create against an allowedRuntimes-excluded runtime must be admitted: status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreatePlaygroundAdmitsVisibleRuntime_spec_27_5_190 pins that an
// origin=playground create against a runtime the allowedRuntimes globs admit
// is not blocked by the §27.4 boundary.
func TestCreatePlaygroundAdmitsVisibleRuntime_spec_27_5_190(t *testing.T) {
	srv, _ := seedNoEnvServer(t, "sess_visible", tenantstore.NoEnvPolicyAllowAll, tenantstore.NoEnvPolicyDenyAll)
	// hidden names a different runtime, so claude-code stays visible.
	srv.SetPlaygroundCaps(playgroundCapsFake{idleSeconds: 300, sessionMins: 30, hidden: "gpt-agent"}, nil)

	rr := createRequestAs(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	}, authmw.Principal{Subject: "alice@acme.com", TenantID: "acme", Origin: "playground"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("playground create against a visible runtime must be admitted: status = %d, body=%s", rr.Code, rr.Body.String())
	}
}
