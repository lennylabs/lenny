// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// TestSingleShotErrorFrom verifies the sessionserver.ServiceError → adapter
// SingleShotError mapping copies status, code, message, retryability, and the
// Retry-After seconds verbatim, so the OpenAI-dialect adapter re-emits the
// shared service layer's error envelope unchanged. The zero-Retry-After case
// (the §4.9 CREDENTIAL_POOL_EXHAUSTED pre-claim miss sets no header) is pinned
// alongside the pod-claim exhaustion case that carries a backoff hint.
// spec: §15.2.1 rule 1; §7.1 create-and-start atomicity; §4.9.
func TestSingleShotErrorFrom(t *testing.T) {
	exhausted := singleShotErrorFrom(&sessionserver.ServiceError{
		HTTPStatus:        http.StatusServiceUnavailable,
		Code:              "SESSION_CREATION_FAILED",
		Message:           "warm pool exhausted",
		Retryable:         true,
		RetryAfterSeconds: 7,
	})
	if exhausted.HTTPStatus != http.StatusServiceUnavailable || exhausted.Code != "SESSION_CREATION_FAILED" ||
		exhausted.Message != "warm pool exhausted" || !exhausted.Retryable || exhausted.RetryAfterSeconds != 7 {
		t.Fatalf("exhaustion mapping = %+v, want status/code/message/retryable/retry-after preserved", exhausted)
	}

	credMiss := singleShotErrorFrom(&sessionserver.ServiceError{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       "CREDENTIAL_POOL_EXHAUSTED",
		Message:    "no credential available",
		Retryable:  true,
	})
	if credMiss.RetryAfterSeconds != 0 {
		t.Errorf("CREDENTIAL_POOL_EXHAUSTED RetryAfterSeconds = %d, want 0 (no header)", credMiss.RetryAfterSeconds)
	}
}

// TestSessionSingleShotBinderMapsServiceError verifies BindSingleShot projects
// a shared-service rejection into a typed *translator.SingleShotError rather
// than an opaque error, so the adapter fails closed with the service layer's
// code. An empty RuntimeRef trips create-and-start validation before any pod
// claim, exercising the error-mapping branch (the happy path needs a live pod
// and is covered at tier 5). spec: §15.2.1 rule 1; §15 single-shot compute model.
func TestSessionSingleShotBinderMapsServiceError(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true})
	binder := sessionSingleShotBinder{srv: srv, runtimes: runtimestore.NewMemory(), capOverrides: runtimecapoverride.NewMemory()}

	binding, err := binder.BindSingleShot(context.Background(), translator.SingleShotSpec{TenantID: "acme"})
	if err == nil {
		t.Fatal("BindSingleShot with an empty RuntimeRef returned no error; want a validation rejection")
	}
	if binding.SessionID != "" || binding.Hold {
		t.Errorf("binding = %+v, want empty and not held on rejection", binding)
	}
	var ssErr *translator.SingleShotError
	if !errors.As(err, &ssErr) {
		t.Fatalf("error type = %T, want *translator.SingleShotError", err)
	}
	if ssErr.Code == "" || ssErr.HTTPStatus < 400 {
		t.Errorf("mapped error = %+v, want a client error code and status", ssErr)
	}
}

// failingRuntimes is a runtime registry whose reads fail with a transient
// error, so the hold decision sees a resolve error rather than a not-found.
type failingRuntimes struct{ runtimestore.Store }

func (failingRuntimes) Get(context.Context, string) (runtimestore.Runtime, error) {
	return runtimestore.Runtime{}, errors.New("runtime registry unavailable")
}

// holdRuntimes builds a registry holding a one_shot runtime, a multi_turn
// runtime without injection, and a multi_turn runtime with injection, and an
// override store that narrows injection off the last one for tenant globex.
func holdRuntimes(t *testing.T) (*runtimestore.Memory, *runtimecapoverride.Memory) {
	t.Helper()
	ctx := context.Background()
	rts := runtimestore.NewMemory()
	for _, rt := range []runtimestore.Runtime{
		{Name: "echo", Capabilities: &runtimestore.RuntimeCapabilities{Interaction: runtimestore.InteractionOneShot}},
		{Name: "chat-noinject", Capabilities: &runtimestore.RuntimeCapabilities{Interaction: runtimestore.InteractionMultiTurn}},
		{Name: "chat", Capabilities: &runtimestore.RuntimeCapabilities{
			Interaction: runtimestore.InteractionMultiTurn,
			Injection:   runtimestore.InjectionCapability{Supported: true},
		}},
	} {
		if err := rts.Create(ctx, rt); err != nil {
			t.Fatalf("seed runtime %s: %v", rt.Name, err)
		}
	}
	overrides := runtimecapoverride.NewMemory()
	off := false
	if err := overrides.Put(ctx, "globex", "chat", runtimestore.CapabilityOverride{InjectionSupported: &off}); err != nil {
		t.Fatalf("seed override: %v", err)
	}
	return rts, overrides
}

// TestSingleShotBinderHoldsOnlyAPlatformMultiTurnInjectionSession_spec_15
// pins the hold decision: a session is held only when its continuity is
// "platform" and its runtime, with the tenant's capability override applied,
// is multi_turn with injection supported. Service-mode continuity, a
// one_shot runtime, a multi_turn runtime whose tenant override turns
// injection off, a runtime without injection, a registry read error, and an
// unknown runtime each release.
//
// spec: §15 (Open Responses conversation sessions), §5.1 (Runtime), §7.1
func TestSingleShotBinderHoldsOnlyAPlatformMultiTurnInjectionSession_spec_15(t *testing.T) {
	rts, overrides := holdRuntimes(t)
	cases := []struct {
		name       string
		runtimes   runtimestore.Store
		tenant     string
		continuity string
		runtime    string
		want       bool
	}{
		{"service mode", rts, "acme", "none", "chat", false},
		{"one_shot runtime", rts, "acme", "platform", "echo", false},
		{"tenant override turns injection off", rts, "globex", "platform", "chat", false},
		{"multi_turn without injection", rts, "acme", "platform", "chat-noinject", false},
		{"resolve error", failingRuntimes{Store: rts}, "acme", "platform", "chat", false},
		{"runtime not found", rts, "acme", "platform", "missing", false},
		{"unwired registry", nil, "acme", "platform", "chat", false},
		{"platform multi_turn with injection", rts, "acme", "platform", "chat", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := sessionSingleShotBinder{runtimes: tc.runtimes, capOverrides: overrides}
			if got := b.holds(context.Background(), tc.tenant, tc.continuity, tc.runtime); got != tc.want {
				t.Errorf("holds = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSessionHolderForwarderAdaptsTheSessionServer_spec_29_3 pins the
// forwarder adapter the gateway wires into the Open Responses handler:
// Forwarded is true exactly on a context the forward listener marked, and a
// turn on a server that coordinates every session is local with nothing
// written and no refusal.
//
// spec: §29.3 (Interactive message send) Forwarding, §15 (Open Responses conversation sessions)
func TestSessionHolderForwarderAdaptsTheSessionServer_spec_29_3(t *testing.T) {
	f := sessionHolderForwarder{srv: sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true})}
	if f.Forwarded(context.Background()) {
		t.Error("Forwarded on an unmarked context = true, want false")
	}
	if !f.Forwarded(sessionserver.WithForwarded(context.Background())) {
		t.Error("Forwarded on a marked context = false, want true")
	}
	row := sessionstore.Session{ID: "sess-1", TenantID: "acme", State: session.StateRunning}
	for _, ctx := range []context.Context{context.Background(), sessionserver.WithForwarded(context.Background())} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
		refused := 0
		local := f.ForwardResponsesTurn(rec, req, row, []byte("{}"), func(http.ResponseWriter, string) { refused++ })
		if !local || refused != 0 || rec.Body.Len() != 0 {
			t.Errorf("forwarded=%v: local %v, refusals %d, body %q; want local with nothing written",
				sessionserver.Forwarded(ctx), local, refused, rec.Body.String())
		}
	}
}
