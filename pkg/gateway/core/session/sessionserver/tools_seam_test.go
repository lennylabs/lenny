// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/environment/environmentmw"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// TestToolsSeamCarriesTheStoredRowAcrossTheSeam checks that the record of a
// stored row carries the row, that the core reads the carried row with every
// field, and that a record carrying no row is read as the row its fields
// describe.
// spec: GWSEAM-ForwardToolCall, GWSEAM-AfterTerminalWrite
func TestToolsSeamCarriesTheStoredRowAcrossTheSeam(t *testing.T) {
	row := sessionstore.Session{ID: "s1", TenantID: "acme", UserID: "alice", State: session.StateSuspended, PodAssignment: "pod-1", PoolRef: "p", LastSeq: 7}
	rec := storedSession(row)
	if rec.ID != "s1" || rec.Pool != "p" || rec.LastSeq != 7 || rec.State != state.State(session.StateSuspended) {
		t.Fatalf("record = %+v", rec)
	}
	if got := rowOf(rec); got.PodAssignment != "pod-1" {
		t.Fatalf("row of a carrying record lost its pod assignment: %+v", got)
	}
	rec.Stored = nil
	got := rowOf(rec)
	if got.ID != "s1" || got.TenantID != "acme" || got.UserID != "alice" || got.PoolRef != "p" || got.LastSeq != 7 || got.PodAssignment != "pod-1" || got.RetryPolicy != nil {
		t.Fatalf("row of a bare record = %+v", got)
	}
}

// TestToolsSeamCreateSessionDecodesTheBodyAsTheToolDid checks the create
// operation's refusals and its decode: a body that does not decode and a
// body that names no runtime are refused with VALIDATION_ERROR, the
// connection's explicit environment fills a missing one, and a refusal of the creation service comes back with its code.
// spec: GWSEAM-CreateSession
func TestToolsSeamCreateSessionDecodesTheBodyAsTheToolDid(t *testing.T) {
	srv := New(memstore.New(), Options{IDFunc: func() string { return "sess_new" }})
	tools := srv.ToolsSeam()
	ctx := context.Background()

	_, err := tools.CreateSession(ctx, "acme", json.RawMessage(`{"runtimeRef":7}`))
	if r, ok := seam.IsRefusal(err); !ok || r.Code != "VALIDATION_ERROR" || r.Details != nil {
		t.Fatalf("undecodable body = %v, want a VALIDATION_ERROR refusal", err)
	}
	_, err = tools.CreateSession(ctx, "acme", json.RawMessage(`{}`))
	if r, ok := seam.IsRefusal(err); !ok || r.Code != "VALIDATION_ERROR" || r.Message != "runtimeRef is required" || r.Details["field"] != "runtimeRef" {
		t.Fatalf("body with no runtime = %v, want runtimeRef is required", err)
	}
	// With no principal the service refuses the create, which comes back
	// as the service's own refusal.
	envCtx := environmentmw.WithExplicitEnvironment(ctx, "team-x")
	_, err = tools.CreateSession(envCtx, "acme", json.RawMessage(`{"runtimeRef":"echo"}`))
	if r, ok := seam.IsRefusal(err); !ok || r.Code != "UNAUTHORIZED" {
		t.Fatalf("create with no principal = %v, want the service's UNAUTHORIZED refusal", err)
	}
}

// TestToolsSeamInvokeClientAPIReportsARefusalAsAResultOutsideTwoHundred
// checks that a refused in-process request comes back as a result whose
// status is outside 2xx with the refusal's code, and no error.
// spec: GWSEAM-InvokeClientAPI
func TestToolsSeamInvokeClientAPIReportsARefusalAsAResultOutsideTwoHundred(t *testing.T) {
	srv := New(memstore.New(), Options{})
	res, err := srv.ToolsSeam().InvokeClientAPI(context.Background(), seam.ClientAPICall{TenantID: "acme", Method: http.MethodGet, Target: "/v1/sessions/missing"})
	if err != nil || res.Status < 300 || res.ErrorCode == "" {
		t.Fatalf("InvokeClientAPI = %+v, %v; want a refusal result", res, err)
	}
}

// sealingExecutor answers every send with a fixed error.
type sealingExecutor struct {
	executor.Executor
	err error
}

func (e sealingExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, e.err
}

// TestToolsSeamSendMessagesMapsTheExecutorsFailures checks that a partial
// sealed delivery comes back holding a *seam.SealedError with its count and
// response, that a sealed session and a session with no binding match the
// seam's sentinels with the executor's message, and that a delivered
// response keeps its parts.
// spec: GWSEAM-SendMessages
func TestToolsSeamSendMessagesMapsTheExecutorsFailures(t *testing.T) {
	ctx := context.Background()
	partial := &executor.SealedError{Delivered: 1, Response: executor.Response{Parts: []executor.MessagePart{{Type: "text", Text: "ok"}}}}
	_, err := New(memstore.New(), Options{Executor: sealingExecutor{err: partial}}).ToolsSeam().SendMessages(ctx, "s1", []seam.Message{{Role: "user", Content: "hi"}})
	var sealed *seam.SealedError
	if !errors.As(err, &sealed) || sealed.Delivered != 1 || len(sealed.Response.Parts) != 1 || err.Error() != partial.Error() {
		t.Fatalf("partial sealed delivery = %v", err)
	}
	unbound := fmt.Errorf("podexec: session s1: %w", executor.ErrNotBound)
	_, err = New(memstore.New(), Options{Executor: sealingExecutor{err: unbound}}).ToolsSeam().SendMessages(ctx, "s1", nil)
	if !errors.Is(err, seam.ErrNotBound) || err.Error() != unbound.Error() {
		t.Fatalf("unbound delivery = %v", err)
	}
	if !errors.Is(seamDeliveryError(executor.ErrSessionSealed), seam.ErrSessionSealed) {
		t.Error("a sealed session does not match seam.ErrSessionSealed")
	}
	if !errors.Is(seamDeliveryError(fmt.Errorf("deliver: %w", ErrHeldDeliveryPreempted)), seam.ErrHeldDeliveryPreempted) {
		t.Error("a preempted held delivery does not match seam.ErrHeldDeliveryPreempted")
	}
	resp, err := New(memstore.New(), Options{Executor: executor.NewEchoExecutor()}).ToolsSeam().SendMessages(ctx, "s1", []seam.Message{{Role: "user", Content: "hi", FromKind: "agent", FromID: "s0"}})
	if err != nil || len(resp.Parts) == 0 {
		t.Fatalf("delivered response = %+v, %v", resp, err)
	}
	if _, err := New(memstore.New(), Options{}).ToolsSeam().SendMessages(ctx, "s1", nil); err == nil || err.Error() != "no executor configured" {
		t.Fatalf("send with no executor = %v", err)
	}
}

// TestToolsSeamRoutesAndHoldsMessages checks the routing of a message from
// another session, the hold outcomes, and the inbox with no coordinator.
// spec: GWSEAM-RouteMessage, GWSEAM-HoldMessage, GWSEAM-EnqueueInbox
func TestToolsSeamRoutesAndHoldsMessages(t *testing.T) {
	tools := New(memstore.New(), Options{}).ToolsSeam()
	cases := map[session.State]seam.MessageRoute{
		session.StateCompleted:     seam.RouteTerminal,
		session.StateSuspended:     seam.RouteResume,
		session.StateInputRequired: seam.RouteInbox,
		session.StateRunning:       seam.RouteDeliver,
		session.StateCreated:       seam.RouteHold,
	}
	for st, want := range cases {
		if got := tools.RouteMessage(state.State(st), false); got != want {
			t.Errorf("RouteMessage(%s) = %d, want %d", st, got, want)
		}
	}
	if got := tools.RouteMessage(state.State(session.StateRunning), true); got != seam.RouteInbox {
		t.Errorf("a running session waiting for input routes to %d, want the inbox", got)
	}
	for core, want := range map[HoldOutcome]seam.HoldOutcome{HoldQueued: seam.HoldHeld, HoldDropped: seam.HoldDropped, HoldTerminal: seam.HoldTerminal} {
		if got := seamHoldOutcome(core); got != want {
			t.Errorf("seamHoldOutcome(%d) = %d, want %d", core, got, want)
		}
	}
	if _, err := tools.EnqueueInbox(context.Background(), "acme", "s1", seam.InboxMessage{MessageID: "m1"}); !errors.Is(err, sessioninbox.ErrMessagingUnavailable) {
		t.Errorf("EnqueueInbox with no coordinator = %v", err)
	}
	if _, _, err := tools.HoldMessage(context.Background(), "acme", "s1", seam.InboxMessage{MessageID: "m1"}); !errors.Is(err, sessioninbox.ErrMessagingUnavailable) {
		t.Errorf("HoldMessage with no coordinator = %v", err)
	}
}

// TestToolsSeamForwardingRefusesAnUnknownKind checks that a forward kind
// the core has no row for is refused without a forward and holds nothing,
// and that each kind of the platform tools seam maps to a row.
// spec: GWSEAM-ForwardToolCall, GWSEAM-HoldsUndelivered
func TestToolsSeamForwardingRefusesAnUnknownKind(t *testing.T) {
	tools := New(memstore.New(), Options{}).ToolsSeam()
	rec := storedSession(sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateRunning})
	_, out, err := tools.ForwardToolCall(context.Background(), rec, seam.ForwardKind("bogus"), "lenny/x", nil)
	if r, ok := seam.IsRefusal(err); !ok || r.Code != "INTERNAL_ERROR" || out != seam.ForwardNotDelivered {
		t.Fatalf("unknown kind = %v, %v", out, err)
	}
	if tools.HoldsUndelivered(rec, seam.ForwardKind("bogus")) {
		t.Error("an unknown kind holds an undelivered message")
	}
	if !tools.HoldsUndelivered(rec, seam.ForwardMessageNoReplyTo) || tools.HoldsUndelivered(rec, seam.ForwardMessage) {
		t.Error("the message kinds do not hold as the forwarding rules state")
	}
	for _, k := range []seam.ForwardKind{seam.ForwardTerminate, seam.ForwardMessage, seam.ForwardMessageNoReplyTo} {
		if _, ok := toolForwardKinds[k]; !ok {
			t.Errorf("forward kind %q maps to no row", k)
		}
	}
	if tools.Forwarded(context.Background()) || !tools.Forwarded(WithForwarded(context.Background())) {
		t.Error("Forwarded does not read the forward listener's mark")
	}
}

// TestToolsSeamAppliesTheOwnershipRule checks the ownership rule on a record
// the seam read.
// spec: GWSEAM-SessionAccessible
func TestToolsSeamAppliesTheOwnershipRule(t *testing.T) {
	tools := New(memstore.New(), Options{}).ToolsSeam()
	ctx := authmw.WithPrincipal(context.Background(), authmw.Principal{Subject: "alice", TenantID: "acme"})
	own := seam.Session{ID: "s1", TenantID: "acme", UserID: "alice"}
	if !tools.SessionAccessible(ctx, own, seam.SessionRead) || !tools.SessionAccessible(ctx, own, seam.SessionManage) {
		t.Error("the owner may not read or manage its session")
	}
	other := seam.Session{ID: "s2", TenantID: "acme", UserID: "bob"}
	if tools.SessionAccessible(ctx, other, seam.SessionManage) {
		t.Error("a caller may manage another user's session")
	}
	if tools.SessionAccessible(ctx, own, seam.SessionAction(0)) {
		t.Error("an unknown action is granted")
	}
}

// TestToolsSeamExportPathsReportsNoLiveBinding checks that a session with
// no binding on this replica reports seam.ErrNoLiveBinding.
// spec: GWSEAM-ExportPaths
func TestToolsSeamExportPathsReportsNoLiveBinding(t *testing.T) {
	if _, err := New(memstore.New(), Options{}).ToolsSeam().ExportPaths(context.Background(), "s1", []seam.ExportSpec{{Source: "*"}}); !errors.Is(err, seam.ErrNoLiveBinding) {
		t.Fatalf("ExportPaths = %v, want ErrNoLiveBinding", err)
	}
}

// TestToolsSeamExperimentRoutingAndRetryBudgetReadTheStoredRow checks that
// the experiment routing returns the routed row in the record, and that
// the retry budget reads the stored row's retry policy.
// spec: GWSEAM-ApplyExperimentRouting, GWSEAM-EffectiveMaxRetries
func TestToolsSeamExperimentRoutingAndRetryBudgetReadTheStoredRow(t *testing.T) {
	tools := New(memstore.New(), Options{}).ToolsSeam()
	child := storedSession(sessionstore.Session{ID: "c1", TenantID: "acme", RuntimeRef: "echo", IsolationProfile: "sandboxed"})
	if err := tools.ApplyExperimentRouting(context.Background(), &child); err != nil {
		t.Fatalf("ApplyExperimentRouting: %v", err)
	}
	if got := rowOf(child); got.RuntimeRef != "echo" || got.IsolationProfile != "sandboxed" {
		t.Fatalf("routed row = %+v, want the row unchanged with no experiment", got)
	}
	withPolicy := storedSession(sessionstore.Session{ID: "c1", RetryPolicy: &session.RetryPolicy{MaxRetries: 5}})
	if got := tools.EffectiveMaxRetries(withPolicy); got != 5 {
		t.Errorf("EffectiveMaxRetries = %d, want the stored row's 5", got)
	}
}
