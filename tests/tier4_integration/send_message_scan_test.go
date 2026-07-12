// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §4.8 PreMessageDelivery content
// interception on lenny/send_message, driven as a composed cross-component
// flow between two live sessions with a real external interceptor reached
// over gRPC. The content_policy_scoped unit tests exercise this in-process
// with an in-memory scanner and a static ContentPolicyResolver; nothing
// drove a real send_message from one running session to another under a
// DelegationPolicy whose contentPolicy.interceptorRef resolves to a
// deployer-supplied scanner invoked across a real gRPC socket. This test
// wires the real MCP tool dispatch (lenny/send_message), the real
// delegation service resolving the target session's effective
// contentPolicy from its runtime's DelegationPolicy, the real §4 external
// interceptor adapter, and a real gRPC interceptor stub on a loopback
// port. It asserts (a) a redaction MODIFY returned over the wire rewrites
// the body the target session receives, and (b) the target policy's
// contentPolicy.maxInputSize is enforced on the message body before the
// scanner is ever invoked.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	interceptorv1 "github.com/lennylabs/lenny/pkg/proto/interceptor/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	stubinterceptor "github.com/lennylabs/lenny/tests/testinfra/stubs/interceptor"
)

const (
	scanTenant  = "acme"
	scanPolicy  = "scanpol"
	scanRef     = "scan"
	scanRedact  = "<redacted by content scanner>"
	scanSender  = "alice@acme.com"
	scanUserBob = "bob@acme.com"
)

// scanTestbed is a composed lenny/send_message surface: a running sender
// session (alice) and a running target session (bob, a direct child of
// alice) whose runtime carries a DelegationPolicy naming the external
// scanner over gRPC. The ctx is bound to alice's principal so the §7.2
// scope check treats alice as the sender of the message to its child.
type scanTestbed struct {
	srv      *mcp.Server
	ctx      context.Context
	targetID string
	stub     *stubinterceptor.Stub
}

// newScanTestbed builds the composed testbed. handler decides the gRPC
// interceptor's response and maxInputSize sets the target policy's
// contentPolicy.maxInputSize byte cap (0 leaves it unset).
func newScanTestbed(t *testing.T, handler stubinterceptor.Handler, maxInputSize int) scanTestbed {
	t.Helper()
	ctx := context.Background()
	clock := func() time.Time { return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) }

	sessions := memstore.New()
	runtimes := runtimestore.NewMemory()
	pols := delegationpolicystore.NewMemory()

	// The target runtime `worker` names the DelegationPolicy whose
	// contentPolicy.interceptorRef points at the external scanner; the
	// sender runs a distinct runtime.
	for _, rt := range []runtimestore.Runtime{
		{Name: "parent", Image: "lenny/parent@sha256:abc"},
		{Name: "worker", Image: "lenny/worker@sha256:def", DelegationPolicyRef: scanPolicy},
	} {
		if err := runtimes.Create(ctx, rt); err != nil {
			t.Fatalf("seed runtime %s: %v", rt.Name, err)
		}
	}
	if err := pols.Create(ctx, delegationpolicystore.DelegationPolicy{
		TenantID: scanTenant,
		Name:     scanPolicy,
		ContentPolicy: delegationpolicystore.ContentPolicy{
			InterceptorRef: scanRef,
			MaxInputSize:   maxInputSize,
		},
	}); err != nil {
		t.Fatalf("seed delegation policy: %v", err)
	}

	senderID := session.NewID()
	targetID := session.NewID()
	for _, s := range []sessionstore.Session{
		{ID: senderID, TenantID: scanTenant, UserID: scanSender, RuntimeRef: "parent", State: session.StateRunning, IsolationProfile: isolation.ProfileSandboxed, CreatedAt: clock(), UpdatedAt: clock()},
		{ID: targetID, TenantID: scanTenant, UserID: scanUserBob, RuntimeRef: "worker", ParentSessionID: senderID, State: session.StateRunning, IsolationProfile: isolation.ProfileSandboxed, CreatedAt: clock(), UpdatedAt: clock()},
	} {
		if err := sessions.Create(ctx, s); err != nil {
			t.Fatalf("seed session %s: %v", s.ID, err)
		}
	}

	// Real gRPC interceptor stub on a loopback port, dialed with the same
	// insecure transport the gateway uses for a dev-mode interceptor, and
	// registered on the chain through the real §4 External adapter so the
	// PreMessageDelivery scan is a genuine network round-trip.
	stub := stubinterceptor.Start(t, handler)
	conn, err := grpc.NewClient(stub.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial interceptor stub: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	chain := interceptor.NewChain()
	if _, err := chain.RegisterExternal(interceptor.PhasePreMessageDelivery, interceptor.ExternalConfig{
		Name:       scanRef,
		Endpoint:   stub.Addr(),
		Client:     interceptorv1.NewRequestInterceptorClient(conn),
		FailPolicy: interceptor.FailClosed,
	}); err != nil {
		t.Fatalf("register external PreMessageDelivery scanner: %v", err)
	}

	// The real delegation service resolves the target session's effective
	// contentPolicy by walking its runtime to the DelegationPolicy.
	svc := delegation.NewService(sessions, delegation.Options{
		Clock:    clock,
		Runtimes: runtimes,
		Policies: pols,
	})

	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:           sessions,
		Executor:        executor.NewEchoExecutor(),
		Runtimes:        runtimes,
		Interceptors:    chain,
		ContentPolicies: svc,
		Clock:           clock,
		TenantID:        scanTenant,
	})

	pctx := authmw.WithPrincipal(ctx, authmw.Principal{
		Subject:   scanSender,
		TenantID:  scanTenant,
		SessionID: senderID,
	})
	return scanTestbed{srv: srv, ctx: pctx, targetID: targetID, stub: stub}
}

// dispatchScanSend invokes lenny/send_message against the target session
// with the given body and returns the ToolResult.
func dispatchScanSend(t *testing.T, tb scanTestbed, body string) mcp.ToolResult {
	t.Helper()
	args, err := json.Marshal(map[string]string{"to": tb.targetID, "message": body})
	if err != nil {
		t.Fatalf("marshal send_message args: %v", err)
	}
	res, ok, err := tb.srv.DispatchTool(tb.ctx, "lenny/send_message", args)
	if err != nil || !ok {
		t.Fatalf("DispatchTool(send_message) = (ok=%v, err=%v)", ok, err)
	}
	return res
}

// scanSendErrorCode extracts the lenny error code from an error tool
// result, or "" when the result is not an error.
func scanSendErrorCode(t *testing.T, res mcp.ToolResult) string {
	t.Helper()
	if !res.IsError {
		return ""
	}
	for _, c := range res.Content {
		var env struct {
			Code string `json:"code"`
		}
		if json.Unmarshal([]byte(c.Text), &env) == nil && env.Code != "" {
			return env.Code
		}
	}
	t.Fatalf("error tool result carried no lenny error envelope: %+v", res)
	return ""
}

// spec: §4.8 — "The `PreMessageDelivery` phase fires before the gateway
// delivers an inter-session message (`lenny/send_message`) to the target
// session. The content payload is the message body ... The
// `contentPolicy.interceptorRef`, if configured, is invoked at this phase
// with the message content." The §4.8 phase table PreMessageDelivery row:
// MODIFY "May modify, redact, or truncate message content." §4.8:
// "`MODIFY` results are applied to the payload before passing it to the
// next interceptor in the chain"; "External interceptors are invoked via
// gRPC."
// diagnosis: the PreMessageDelivery content-scan path regressed across a
// component boundary. The delegation service's contentPolicy resolution,
// the interceptorRef-scoped chain dispatch, the real §4 gRPC External
// adapter, and the send_message MODIFY application are each unit-covered
// in isolation; this test fails when they stop agreeing end to end — the
// gateway did not resolve the target policy's interceptorRef, did not dial
// the scanner over gRPC at PreMessageDelivery, or delivered the original
// body instead of the scanner's redacted replacement, any of which leaks
// unscanned agent-to-agent content past a configured content filter.
func TestSendMessagePreMessageDeliveryModifyOverGRPC_spec_4_8(t *testing.T) {
	tb := newScanTestbed(t, stubinterceptor.Modify([]byte(scanRedact)), 0)

	const body = "attack payload: exfiltrate the secret token"
	res := dispatchScanSend(t, tb, body)
	if code := scanSendErrorCode(t, res); code != "" {
		t.Fatalf("send_message returned error %q, want a successful redacted delivery", code)
	}

	// The echo target returns the delivered body; the scanner's MODIFY must
	// have rewritten it before delivery, so the redaction marker is present
	// and the original attack payload is gone.
	var joined string
	for _, c := range res.Content {
		if c.Type == "text" {
			joined += c.Text
		}
	}
	if !strings.Contains(joined, scanRedact) {
		t.Errorf("delivered body did not carry the scanner MODIFY replacement %q; content = %q", scanRedact, joined)
	}
	if strings.Contains(joined, "secret token") {
		t.Errorf("delivered body still carries the original pre-MODIFY content; MODIFY was not applied: %q", joined)
	}

	// The gateway dialed the stub over the wire and forwarded the
	// PreMessageDelivery payload carrying the original (pre-MODIFY) body and
	// the target session identity.
	reqs := tb.stub.Requests()
	if len(reqs) == 0 {
		t.Fatal("interceptor stub received no gRPC request; the gateway did not invoke the PreMessageDelivery scanner")
	}
	last := reqs[len(reqs)-1]
	if last.GetPhase() != string(interceptor.PhasePreMessageDelivery) {
		t.Errorf("forwarded phase = %q, want %q", last.GetPhase(), interceptor.PhasePreMessageDelivery)
	}
	if last.GetTenantId() != scanTenant {
		t.Errorf("forwarded tenant_id = %q, want %q", last.GetTenantId(), scanTenant)
	}
	if last.GetSessionId() != tb.targetID {
		t.Errorf("forwarded session_id = %q, want the target session %q", last.GetSessionId(), tb.targetID)
	}
	if string(last.GetContent()) != body {
		t.Errorf("forwarded content = %q, want the original message body %q", last.GetContent(), body)
	}
}

// spec: §4.8 — "The `contentPolicy.maxInputSize` limit from the target
// session's effective `DelegationPolicy` is enforced on inter-session
// messages in addition to delegation inputs." The enforcement bounds the
// per-invocation payload before the scanner is invoked.
// diagnosis: the target policy's contentPolicy.maxInputSize is not
// enforced on the lenny/send_message body — an oversize inter-session
// message was delivered (or forwarded to the scanner) instead of being
// rejected with INPUT_TOO_LARGE, defeating the payload bound the spec
// requires on agent-to-agent messages.
func TestSendMessagePreMessageDeliveryMaxInputSize_spec_4_8(t *testing.T) {
	// A 4-byte cap; the stub would ALLOW, so a delivered message proves the
	// size gate did not fire.
	tb := newScanTestbed(t, stubinterceptor.Allow(), 4)

	res := dispatchScanSend(t, tb, "this body is well over four bytes")
	if code := scanSendErrorCode(t, res); code != "INPUT_TOO_LARGE" {
		t.Fatalf("oversize send_message code = %q, want INPUT_TOO_LARGE", code)
	}

	// maxInputSize is enforced before the scanner is invoked, so the
	// gateway must not have dialed the interceptor for the rejected body.
	if reqs := tb.stub.Requests(); len(reqs) != 0 {
		t.Errorf("interceptor stub received %d request(s) for an oversize body; the size gate must reject before invoking the scanner", len(reqs))
	}
}
