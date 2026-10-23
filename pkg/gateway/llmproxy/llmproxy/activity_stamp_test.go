// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
)

// recordingStamper records each Stamp call.
type recordingStamper struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingStamper) Stamp(tenantID, sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, tenantID+"/"+sessionID)
}

// TestProxyStreamStampsSessionActivity_spec_6_2 pins the proxy-mode
// per-chunk stamp: each relayed stream line of a proxy-mode lease that names
// a session stamps the session's activity, and a handler with no stamper
// relays the stream unchanged. The direct-mode and sessionless guards are
// pinned in TestStampingFlushGuards_spec_6_2, because the handler refuses
// both leases before it streams.
//
// spec: 6.2 (maxClientIdleSeconds clock behavior across states)
func TestProxyStreamStampsSessionActivity_spec_6_2(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(anthropicSSEStream))
	}))
	defer upstream.Close()
	lines := strings.Count(anthropicSSEStream, "\n")

	for _, tc := range []struct {
		name      string
		mutate    func(*credential.Lease)
		stamper   bool
		wantStamp bool
	}{
		{name: "proxy_with_session", stamper: true, wantStamp: true},
		{name: "no_stamper"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lease := handlerLease("lt-act")
			lease.TenantID = "acme"
			if tc.mutate != nil {
				tc.mutate(&lease)
			}
			leases := credleasestore.New()
			if err := leases.Put(lease); err != nil {
				t.Fatalf("seed lease: %v", err)
			}
			stamper := &recordingStamper{}
			h := &llmproxy.Handler{
				Leases:      leases,
				Translator:  &llmproxy.AnthropicDirectTranslator{BaseURL: upstream.URL, DefaultAnthropicVersion: "2023-06-01"},
				Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
				Credentials: fakeResolver{key: "sk-ant-real", ok: true},
			}
			if tc.stamper {
				h.Activity = stamper
			}
			rr := post(h, "lt-act", `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}],"stream":true}`)
			if rr.Code != http.StatusOK || rr.Body.String() != anthropicSSEStream {
				t.Fatalf("status %d body %q, want the upstream stream relayed", rr.Code, rr.Body.String())
			}
			if !tc.wantStamp {
				if len(stamper.calls) != 0 {
					t.Fatalf("stamped %v, want none", stamper.calls)
				}
				return
			}
			if len(stamper.calls) == 0 || len(stamper.calls) > lines {
				t.Fatalf("stamped %d times, want one per relayed line (at most %d)", len(stamper.calls), lines)
			}
			for _, c := range stamper.calls {
				if c != "acme/s_h1" {
					t.Fatalf("stamp = %q, want acme/s_h1", c)
				}
			}
		})
	}
}
