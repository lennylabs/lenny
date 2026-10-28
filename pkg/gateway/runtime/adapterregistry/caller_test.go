// SPDX-License-Identifier: MIT

package adapterregistry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/common/scopes"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterregistry"
)

func mustScopes(t *testing.T, raw string) scopes.Set {
	t.Helper()
	s, err := scopes.Parse(raw)
	if err != nil {
		t.Fatalf("parse scope %q: %v", raw, err)
	}
	return s
}

// spec: 15 (External API Surface) "Shared Adapter Types" CallerIdentity and ActorClaim, 13.3 (Credential Flow) "Lenny JWT claim structure" act
//
// The §15 CallerIdentity projection copies the principal's subject, caller
// type, verbatim scope claim, and `act` claim. Act is nil exactly when the
// bearer carries no `act` claim, an actor that is not a Lenny session keeps
// an empty SessionID, and an absent scope claim projects to an empty Scope.
func TestCallerIdentityFromPrincipal_spec_15(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    authmw.Principal
		want adapterregistry.CallerIdentity
	}{
		{
			name: "direct caller without act",
			p: authmw.Principal{
				Subject: "alice@acme.com", TenantID: "acme", CallerType: "human",
				Scopes: mustScopes(t, "tools:sessions:read tools:sessions:write"),
			},
			want: adapterregistry.CallerIdentity{
				Sub: "alice@acme.com", CallerType: "human",
				Scope: "tools:sessions:read tools:sessions:write",
			},
		},
		{
			name: "delegated caller with a session actor",
			p: authmw.Principal{
				Subject: "alice@acme.com", TenantID: "acme", CallerType: "agent",
				Typ:    pkgauth.TokenA2ADelegation,
				Scopes: mustScopes(t, "tools:sessions:read"),
				Act:    &jwt.ActorClaim{Subject: "bob@acme.com", TenantID: "acme", SessionID: "sess_bob", DelegationDepth: 1},
			},
			want: adapterregistry.CallerIdentity{
				Sub: "alice@acme.com", CallerType: "agent", Scope: "tools:sessions:read",
				Act: &adapterregistry.ActorClaim{Sub: "bob@acme.com", TenantID: "acme", SessionID: "sess_bob", DelegationDepth: 1},
			},
		},
		{
			name: "delegated caller with a root non-session actor",
			p: authmw.Principal{
				Subject: "svc-ci", TenantID: "acme", CallerType: "service",
				Act: &jwt.ActorClaim{Subject: "carol@acme.com", TenantID: "acme"},
			},
			want: adapterregistry.CallerIdentity{
				Sub: "svc-ci", CallerType: "service",
				Act: &adapterregistry.ActorClaim{Sub: "carol@acme.com", TenantID: "acme"},
			},
		},
		{
			name: "zero principal",
			p:    authmw.Principal{},
			want: adapterregistry.CallerIdentity{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := adapterregistry.CallerIdentityFromPrincipal(tc.p)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("CallerIdentityFromPrincipal = %+v (act %+v), want %+v (act %+v)", got, got.Act, tc.want, tc.want.Act)
			}
		})
	}
}

// spec: 15 (External API Surface) "Shared Adapter Types" CallerIdentity ("Adapters MUST treat CallerIdentity as read-only")
//
// The projected actor is a fresh value: an adapter that edits it cannot
// reach the principal's verified `act` claim, and a nil claim projects to
// nil.
func TestActorClaimFromJWTIsIndependent_spec_15(t *testing.T) {
	if adapterregistry.ActorClaimFromJWT(nil) != nil {
		t.Fatal("ActorClaimFromJWT(nil) is non-nil")
	}
	src := &jwt.ActorClaim{Subject: "bob@acme.com", TenantID: "acme", SessionID: "sess_bob", DelegationDepth: 2}
	dst := adapterregistry.ActorClaimFromJWT(src)
	dst.Sub = "dave@acme.com"
	dst.DelegationDepth = 9
	if src.Subject != "bob@acme.com" || src.DelegationDepth != 2 {
		t.Errorf("source claim = %+v after editing the projection", src)
	}
}

// spec: 15 (External API Surface) "Shared Adapter Types" SessionMetadata.CallerIdentity, 13.3 (Credential Flow) "Lenny JWT claim structure" act
//
// A delegated bearer's `act` claim travels from the verified token through
// the gateway auth middleware's principal into SessionMetadata, and the
// registry delivers it unchanged to every adapter's OnSessionCreated.
func TestDelegatedBearerActReachesOnSessionCreated_spec_15(t *testing.T) {
	signer := jwt.NewHMACSigner("test", []byte("secret"))
	tok, err := signer.Sign(jwt.Claims{
		TenantID: "acme", Subject: "alice@acme.com", CallerType: "agent",
		Typ: pkgauth.TokenA2ADelegation, DelegationDepth: 2, Scope: "tools:sessions:write",
		Act:    &jwt.ActorClaim{Subject: "bob@acme.com", TenantID: "acme", SessionID: "sess_bob", DelegationDepth: 1},
		Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	r := adapterregistry.New()
	a := &fakeAdapter{name: "a", prefix: "/a"}
	if err := r.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p, ok := authmw.FromContext(req.Context())
		if !ok {
			t.Error("no principal on the request context")
		}
		m := adapterregistry.SessionMetadata{
			TenantID: p.TenantID, SessionID: "sess_alice", RuntimeName: "claude-code",
			DelegationDepth: 2, CallerIdentity: adapterregistry.CallerIdentityFromPrincipal(p),
		}
		if errs := r.DispatchSessionCreated(context.Background(), m); len(errs) != 0 {
			t.Errorf("DispatchSessionCreated errs: %v", errs)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := authmw.Wrap(inner, authmw.Options{Verifier: signer, MultiTenant: true, Registry: registeredTenants{}})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.created) != 1 {
		t.Fatalf("OnSessionCreated calls = %d, want 1", len(a.created))
	}
	want := adapterregistry.CallerIdentity{
		Sub: "alice@acme.com", CallerType: "agent", Scope: "tools:sessions:write",
		Act: &adapterregistry.ActorClaim{Sub: "bob@acme.com", TenantID: "acme", SessionID: "sess_bob", DelegationDepth: 1},
	}
	got := a.created[0].CallerIdentity
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CallerIdentity = %+v (act %+v), want %+v (act %+v)", got, got.Act, want, want.Act)
	}
	if got.Act.TenantID != a.created[0].TenantID {
		t.Errorf("ActorClaim.TenantID = %q, SessionMetadata.TenantID = %q; §15 requires equality", got.Act.TenantID, a.created[0].TenantID)
	}
}

// registeredTenants is an auth.TenantRegistry that recognizes every
// tenant.
type registeredTenants struct{}

func (registeredTenants) IsRegistered(string) (bool, error) { return true, nil }
