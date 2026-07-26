// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// playgroundCapsFake satisfies sessionserver.PlaygroundCapResolver with the
// §27.6 min() math so the create-path wiring can be exercised without the
// playground package.
type playgroundCapsFake struct {
	idleSeconds int
	sessionMins int
	// hidden names a single runtime the §27.2 playground.allowedRuntimes
	// list excludes; empty means every runtime is visible.
	hidden string
}

func (f playgroundCapsFake) RuntimeVisible(name string) bool {
	return f.hidden == "" || name != f.hidden
}

func (f playgroundCapsFake) EffectiveIdleSeconds(runtimeIdleSeconds int) int {
	if runtimeIdleSeconds > 0 && runtimeIdleSeconds < f.idleSeconds {
		return runtimeIdleSeconds
	}
	return f.idleSeconds
}

func (f playgroundCapsFake) EffectiveSessionMinutes(runtimeMinutes int) int {
	if runtimeMinutes > 0 && runtimeMinutes < f.sessionMins {
		return runtimeMinutes
	}
	return f.sessionMins
}

// createSessionEnvelope is the slice of the §15.1 create response this test
// inspects: the §27.6 origin label and the per-session timeouts.
type createSessionEnvelope struct {
	ID       string `json:"id"`
	Origin   string `json:"origin"`
	Timeouts *struct {
		MaxSessionAgeSeconds int64 `json:"maxSessionAgeSeconds"`
		MaxIdleSeconds       int64 `json:"maxIdleSeconds"`
	} `json:"timeouts"`
}

// TestCreatePlaygroundOriginStampsCapsAndLabel_spec_27_6 — a §27.3
// origin=playground create lands the §27.6 idle/duration caps and the
// origin=playground audit label on both the response and the persisted row,
// and increments the §27.8 created counter.
func TestCreatePlaygroundOriginStampsCapsAndLabel_spec_27_6(t *testing.T) {
	srv, store := seedNoEnvServer(t, "sess_pg", tenantstore.NoEnvPolicyAllowAll, tenantstore.NoEnvPolicyDenyAll)
	created := 0
	srv.SetPlaygroundCaps(playgroundCapsFake{idleSeconds: 300, sessionMins: 30},
		func(rt string) { created++ })

	rr := createRequestAs(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	}, authmw.Principal{Subject: "alice@acme.com", TenantID: "acme", Origin: "playground"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	var env createSessionEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Origin != "playground" {
		t.Errorf("response origin = %q, want playground", env.Origin)
	}
	if env.Timeouts == nil {
		t.Fatal("response carries no timeouts; want playground caps stamped")
	}
	if env.Timeouts.MaxSessionAgeSeconds != 1800 {
		t.Errorf("response maxSessionAgeSeconds = %d, want 1800", env.Timeouts.MaxSessionAgeSeconds)
	}
	if env.Timeouts.MaxIdleSeconds != 300 {
		t.Errorf("response maxIdleSeconds = %d, want 300", env.Timeouts.MaxIdleSeconds)
	}
	if created != 1 {
		t.Errorf("playground created counter = %d, want 1", created)
	}

	// The persisted row must carry the same label + caps so a GET on a
	// coordinator-handed-off replica sees them.
	row, err := store.Get(context.Background(), "acme", "sess_pg")
	if err != nil {
		t.Fatalf("get persisted row: %v", err)
	}
	if row.Origin != "playground" {
		t.Errorf("persisted origin = %q, want playground", row.Origin)
	}
	if row.Timeouts == nil || row.Timeouts.MaxSessionAgeSeconds != 1800 || row.Timeouts.MaxIdleSeconds != 300 {
		t.Errorf("persisted timeouts = %+v, want {1800,300}", row.Timeouts)
	}
}

// TestCreateNonPlaygroundLeavesOriginUnset_spec_27_6 — a non-playground create
// carries no origin label, no playground caps, and never increments the
// counter, so the §27.6 enforcement is scoped to the origin claim.
func TestCreateNonPlaygroundLeavesOriginUnset_spec_27_6(t *testing.T) {
	srv, store := seedNoEnvServer(t, "sess_plain", tenantstore.NoEnvPolicyAllowAll, tenantstore.NoEnvPolicyDenyAll)
	created := 0
	srv.SetPlaygroundCaps(playgroundCapsFake{idleSeconds: 300, sessionMins: 30},
		func(string) { created++ })

	rr := createRequestAs(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	}, authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	var env createSessionEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Origin != "" {
		t.Errorf("response origin = %q, want empty", env.Origin)
	}
	if env.Timeouts != nil {
		t.Errorf("response timeouts = %+v, want nil (no playground caps)", env.Timeouts)
	}
	if created != 0 {
		t.Errorf("playground created counter = %d, want 0", created)
	}

	row, err := store.Get(context.Background(), "acme", "sess_plain")
	if err != nil {
		t.Fatalf("get persisted row: %v", err)
	}
	if row.Origin != "" || row.Timeouts != nil {
		t.Errorf("persisted row carries origin=%q timeouts=%+v, want both unset", row.Origin, row.Timeouts)
	}
}
