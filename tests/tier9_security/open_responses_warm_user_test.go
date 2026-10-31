// SPDX-License-Identifier: MIT

//go:build security

package tier9_security_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// orHoldingBinder creates a running session row per bind and reports hold,
// as the gateway's binder does for a multi_turn runtime with injection
// supported. It records each created session's owner, the request
// principal's subject.
type orHoldingBinder struct {
	store sessionstore.Store
	mu    sync.Mutex
	users []string
}

func (b *orHoldingBinder) BindSingleShot(ctx context.Context, spec translator.SingleShotSpec) (translator.SingleShotBinding, error) {
	b.mu.Lock()
	b.users = append(b.users, ownerprincipal.Subject(ctx))
	b.mu.Unlock()
	id := session.NewID()
	now := time.Now().UTC()
	err := b.store.Create(ctx, sessionstore.Session{
		ID: id, TenantID: spec.TenantID, UserID: ownerprincipal.Subject(ctx), RuntimeRef: spec.RuntimeRef,
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	})
	return translator.SingleShotBinding{SessionID: id, Hold: true}, err
}

// spec: §10.2 (Session ownership) rules 4, 5, and 7, §15 (Open Responses conversation sessions)
// diagnosis: a principal that does not own alice's held session either
// resolved one of its responses (bob/user read it through GET
// /v1/responses/{id} or continued it with previous_response_id, even when
// his request named alice as its `user`), or continued it warm on alice's
// held pod (a tenant-admin that may read the session ran its turn on it), or
// alice herself lost her warm continuation when her request named another
// `user`. The resolve step in continuity.resolve must ask the wired
// SessionAccess for a read and answer the native 404 on a refusal, and warm
// condition 4 in warmEligible (pkg/gateway/environment/translator/
// open_responses.go) must compare the request principal's subject with the
// row's owner rather than the request's `user` field.
func TestOpenResponsesNonOwnerNeitherResolvesNorContinuesWarm_spec_10_2(t *testing.T) {
	f, binder, fwd := newHeldORFixture(t)
	head := f.post(t, "acme", "one", "")
	held := orSessionOf(t, head)
	bob := authmw.Principal{Subject: "bob", TenantID: "acme", Roles: []auth.Role{auth.RoleUser}}
	admin := authmw.Principal{Subject: "carol", TenantID: "acme", Roles: []auth.Role{auth.RoleTenantAdmin}}

	assertHeldUntouched := func(t *testing.T, wantResponses int) {
		t.Helper()
		row, err := f.sessions.Get(context.Background(), "acme", held)
		if err != nil {
			t.Fatal(err)
		}
		if len(row.Responses) != wantResponses {
			t.Errorf("alice's held session has %d responses, want %d", len(row.Responses), wantResponses)
		}
	}

	t.Run("bob/user GET of alice's response answers the native 404", func(t *testing.T) {
		code, raw := f.doAsPrincipal(t, bob, http.MethodGet, "/v1/responses/"+head, "acme", nil)
		assertNative404(t, code, raw)
		if binder.binds() != 1 {
			t.Errorf("binds = %d, want 1 (alice's own create only)", binder.binds())
		}
	})

	t.Run("bob/user continuation naming alice as user answers the native 404 with no bind", func(t *testing.T) {
		sends := f.exec.sends()
		body := map[string]any{"model": "echo", "input": "bob's turn", "user": "alice", "previous_response_id": head}
		code, raw := f.doAsPrincipal(t, bob, http.MethodPost, "/v1/responses", "acme", body)
		assertNative404(t, code, raw)
		if binder.binds() != 1 || f.exec.sends() != sends || fwd.forwards.Load() != 0 {
			t.Errorf("binds = %d, sends = %d (was %d), forwards = %d; want no bind, dispatch, or forward",
				binder.binds(), f.exec.sends(), sends, fwd.forwards.Load())
		}
		assertHeldUntouched(t, 1)
	})

	t.Run("tenant-admin continuation goes cold on a session it owns", func(t *testing.T) {
		body := map[string]any{"model": "echo", "input": "admin's turn", "previous_response_id": head}
		code, raw := f.doAsPrincipal(t, admin, http.MethodPost, "/v1/responses", "acme", body)
		if code != http.StatusOK {
			t.Fatalf("tenant-admin continuation: %d, body=%s", code, raw)
		}
		var out struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &out)
		cold := orSessionOf(t, out.ID)
		if cold == held {
			t.Fatalf("the tenant-admin's continuation ran on alice's held session %s", held)
		}
		row, err := f.sessions.Get(context.Background(), "acme", cold)
		if err != nil {
			t.Fatal(err)
		}
		if row.UserID != admin.Subject {
			t.Errorf("cold session owner = %q, want the tenant-admin %q", row.UserID, admin.Subject)
		}
		if fwd.forwards.Load() != 0 {
			t.Errorf("forwards = %d, want 0: a cold turn is never forwarded to the holder", fwd.forwards.Load())
		}
		assertHeldUntouched(t, 1)
	})

	t.Run("alice's continuation naming another user stays warm", func(t *testing.T) {
		binds := binder.binds()
		body := map[string]any{"model": "echo", "input": "alice again", "user": "bob", "previous_response_id": head}
		code, raw := f.do(t, http.MethodPost, "/v1/responses", "acme", body)
		if code != http.StatusOK {
			t.Fatalf("alice's continuation: %d, body=%s", code, raw)
		}
		var out struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &out)
		if orSessionOf(t, out.ID) != held || binder.binds() != binds {
			t.Errorf("alice's continuation %q bound %d sessions; want a warm turn on %s with no bind",
				out.ID, binder.binds()-binds, held)
		}
		assertHeldUntouched(t, 2)
	})
}

// assertNative404 fails unless the answer is the native OpenAI 404 envelope.
func assertNative404(t *testing.T, code int, raw []byte) {
	t.Helper()
	var env struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	if code != http.StatusNotFound || env.Error.Type != "not_found_error" {
		t.Fatalf("answer = %d %s, want the native 404 not_found_error", code, raw)
	}
}
