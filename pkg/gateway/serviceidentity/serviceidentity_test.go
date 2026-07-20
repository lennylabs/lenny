// SPDX-License-Identifier: MIT

package serviceidentity_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/serviceidentity"
)

// fakeVerifier stands in for the Kubernetes TokenReview. It records what it
// was asked and answers with a fixed verdict.
type fakeVerifier struct {
	username string
	err      error
	calls    int
	audience string
}

func (f *fakeVerifier) VerifyUser(_ context.Context, _, audience string) (string, error) {
	f.calls++
	f.audience = audience
	return f.username, f.err
}

// saToken builds a token whose unverified payload claims audiences, which is
// what the resolver screens on before paying for a TokenReview.
func saToken(audiences ...string) string {
	payload, _ := json.Marshal(map[string]any{"aud": audiences, "sub": "system:serviceaccount:lenny-system:lenny-ops-sa"})
	return "h." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

const opsSA = "system:serviceaccount:lenny-system:lenny-ops-sa"

// spec: 25.4 (lenny-ops calls the gateway admin API as a dedicated service
// account holding platform-admin, through the gateway's standard RBAC), 10.2
// line 227 (the gateway validates the projected token on every pod→gateway
// request)
func TestResolverAdmitsTheGrantedServiceAccount_spec_25_4(t *testing.T) {
	v := &fakeVerifier{username: opsSA}
	r := serviceidentity.New(serviceidentity.Config{
		Verifier: v,
		Audience: "lenny-gateway",
		Roles:    map[string][]auth.Role{opsSA: {auth.RolePlatformAdmin}},
		TenantID: "platform",
	})

	id, ok, err := r.ResolveService(context.Background(), saToken("lenny-gateway"))
	if err != nil || !ok {
		t.Fatalf("ResolveService = (%+v, %t, %v), want the granted account admitted", id, ok, err)
	}
	if id.Subject != opsSA || id.TenantID != "platform" {
		t.Errorf("identity = %+v, want subject %q in tenant platform", id, opsSA)
	}
	if len(id.Roles) != 1 || id.Roles[0] != auth.RolePlatformAdmin {
		t.Errorf("roles = %v, want the platform-admin grant §25.4 defines", id.Roles)
	}
	if v.audience != "lenny-gateway" {
		t.Errorf("TokenReview audience = %q, want the configured deployment audience", v.audience)
	}
}

// spec: 25.4 (the grant is per service account; all calls go through the
// gateway's standard RBAC with no backdoor)
func TestResolverRefusesAnUngrantedServiceAccount_spec_25_4(t *testing.T) {
	v := &fakeVerifier{username: "system:serviceaccount:lenny-system:some-other-sa"}
	r := serviceidentity.New(serviceidentity.Config{
		Verifier: v,
		Audience: "lenny-gateway",
		Roles:    map[string][]auth.Role{opsSA: {auth.RolePlatformAdmin}},
	})

	id, ok, err := r.ResolveService(context.Background(), saToken("lenny-gateway"))
	if ok || err != nil {
		t.Fatalf("ResolveService = (%+v, %t, %v), want an ungranted account refused: an authentic token "+
			"for any other service account must not carry the platform-admin grant", id, ok, err)
	}
}

// spec: 25.4 (all calls go through the gateway's standard RBAC), 10.2 line 227
func TestResolverFailsClosed_spec_25_4(t *testing.T) {
	granted := map[string][]auth.Role{opsSA: {auth.RolePlatformAdmin}}

	t.Run("verification failure is not admitted", func(t *testing.T) {
		v := &fakeVerifier{err: errors.New("tokenreview request failed")}
		r := serviceidentity.New(serviceidentity.Config{Verifier: v, Audience: "lenny-gateway", Roles: granted})
		if _, ok, err := r.ResolveService(context.Background(), saToken("lenny-gateway")); ok || err == nil {
			t.Fatalf("ok=%t err=%v, want a refusal with the failure reported: an unreachable apiserver "+
				"must deny rather than admit", ok, err)
		}
	})

	t.Run("no configured audience admits nothing", func(t *testing.T) {
		v := &fakeVerifier{username: opsSA}
		r := serviceidentity.New(serviceidentity.Config{Verifier: v, Roles: granted})
		if _, ok, _ := r.ResolveService(context.Background(), saToken("lenny-gateway")); ok {
			t.Fatal("an unconfigured audience admitted a token; without an audience any token the " +
				"cluster issuer signed would be accepted")
		}
		if v.calls != 0 {
			t.Errorf("verifier called %d times with no audience configured, want 0", v.calls)
		}
	})

	t.Run("no verifier admits nothing", func(t *testing.T) {
		r := serviceidentity.New(serviceidentity.Config{Audience: "lenny-gateway", Roles: granted})
		if _, ok, _ := r.ResolveService(context.Background(), saToken("lenny-gateway")); ok {
			t.Fatal("a resolver with no token verifier admitted a token unverified")
		}
	})

	t.Run("no granted account admits nothing", func(t *testing.T) {
		v := &fakeVerifier{username: opsSA}
		r := serviceidentity.New(serviceidentity.Config{Verifier: v, Audience: "lenny-gateway"})
		if _, ok, _ := r.ResolveService(context.Background(), saToken("lenny-gateway")); ok {
			t.Fatal("a resolver with no configured grant admitted a service account")
		}
	})
}

// spec: 10.2 line 227 (audience binding: a token minted for another deployment
// is rejected even though the same cluster issuer signed it)
func TestResolverScreensTheAudienceBeforeTheTokenReview_spec_10_2(t *testing.T) {
	v := &fakeVerifier{username: opsSA}
	r := serviceidentity.New(serviceidentity.Config{
		Verifier: v,
		Audience: "lenny-gateway",
		Roles:    map[string][]auth.Role{opsSA: {auth.RolePlatformAdmin}},
	})

	for _, token := range []string{saToken("some-other-deployment"), "not-a-jwt", ""} {
		if _, ok, err := r.ResolveService(context.Background(), token); ok || err != nil {
			t.Errorf("token %q resolved (ok=%t err=%v), want a refusal", token, ok, err)
		}
	}
	if v.calls != 0 {
		t.Errorf("verifier called %d times for tokens claiming another audience, want 0: an arbitrary "+
			"rejected bearer must not cost an apiserver round-trip", v.calls)
	}

	// A token listing the deployment audience among several still reaches the
	// authoritative check.
	if _, ok, err := r.ResolveService(context.Background(), saToken("other", "lenny-gateway")); !ok || err != nil {
		t.Errorf("multi-audience token = (%t, %v), want admitted", ok, err)
	}
}
