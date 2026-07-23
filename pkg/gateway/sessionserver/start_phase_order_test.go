// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// phaseRecorder is an external §4.8 interceptor that appends its name to a
// shared slice on each invocation and returns the configured action. It lets a
// test observe the order in which the create-and-start path invokes the
// PostAuth and PreRoute chains. It is external (priority above the reserved
// ceiling) so it is legal at any non-PreAuth phase.
type phaseRecorder struct {
	name   string
	action interceptor.Action
	calls  *[]string
}

func (p phaseRecorder) Name() string                     { return p.name }
func (phaseRecorder) Priority() int32                    { return 150 }
func (phaseRecorder) Builtin() bool                      { return false }
func (phaseRecorder) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (phaseRecorder) Timeout() time.Duration             { return 0 }
func (p phaseRecorder) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	*p.calls = append(*p.calls, p.name)
	return interceptor.Result{Action: p.action}, nil
}

// phaseOrderServer builds a create-and-start server whose §4.8 interceptor
// chain carries the given PostAuth and PreRoute interceptors, sharing one
// invocation-order slice.
func phaseOrderServer(t *testing.T, postAuth, preRoute interceptor.Interceptor) *sessionserver.Server {
	t.Helper()
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePostAuth, postAuth); err != nil {
		t.Fatalf("register PostAuth: %v", err)
	}
	if err := chain.Register(interceptor.PhasePreRoute, preRoute); err != nil {
		t.Fatalf("register PreRoute: %v", err)
	}
	return sessionserver.New(memstore.New(), sessionserver.Options{
		Clock:        func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) },
		Interceptors: chain,
	})
}

// TestSessionStartPostAuthChainRunsBeforePreRoute_spec_4_8 pins the §4.8
// canonical phase order (PreAuth -> PostAuth -> PreRoute -> PostRoute) on the
// combined create-and-start path: the §4.8 PostAuth policy chain (which
// requirePolicyChain runs) fires before the §4.8 PreRoute interceptor chain
// (which buildCreateAndStartRow runs). A regression that runs the row build,
// and thus the PreRoute chain, before the admission gates inverts the phases
// and records PreRoute first.
// spec: §4.8 (PostAuth before PreRoute); §15.2.1 rule 1.
func TestSessionStartPostAuthChainRunsBeforePreRoute_spec_4_8(t *testing.T) {
	var calls []string
	srv := phaseOrderServer(t,
		phaseRecorder{name: "postauth", action: interceptor.ActionAllow, calls: &calls},
		phaseRecorder{name: "preroute", action: interceptor.ActionAllow, calls: &calls})
	rr := createAndStartRequest(t, srv.Handler(),
		sessionserver.CreateAndStartRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create-and-start with two ALLOW interceptors: status %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if len(calls) != 2 || calls[0] != "postauth" || calls[1] != "preroute" {
		t.Fatalf("phase invocation order = %v, want [postauth preroute]; the §4.8 PostAuth chain must run before the PreRoute chain", calls)
	}
}

// TestSessionStartPostAuthRejectShortCircuitsBeforePreRoute_spec_4_8 pins that
// a §4.8 PostAuth REJECT on the create-and-start path short-circuits before the
// §4.8 PreRoute interceptor chain and the §10.7 experiment router run: the
// PreRoute recorder is never invoked. A regression that runs buildCreateAndStartRow
// (PreRoute + experiment routing) before the PostAuth policy chain records a
// PreRoute invocation even though the request is ultimately rejected.
// spec: §4.8 (PostAuth before PreRoute); §11.2 (policy REJECT -> 429); §15.2.1 rule 1.
func TestSessionStartPostAuthRejectShortCircuitsBeforePreRoute_spec_4_8(t *testing.T) {
	var calls []string
	srv := phaseOrderServer(t,
		phaseRecorder{name: "postauth", action: interceptor.ActionReject, calls: &calls},
		phaseRecorder{name: "preroute", action: interceptor.ActionAllow, calls: &calls})
	rr := createAndStartRequest(t, srv.Handler(),
		sessionserver.CreateAndStartRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("PostAuth REJECT: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
	if len(calls) != 1 || calls[0] != "postauth" {
		t.Fatalf("invocation order = %v, want [postauth] only; the PreRoute chain must not run once the PostAuth chain REJECTs", calls)
	}
}
