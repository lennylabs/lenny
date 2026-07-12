//go:build component

// SPDX-License-Identifier: MIT

// Component-tier suite for the §4.1 / §4.9 LLM Proxy gateway subsystem.
// TESTING.md §12.2.3 declares that each gateway subsystem has a
// component-tier suite that wires it to real stores and mocked peers.
// This suite wires the llmproxy.Handler to a real Postgres-backed
// credential-lease store (the §4.9 durable lease record) and the mock
// LLM provider recorder (the mocked upstream peer), then drives the
// §12.2.3 LLM Proxy behaviors on the wire: lease-token validation, the
// anthropic_direct native translator, request/response and SSE
// translation, deny-list enforcement, and circuit-breaker behavior. The
// lease is resolved through the real store on every request, so the
// streaming relay in particular exercises the SSE-relay-on-real-store
// path that the in-memory proxylease fixture and the tier-4 round-trip
// test do not reach.
package gateway_subsystems_test

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	credleasepg "github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
)

// realUpstreamKey is the credential the gateway injects on the
// upstream leg. It must never reach the pod or the upstream request as
// the lease token, and it is the value the mock provider records.
const realUpstreamKey = "sk-ant-upstream-real-secret"

// messagesRequest is a minimal Anthropic Messages request body. The
// anthropic_direct translator requires the model and messages fields.
// echo is the last user message the mock provider echoes back.
func messagesRequest(echo string, stream bool) string {
	return fmt.Sprintf(
		`{"model":"claude-3-5-sonnet","stream":%t,"messages":[{"role":"user","content":%q}]}`,
		stream, echo)
}

// fakeResolver is the §4.9 CredentialResolver read side: the in-memory
// upstream-credential cache the proxy consults after resolving a lease.
// It returns the real upstream key the pod never holds.
type fakeResolver struct{ key string }

func (f fakeResolver) UpstreamCredential(credential.Lease) (string, bool) {
	return f.key, true
}

// fakeDenyList reports a fixed revocation verdict for the §4.9
// source-aware credential deny list.
type fakeDenyList struct{ revoked bool }

func (f fakeDenyList) Revoked(credential.CredentialKey) bool { return f.revoked }

// realStore brings up a Postgres container with the production
// migrations and returns a Postgres-backed credential-lease store over a
// local KMS KEK provider, the §12.9 envelope-encryption posture the
// store requires. This is the "real store" §12.2.3 wires the subsystem
// to; it is started once per test and shared across the subtests.
func realStore(t *testing.T) *credleasepg.Store {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("kms provider: %v", err)
	}
	store, err := credleasepg.New(pg.Pool, provider)
	if err != nil {
		t.Fatalf("credleasepg.New: %v", err)
	}
	return store
}

// storeProxyLease persists a valid pool-backed proxy lease carrying the
// given bearer token through the real store and returns it. A
// pool-backed lease carries no tenant_id; credential_leases is
// platform-global.
func storeProxyLease(t *testing.T, store *credleasepg.Store, token string) credential.Lease {
	t.Helper()
	lease := credential.Lease{
		LeaseID:      "cl-" + newUUID(t),
		SessionID:    "s-" + newUUID(t),
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-1",
		DeliveryMode: credential.DeliveryProxy,
		IssuedAt:     time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond),
		ExpiresAt:    time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
		Proxy: &credential.ProxyConfig{
			ProxyURL:     "https://gateway-internal:8443/llm-proxy",
			ProxyDialect: "anthropic",
			LeaseToken:   token,
		},
	}
	if err := store.Put(lease); err != nil {
		t.Fatalf("store proxy lease: %v", err)
	}
	return lease
}

// newHandler builds an llmproxy.Handler wired to the real lease store, a
// mock provider recorder as the upstream peer, and a fake in-memory
// credential resolver. denied and breaker let a subtest install deny-list
// enforcement or a pre-configured circuit breaker.
func newHandler(t *testing.T, store *credleasepg.Store, denied bool, breaker *llmproxy.CircuitBreaker) (*llmproxy.Handler, *llmprovider.Stub) {
	t.Helper()
	upstream := llmprovider.New(t)
	if breaker == nil {
		breaker = &llmproxy.CircuitBreaker{}
	}
	h := &llmproxy.Handler{
		Leases:      store,
		Translator:  &llmproxy.AnthropicDirectTranslator{BaseURL: upstream.URL(), DefaultAnthropicVersion: "2023-06-01"},
		Forwarder:   &llmproxy.Forwarder{Breaker: breaker},
		Credentials: fakeResolver{key: realUpstreamKey},
		DenyList:    fakeDenyList{revoked: denied},
	}
	return h, upstream
}

// postToken issues a proxy request carrying the lease token in x-api-key.
func postToken(h *llmproxy.Handler, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	if token != "" {
		req.Header.Set("x-api-key", token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// newUUID returns a fresh random UUIDv4 string for lease and session
// identifiers. The credential_leases lease_id is a free-form text id;
// the session id column is a UUID.
func newUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// spec: §12.2.3 (LLM Proxy component-tier suite: lease-token validation,
// native anthropic_direct translator, request/response/SSE translation,
// deny-list enforcement, circuit-breaker behavior, validated against the
// mock LLM provider), §4.9 (LLM reverse proxy delivery path resolving the
// lease through the durable store), §4.1 (LLM Proxy subsystem).
//
// diagnosis: the LLM Proxy subsystem, wired to the real Postgres-backed
// credential-lease store and the mock upstream provider, diverged from
// §4.9. A failure means one of: a lease persisted in Postgres did not
// resolve on the proxy hot path; the real upstream key leaked to the pod
// or was sent as the lease token; a streaming request's SSE relay did not
// reach the pod after resolving the lease from the real store; an invalid,
// expired, or revoked lease was not rejected before the upstream call; or
// an open circuit breaker did not fail requests closed with
// PROVIDER_UNAVAILABLE.
func TestLLMProxySubsystemOnRealStore(t *testing.T) {
	store := realStore(t)

	t.Run("non-streaming round-trip resolves the lease from the real store", func(t *testing.T) {
		lease := storeProxyLease(t, store, "lt-"+newUUID(t))
		h, upstream := newHandler(t, store, false, nil)

		rr := postToken(h, lease.Proxy.LeaseToken, messagesRequest("hello-blob", false))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d body %q, want 200", rr.Code, rr.Body.String())
		}
		// The translated upstream response echoes the user message back
		// to the pod in the anthropic dialect.
		if !strings.Contains(rr.Body.String(), "hello-blob") {
			t.Errorf("proxy response did not carry the translated upstream body: %s", rr.Body.String())
		}
		// The gateway injected the real upstream credential; the opaque
		// lease token never reached upstream.
		up, ok := upstream.LastRequest()
		if !ok {
			t.Fatal("the mock provider received no request; the proxy did not forward")
		}
		if got := up.Header.Get("x-api-key"); got != realUpstreamKey {
			t.Errorf("upstream x-api-key = %q, want the injected real key %q", got, realUpstreamKey)
		}
		if strings.Contains(string(up.Body), lease.Proxy.LeaseToken) {
			t.Error("the pod's lease token leaked to the upstream provider")
		}
		if strings.Contains(rr.Body.String(), realUpstreamKey) {
			t.Error("the real upstream key leaked to the pod in the proxy response")
		}
	})

	t.Run("streaming request relays the SSE stream after a real-store lease lookup", func(t *testing.T) {
		lease := storeProxyLease(t, store, "lt-"+newUUID(t))
		h, upstream := newHandler(t, store, false, nil)

		rr := postToken(h, lease.Proxy.LeaseToken, messagesRequest("hello-stream", true))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d body %q, want 200", rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
			t.Errorf("Content-Type = %q, want text/event-stream", ct)
		}
		events, err := llmprovider.ReadSSE(rr.Body)
		if err != nil {
			t.Fatalf("parse relayed SSE: %v", err)
		}
		var sawStart, sawDelta bool
		for _, e := range events {
			switch e.Event {
			case "message_start":
				sawStart = true
			case "content_block_delta":
				if strings.Contains(e.Data, "hello-stream") {
					sawDelta = true
				}
			}
		}
		if !sawStart || !sawDelta {
			t.Errorf("relayed SSE missing message_start (%v) or the echoed delta (%v): %+v", sawStart, sawDelta, events)
		}
		// The upstream leg still carried the injected real key on the
		// streaming path.
		up, ok := upstream.LastRequest()
		if !ok || up.Header.Get("x-api-key") != realUpstreamKey {
			t.Fatalf("streaming upstream request missing the injected real key: %+v ok=%v", up, ok)
		}
	})

	t.Run("lease-token validation rejects before any upstream call", func(t *testing.T) {
		valid := storeProxyLease(t, store, "lt-"+newUUID(t))

		cases := []struct {
			name     string
			token    string
			denied   bool
			expired  bool
			wantCode int
			wantErr  string
		}{
			{"missing token", "", false, false, http.StatusUnauthorized, "LEASE_TOKEN_MISSING"},
			{"unknown token", "lt-absent-" + newUUID(t), false, false, http.StatusUnauthorized, "LEASE_TOKEN_INVALID"},
			{"expired lease", valid.Proxy.LeaseToken, false, true, http.StatusForbidden, "LEASE_EXPIRED"},
			{"revoked credential", valid.Proxy.LeaseToken, true, false, http.StatusForbidden, "CREDENTIAL_REVOKED"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				h, upstream := newHandler(t, store, tc.denied, nil)
				if tc.expired {
					// Store keeps a future-expiry lease (Put validates it);
					// advance the handler clock past expiry at request time.
					h.Now = func() time.Time { return valid.ExpiresAt.Add(time.Minute) }
				}
				rr := postToken(h, tc.token, messagesRequest("nope", false))
				if rr.Code != tc.wantCode {
					t.Errorf("status = %d, want %d (body %q)", rr.Code, tc.wantCode, rr.Body.String())
				}
				if !strings.Contains(rr.Body.String(), tc.wantErr) {
					t.Errorf("error body %q, want code %q", rr.Body.String(), tc.wantErr)
				}
				if _, ok := upstream.LastRequest(); ok {
					t.Error("a rejected request reached the upstream provider")
				}
			})
		}
	})

	t.Run("circuit breaker fails requests closed once open", func(t *testing.T) {
		lease := storeProxyLease(t, store, "lt-"+newUUID(t))
		// One upstream 5xx trips the breaker from closed to open.
		breaker := &llmproxy.CircuitBreaker{FailureThreshold: 1, Cooldown: time.Hour}
		h, upstream := newHandler(t, store, false, breaker)
		upstream.SetResponseOverride(func(llmprovider.Request) (int, string, map[string]string) {
			return http.StatusInternalServerError, `{"error":"upstream down"}`, nil
		})

		// First request reaches upstream, sees the 5xx, and trips the breaker.
		first := postToken(h, lease.Proxy.LeaseToken, messagesRequest("one", false))
		if first.Code == http.StatusOK {
			t.Fatalf("first request unexpectedly succeeded against a 5xx upstream: %s", first.Body.String())
		}
		if breaker.State() != llmproxy.CircuitOpen {
			t.Fatalf("breaker state = %v after a 5xx, want open", breaker.State())
		}
		before := len(upstream.Requests())

		// Second request is rejected by the open breaker before any
		// upstream dial and maps to 503 PROVIDER_UNAVAILABLE.
		second := postToken(h, lease.Proxy.LeaseToken, messagesRequest("two", false))
		if second.Code != http.StatusServiceUnavailable {
			t.Errorf("open-breaker status = %d, want 503 (body %q)", second.Code, second.Body.String())
		}
		if !strings.Contains(second.Body.String(), "PROVIDER_UNAVAILABLE") {
			t.Errorf("open-breaker error body %q, want PROVIDER_UNAVAILABLE", second.Body.String())
		}
		if after := len(upstream.Requests()); after != before {
			t.Errorf("open breaker dialed upstream: request count %d -> %d", before, after)
		}
	})
}
