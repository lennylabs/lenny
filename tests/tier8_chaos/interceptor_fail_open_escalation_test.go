// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos regression for the §4.8 cumulative fail-open escalation.
// A deployer may set a content-scanning interceptor to failPolicy:
// fail-open so a degraded scanner does not block traffic. To stop a
// persistently broken scanner from silently failing open forever, the
// gateway tracks a per-interceptor rolling failure window: once a
// fail-open interceptor errors more than interceptorFailOpenMaxConsecutive
// times within the 5-minute window, the gateway auto-escalates it to
// fail-closed, rejecting subsequent requests with INTERCEPTOR_TIMEOUT, and
// restores it to fail-open once it succeeds again. The transitions emit
// interceptor.fail_open_escalated / interceptor.fail_open_restored.
//
// The interceptor package's own tier-1 tests exercise this against an
// in-process fake with a synthetic error. This test degrades a REAL
// external interceptor: it stands up the RequestInterceptor gRPC stub on a
// loopback TCP socket, dials it with the production gRPC client, and
// registers it as a fail-open External on a real Chain. It then faults the
// stub (the controllable fault mode), drives the chain past the ceiling
// over the real wire, asserts the escalation event fired and subsequent
// calls reject with INTERCEPTOR_TIMEOUT, recovers the stub, and asserts
// the restoration event fired and the chain admits again. The rolling
// window is pinned with an injected clock so every fault lands inside one
// window deterministically.
package tier8_chaos_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	interceptorv1 "github.com/lennylabs/lenny/pkg/proto/interceptor/v1"
	stubinterceptor "github.com/lennylabs/lenny/tests/testinfra/stubs/interceptor"
)

// failOpenObserver records the §4.8 escalation transitions the chain
// emits so the test can assert exactly one escalation and one restoration
// fired, carrying the faulting interceptor's identity.
type failOpenObserver struct {
	escalated []interceptor.FailOpenEvent
	restored  []interceptor.FailOpenEvent
}

func (o *failOpenObserver) FailOpenEscalated(_ context.Context, ev interceptor.FailOpenEvent) {
	o.escalated = append(o.escalated, ev)
}

func (o *failOpenObserver) FailOpenRestored(_ context.Context, ev interceptor.FailOpenEvent) {
	o.restored = append(o.restored, ev)
}

// dialInterceptorStub dials the stub over a real loopback TCP socket with
// the production gRPC RequestInterceptor client and returns it, tearing
// the connection down on cleanup. Using the real client and socket (not
// an in-process fake) is what makes this a chaos test of a degraded
// external dependency rather than a unit test of the escalation counter.
func dialInterceptorStub(t *testing.T, addr string) interceptor.InterceptClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial interceptor stub %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return interceptorv1.NewRequestInterceptorClient(conn)
}

// spec: §4.8 (gateway policy engine, cumulative fail-open escalation) —
// "When a fail-open interceptor has failed (timeout or error) more than
// interceptorFailOpenMaxConsecutive times (default: 10) within a rolling
// 5-minute window, the gateway automatically escalates that interceptor to
// fail-closed for the remainder of the window — subsequent requests that
// would invoke the interceptor are rejected with INTERCEPTOR_TIMEOUT as if
// failPolicy: fail-closed were set. The gateway emits an
// interceptor.fail_open_escalated audit event ... when escalation triggers
// and interceptor.fail_open_restored when the interceptor begins
// succeeding again."
// diagnosis: the gateway did not auto-escalate a persistently failing
// fail-open external interceptor to fail-closed across the real gRPC
// boundary, or did not restore it on recovery. Either is a policy-bypass
// defect — a broken content scanner keeps failing open indefinitely (no
// escalation) or stays escalated after it recovers (no restore). A tier-1
// fake with a synthetic error cannot catch a regression in how transport
// errors from a real degraded interceptor drive the rolling window.
func TestInterceptorFailOpenEscalationOverGRPC_spec_4_8(t *testing.T) {
	// The stub starts healthy (ALLOW), then the test degrades it.
	stub := stubinterceptor.Start(t, stubinterceptor.Allow())
	client := dialInterceptorStub(t, stub.Addr())

	obs := &failOpenObserver{}
	// Pin the rolling window with a fixed clock so every fault lands inside
	// one window; maxConsecutive/window of 0 select the §4.8 production
	// defaults (10 and 5 minutes).
	fixedNow := time.Unix(1_700_000_000, 0).UTC()
	chain := interceptor.NewChain()
	chain.SetFailOpenEscalation(0, 0, obs, func() time.Time { return fixedNow })

	const name = "content-scanner"
	if _, err := chain.RegisterExternal(interceptor.PhasePreDelegation, interceptor.ExternalConfig{
		Name:       name,
		Priority:   interceptor.DefaultExternalPriority,
		FailPolicy: interceptor.FailOpen,
		Timeout:    2 * time.Second,
		Client:     client,
	}); err != nil {
		t.Fatalf("register external interceptor: %v", err)
	}

	run := func() interceptor.Result {
		return chain.Run(context.Background(), interceptor.Request{
			Phase:    interceptor.PhasePreDelegation,
			TenantID: "acme",
			Content:  []byte(`[{"type":"text","text":"delegate this"}]`),
		})
	}

	// Healthy: the scanner ALLOWs, so the chain admits.
	if got := run(); got.Action != interceptor.ActionAllow {
		t.Fatalf("healthy call: action = %v, want ALLOW", got.Action)
	}

	// Degrade the scanner: every gRPC call now errors.
	stub.SetHandler(stubinterceptor.Fail("scanner degraded"))

	// The first interceptorFailOpenMaxConsecutive errors are within the
	// ceiling, so the fail-open policy skips the scanner and the chain still
	// admits — no escalation yet.
	for i := 0; i < interceptor.DefaultFailOpenMaxConsecutive; i++ {
		if got := run(); got.Action != interceptor.ActionAllow {
			t.Fatalf("faulting call %d within ceiling: action = %v, want ALLOW (skipped fail-open)", i, got.Action)
		}
	}
	if len(obs.escalated) != 0 {
		t.Fatalf("escalated before crossing the ceiling: %d events", len(obs.escalated))
	}

	// The next error exceeds the ceiling: the scanner auto-escalates to
	// fail-closed and the chain rejects with INTERCEPTOR_TIMEOUT.
	res := run()
	if res.Action != interceptor.ActionReject || res.Code != interceptor.CodeInterceptorTimeout {
		t.Fatalf("escalated call: %+v, want REJECT / INTERCEPTOR_TIMEOUT", res)
	}
	if res.RejectedBy != name {
		t.Errorf("RejectedBy = %q, want %q", res.RejectedBy, name)
	}
	if len(obs.escalated) != 1 {
		t.Fatalf("escalation events = %d, want exactly 1", len(obs.escalated))
	}
	if ev := obs.escalated[0]; ev.InterceptorName != name || ev.TenantID != "acme" || ev.Phase != interceptor.PhasePreDelegation {
		t.Errorf("escalation event = %+v, want interceptor=%q tenant=acme phase=PreDelegation", ev, name)
	}
	if ev := obs.escalated[0]; ev.WindowSeconds != int(interceptor.DefaultFailOpenWindow/time.Second) {
		t.Errorf("escalation window_seconds = %d, want %d", ev.WindowSeconds, int(interceptor.DefaultFailOpenWindow/time.Second))
	}

	// While escalated and still faulting, subsequent requests keep
	// rejecting with INTERCEPTOR_TIMEOUT and do not re-emit the escalation.
	for i := 0; i < 3; i++ {
		if got := run(); got.Action != interceptor.ActionReject || got.Code != interceptor.CodeInterceptorTimeout {
			t.Fatalf("post-escalation call %d: %+v, want REJECT / INTERCEPTOR_TIMEOUT", i, got)
		}
	}
	if len(obs.escalated) != 1 {
		t.Errorf("escalation re-emitted while already escalated: %d events", len(obs.escalated))
	}
	if len(obs.restored) != 0 {
		t.Fatalf("restored before the scanner recovered: %d events", len(obs.restored))
	}

	// Recover the scanner: the next successful call restores it to fail-open
	// and the chain admits again.
	stub.SetHandler(stubinterceptor.Allow())
	if got := run(); got.Action != interceptor.ActionAllow {
		t.Fatalf("recovered call: action = %v, want ALLOW", got.Action)
	}
	if len(obs.restored) != 1 {
		t.Fatalf("restoration events = %d, want exactly 1", len(obs.restored))
	}
	if ev := obs.restored[0]; ev.InterceptorName != name || ev.TenantID != "acme" {
		t.Errorf("restoration event = %+v, want interceptor=%q tenant=acme", ev, name)
	}

	// After restore the fail-open policy skips errors again until the
	// ceiling is re-crossed: a single fault is admitted, not rejected.
	stub.SetHandler(stubinterceptor.Fail("scanner degraded again"))
	if got := run(); got.Action != interceptor.ActionAllow {
		t.Errorf("first fault after restore: action = %v, want ALLOW (skipped fail-open)", got.Action)
	}
}
