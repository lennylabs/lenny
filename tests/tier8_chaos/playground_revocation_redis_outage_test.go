// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §27.3.1 playground revocation fail-closed
// posture against a real Redis outage. pkg/gateway/middleware/auth's
// playground_revocation_test.go already proves the middleware fails
// closed when the PlaygroundRevocationChecker returns an error, but it
// does so with a fake checker that returns a canned context.DeadlineExceeded
// — it never drives the real pkg/gateway/mcpfabric/playground.RedisSessionStore
// against a real, then-terminated, Redis container, and it never
// exercises the recovery leg (a fresh Redis reachable again resumes
// normal, non-fail-closed operation). This test closes that gap using
// the same "terminate the container, then start a fresh one" injection
// pattern slot_counter_redis_outage_test.go uses for the §12.4 slot
// counter.
package tier8_chaos_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// permissivePlaygroundTenantRegistry admits every tenant id, matching
// the equivalent in-package permissiveRegistry the auth middleware's
// own unit tests use; duplicated here because it is unexported and
// this file builds in the external tier8_chaos_test package.
type permissivePlaygroundTenantRegistry struct{}

func (permissivePlaygroundTenantRegistry) IsRegistered(string) (bool, error) { return true, nil }

// spec: §27.3.1 ("Redis unavailability", spec/27_web-playground.md line
// 99) — "The revocation check fails closed: when Redis is unreachable
// during the per-request check, playground-origin requests are
// rejected with 503 REDIS_UNAVAILABLE rather than permitted." and line
// 98 ("Replicas with a dropped subscription MUST re-subscribe ...").
// diagnosis: a failure here means the §27.3.1 fail-closed guarantee
// does not hold against a real Redis outage: either a live
// playground-origin bearer is wrongly honored while the backing
// pkg/gateway/mcpfabric/playground.RedisSessionStore cannot reach
// Redis (a revocation-bypass window), or the bearer is wrongly denied
// once a fresh Redis becomes reachable again (a false-positive
// fail-closed that never recovers). Inspect the HTTP status/body pairs
// logged for the healthy, outage, and recovery phases to see which
// phase diverged.
func TestPlaygroundRevocationChecksFailsClosedOnRealRedisOutageAndRecovers(t *testing.T) {
	signer := jwt.NewHMACSigner("chaos-test", []byte("chaos-test-signing-secret"))
	tok, err := signer.Sign(jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Expiry:   time.Now().Add(time.Hour).Unix(),
		Typ:      pkgauth.TokenSessionCapability,
		JWTID:    "jti-chaos-redis-outage",
		Origin:   "playground",
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	newHandler := func(checker authmw.PlaygroundRevocationChecker) http.Handler {
		inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
		return authmw.Wrap(inner, authmw.Options{
			Verifier:              signer,
			MultiTenant:           true,
			Registry:              permissivePlaygroundTenantRegistry{},
			PlaygroundRevocations: checker,
		})
	}

	doRequest := func(h http.Handler) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// ---- Healthy: a live bearer is honored against a real, reachable
	// Redis-backed store. ----
	rd := containers.StartRedis(t, containers.RedisOptions{})
	store := playground.NewRedisSessionStore(rd.Client)
	healthy := doRequest(newHandler(store))
	if healthy.Code != http.StatusNoContent {
		t.Fatalf("healthy phase: status = %d, want 204; body = %s", healthy.Code, healthy.Body.String())
	}

	// ---- Inject: terminate the Redis container mid-flight. Every
	// subsequent call the store makes to Redis now fails to connect
	// instead of returning a miss, so the middleware must fail closed
	// rather than silently treating "store errored" as "not revoked". ----
	rd.Stop(t)

	outage := doRequest(newHandler(store))
	if outage.Code != http.StatusServiceUnavailable {
		t.Fatalf("outage phase: status = %d, want 503 (fail-closed); body = %s", outage.Code, outage.Body.String())
	}
	if !strings.Contains(outage.Body.String(), "REDIS_UNAVAILABLE") {
		t.Errorf("outage phase: body must carry REDIS_UNAVAILABLE, got %s", outage.Body.String())
	}

	// ---- Recover: a fresh Redis becomes reachable. §27.3.1's
	// correctness guarantee is anchored to Redis being authoritative and
	// reachable, not to surviving container state, so a fresh instance
	// resuming service is exactly the "Redis recovers" case: the same
	// still-valid, never-revoked bearer is honored again without any
	// gateway restart or process change on the caller's side. ----
	rd2 := containers.StartRedis(t, containers.RedisOptions{})
	store2 := playground.NewRedisSessionStore(rd2.Client)
	recovered := doRequest(newHandler(store2))
	if recovered.Code != http.StatusNoContent {
		t.Fatalf("recovery phase: status = %d, want 204; body = %s", recovered.Code, recovered.Body.String())
	}

	// A bearer actually revoked before the outage is still rejected once
	// Redis recovers, confirming recovery resumes real revocation
	// enforcement rather than merely returning to an always-allow state.
	if err := store2.RevokeSession(context.Background(), "acme", "sess-chaos", []string{"jti-chaos-redis-outage"}, time.Hour); err != nil {
		t.Fatalf("RevokeSession on the recovered store: %v", err)
	}
	postRevoke := doRequest(newHandler(store2))
	if postRevoke.Code != http.StatusUnauthorized {
		t.Fatalf("post-recovery revoked bearer: status = %d, want 401; body = %s", postRevoke.Code, postRevoke.Body.String())
	}
	if !strings.Contains(postRevoke.Body.String(), "bearer_revoked") {
		t.Errorf("post-recovery revoked bearer: body must carry bearer_revoked, got %s", postRevoke.Body.String())
	}
}
