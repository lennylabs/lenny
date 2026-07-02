// SPDX-License-Identifier: MIT

//go:build load_local

// Package budget_gate_deny_flag_race exercises the §11.2/§8.6 write path of
// the gateway LLM Proxy handler under concurrency: many in-flight proxied
// requests read the shared deny-next-request flag through the handler's
// pre-flight BudgetGate.Allow gate while the out-of-band §8.6 extension
// episode fan-out mutates that same flag (RaiseBudget clears it on a grant,
// TerminateSession sets it on a terminal outcome), the reconciliation
// proposal 0023 S4 lands.
//
// The proxy Handler drives its write-path branch on the tri-state extension
// Outcome (proposal 0023 S4): the pre-flight gate rejects an over-budget
// session with BUDGET_EXHAUSTED before any upstream call, and a Pending or
// terminal exhaustion leaves the session denying per request until the
// out-of-band episode raises its budget (admitting its next request) or
// terminates it. The deny flag the gate reads is the sessionbudget.Enforcer
// state the episode fan-out reclaims concurrently, so a torn read of that
// flag, or a request admitted after its session was terminated, would be a
// §11.2 fail-open.
//
// The invariants this scenario asserts:
//   - No torn read of the deny flag: every ServeHTTP call returns a
//     well-formed 200 or a 403 BUDGET_EXHAUSTED envelope, never a panic, a
//     partial write, or a data race (run under -race with the load_local
//     tag). The race detector is the primary assertion; the loop also counts
//     every response and fails on any unexpected status.
//   - No request admitted after its session terminated: a deterministic
//     drain phase terminates every session through the reclaimer, then drives
//     one more request per session through the handler and asserts each is
//     rejected 403 BUDGET_EXHAUSTED before any upstream call. A gate that read
//     a stale (not-denied) flag would forward an unbudgeted request upstream.
//   - The upstream is never called for a denied session: the fake Anthropic
//     server counts its calls, and the terminated-session drain must add zero
//     upstream calls.
//
// Runnable under `lenny-test stress --test budget_gate_deny_flag_race
// --runs N`.
//
// TESTING.md §12.7.a regression scenarios; proposal 0023 S4.
package budget_gate_deny_flag_race

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "budget_gate_deny_flag_race"

// sessionCount spreads the load over many sessions so the shared enforcer
// map is contended: in-path ServeHTTP goroutines read each session's deny
// flag through the gate while fan-out goroutines mutate it.
const sessionCount = 64

// messagesBody is a minimal Anthropic Messages request the proxy forwards.
const messagesBody = `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}]}`

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// Scenario drives concurrent proxy requests through the handler's pre-flight
// budget gate while the episode fan-out mutates the shared deny flag.
type Scenario struct {
	counters *scenkit.Counters

	enforcer *sessionbudget.Enforcer
	handler  *llmproxy.Handler
	leases   *credleasestore.Store
	upstream *httptest.Server
	// upstreamN counts fake-Anthropic calls so the drain phase can assert the
	// gate never forwards a denied session upstream.
	upstreamN atomic.Int32
}

func (s *Scenario) Name() string { return name }

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 64, Duration: 2 * time.Second}
}

func sessionID(j int) string { return fmt.Sprintf("s-%d", j%sessionCount) }

func leaseToken(j int) string { return fmt.Sprintf("lt-%d", j%sessionCount) }

func (s *Scenario) Setup(_ context.Context) error {
	s.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.upstreamN.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))

	// The enforcer is the real §11.2 mid-session gate and the
	// leasecontrol.SessionReclaimer the episode fan-out calls. Its Allow
	// method satisfies llmproxy.BudgetGate directly.
	s.enforcer = sessionbudget.New(nopTerminator{}, nil, nil)

	// Seed each session's enforcer counter with a large budget and zero usage
	// so the session exists (RaiseBudget/TerminateSession key on an existing
	// counter) and starts admitted. The counter is what the deny flag lives
	// on; without it TerminateSession/RaiseBudget would be no-ops and Allow
	// would default-admit, so the drain invariant would not test the flag.
	for j := 0; j < sessionCount; j++ {
		s.enforcer.Record(context.Background(), context.Background(), "acme", sessionID(j), 1_000_000, 0)
	}

	s.leases = credleasestore.New()
	for j := 0; j < sessionCount; j++ {
		if err := s.leases.Put(credential.Lease{
			LeaseID:      fmt.Sprintf("cl-%d", j),
			SessionID:    sessionID(j),
			Provider:     credential.ProviderAnthropicDirect,
			Source:       credential.SourcePool,
			PoolID:       "claude-prod",
			CredentialID: "key-1",
			DeliveryMode: credential.DeliveryProxy,
			IssuedAt:     time.Now(),
			ExpiresAt:    time.Now().Add(time.Hour),
			Proxy: &credential.ProxyConfig{
				ProxyURL:     "https://gateway-internal:8443/llm-proxy",
				ProxyDialect: "anthropic",
				LeaseToken:   leaseToken(j),
			},
		}); err != nil {
			return fmt.Errorf("seed lease %d: %w", j, err)
		}
	}

	s.handler = &llmproxy.Handler{
		Leases:      s.leases,
		Translator:  &llmproxy.AnthropicDirectTranslator{BaseURL: s.upstream.URL, DefaultAnthropicVersion: "2023-06-01"},
		Forwarder:   &llmproxy.Forwarder{Breaker: &llmproxy.CircuitBreaker{}},
		Credentials: fixedKey{},
		BudgetGate:  s.enforcer,
	}
	return nil
}

func (s *Scenario) Teardown(_ context.Context) error {
	if s.upstream != nil {
		s.upstream.Close()
	}
	return nil
}

// Run drives one iteration: in-path virtual users issue a proxied request
// through the handler (reading the deny flag via the pre-flight gate), while
// fan-out virtual users mutate the shared deny flag as the episode's
// per-session reclaim does — RaiseBudget clears it on a grant,
// TerminateSession sets it on a terminal outcome.
func (s *Scenario) Run(_ context.Context, vu, iter int) error {
	j := vu*7 + iter
	switch vu % 3 {
	case 0:
		// In-path proxy request: the pre-flight gate reads the session's deny
		// flag. A 200 or a 403 BUDGET_EXHAUSTED is well-formed; anything else
		// is a torn read or a lease/translation fault under contention.
		rr := s.serve(j)
		switch rr.Code {
		case http.StatusOK:
			s.counters.Inc("admitted")
		case http.StatusForbidden:
			if code := errorCode(rr); code != "BUDGET_EXHAUSTED" {
				s.counters.Inc("errors")
				return fmt.Errorf("unexpected 403 code %q", code)
			}
			s.counters.Inc("denied")
		default:
			s.counters.Inc("errors")
			return fmt.Errorf("unexpected status %d: %s", rr.Code, rr.Body.String())
		}
	case 1:
		// Out-of-band fan-out grant: raise the session's budget and clear its
		// deny flag, as the episode fan-out does through the SessionReclaimer.
		s.enforcer.RaiseBudget(sessionID(j), 1000)
		s.counters.Inc("raised")
	default:
		// Out-of-band fan-out terminal: set the deny flag (fail closed) as the
		// episode fan-out does on a terminal per-session outcome.
		s.enforcer.TerminateSession(sessionID(j))
		s.counters.Inc("terminated")
	}
	return nil
}

// serve issues one proxied request for the j-th session through the handler.
func (s *Scenario) serve(j int) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/llm-proxy/v1/messages", strings.NewReader(messagesBody))
	req.Header.Set("x-api-key", leaseToken(j))
	rr := httptest.NewRecorder()
	s.handler.ServeHTTP(rr, req)
	return rr
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)

	if e := s.counters.Get("errors"); e > 0 {
		return fmt.Errorf("handler observed %d malformed responses under load", e)
	}
	if r.Iterations < 100 {
		return fmt.Errorf("scenario did not get enough load: %d iterations (want >= 100)", r.Iterations)
	}

	// No request admitted after its session terminated. Deterministically
	// terminate every session through the reclaimer, then drive one more
	// request per session through the handler and assert each is rejected
	// 403 BUDGET_EXHAUSTED before any upstream call. A gate that read a stale
	// not-denied flag would forward an unbudgeted request upstream (a §11.2
	// fail-open).
	for j := 0; j < sessionCount; j++ {
		s.enforcer.TerminateSession(sessionID(j))
	}
	before := s.upstreamN.Load()
	for j := 0; j < sessionCount; j++ {
		rr := s.serve(j)
		if rr.Code != http.StatusForbidden || errorCode(rr) != "BUDGET_EXHAUSTED" {
			return fmt.Errorf("terminated session %s admitted: status=%d body=%s",
				sessionID(j), rr.Code, rr.Body.String())
		}
	}
	if added := s.upstreamN.Load() - before; added != 0 {
		return fmt.Errorf("gate forwarded %d requests upstream for terminated sessions (want 0)", added)
	}
	return nil
}

// errorCode decodes the proxy error envelope's code, or "" when the body is
// not a proxy error envelope.
func errorCode(rr *httptest.ResponseRecorder) string {
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		return ""
	}
	return env.Error.Code
}

// fixedKey is a CredentialResolver returning a fixed upstream key.
type fixedKey struct{}

func (fixedKey) UpstreamCredential(credential.Lease) (string, bool) {
	return "sk-ant-real-upstream-key", true
}

// nopTerminator is a Terminator that does nothing: the scenario asserts the
// deny flag directly through the gate, not the terminal pipeline.
type nopTerminator struct{}

func (nopTerminator) TerminateSession(_ /*sessionID*/, _ /*reason*/ string) {}
