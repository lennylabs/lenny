// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/platformtools/seamrow"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/session/state"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
	"github.com/lennylabs/lenny/tests/testinfra/toolseam"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/policy/interceptor"
)

// dlqDelivery is a fake of the session core's message delivery operations:
// HoldMessage enqueues to the coordinator's DLQ, RouteMessage classifies as
// the session core does, and the other operations record their calls.
type dlqDelivery struct {
	seam.MessageDelivery
	coord *sessioninbox.Coordinator

	mu       sync.Mutex
	locked   int
	unlocked int
	holds    int
	// undelivered counts HoldUndeliveredForward calls.
	undelivered int
	outcome     seam.HoldOutcome
	holdErr     error
	deliverErr  error
	admitErr    error
	admits      int
	delivered   []sessionstore.Session
	resumed     []sessionstore.Session
	order       []string
}

func (d *dlqDelivery) LockDelivery(context.Context, string) (func(), error) {
	d.mu.Lock()
	d.locked++
	d.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			d.unlocked++
			d.mu.Unlock()
		})
	}, nil
}

// RouteMessage classifies a message through the session core's routing.
func (d *dlqDelivery) RouteMessage(st state.State, inputRequired bool) seam.MessageRoute {
	return (*sessionserver.Server)(nil).ToolsSeam().RouteMessage(st, inputRequired)
}

func (d *dlqDelivery) HoldMessage(ctx context.Context, tenantID, sessionID string, msg seam.InboxMessage) (seam.HoldOutcome, state.State, error) {
	d.mu.Lock()
	d.holds++
	outcome, herr := d.outcome, d.holdErr
	d.mu.Unlock()
	if outcome == 0 {
		outcome = seam.HoldHeld
	}
	if herr != nil {
		return seam.HoldHeld, "", herr
	}
	if outcome == seam.HoldTerminal {
		return outcome, state.State(session.StateCompleted), nil
	}
	held := sessioninbox.Message{MessageID: msg.MessageID, SenderSessionID: msg.SenderSessionID, Payload: msg.Payload, EnqueuedAt: msg.EnqueuedAt}
	if _, err := d.coord.EnqueueDLQ(ctx, tenantID, sessionID, held, 0); err != nil {
		return seam.HoldHeld, "", err
	}
	return outcome, state.State(session.StateSuspended), nil
}

// HoldUndeliveredForward records the hold of a message whose forward was not
// delivered and holds it as HoldMessage does.
func (d *dlqDelivery) HoldUndeliveredForward(ctx context.Context, s seam.Session, msg seam.InboxMessage) (seam.HoldOutcome, state.State, error) {
	d.mu.Lock()
	d.undelivered++
	d.mu.Unlock()
	return d.HoldMessage(ctx, s.TenantID, s.ID, msg)
}

func (d *dlqDelivery) DeliverHeld(_ context.Context, s seam.Session) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.order = append(d.order, "held")
	if d.deliverErr != nil {
		return d.deliverErr
	}
	d.delivered = append(d.delivered, seamrow.Row(s))
	return nil
}

func (d *dlqDelivery) ResumeSuspended(_ context.Context, s seam.Session) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.resumed = append(d.resumed, seamrow.Row(s))
	return nil
}

// AdmitResume records the check and answers admitErr, with a session
// server cap refusal answered as the session core answers it.
func (d *dlqDelivery) AdmitResume(context.Context, seam.Session) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.admits++
	d.order = append(d.order, "admit")
	var refusal *sessionserver.CapRefusal
	if errors.As(d.admitErr, &refusal) {
		return &seam.CapRefusal{Message: refusal.Message(), Details: refusal.Details()}
	}
	return d.admitErr
}

// countingExecutor counts sends; onSend, when set, answers a send.
type countingExecutor struct {
	sends  int
	onSend func() error
}

func (e *countingExecutor) Send(_ context.Context, _ string, msgs []executor.Message) (executor.Response, error) {
	if e.onSend != nil {
		if err := e.onSend(); err != nil {
			return executor.Response{}, err
		}
	}
	e.sends++
	return executor.Response{Parts: []executor.MessagePart{{Type: "text", Text: "echo: " + msgs[0].Content}}}, nil
}

func (e *countingExecutor) Close(context.Context, string) error { return nil }

// newMCPRoutingDelivery builds the routing server with a dlqDelivery seam.
// A nil exec selects the echo executor.
func newMCPRoutingDelivery(t *testing.T, exec executor.Executor) (*mcp.Server, sessionstore.Store, *sessioninbox.MemoryInbox, *sessioninbox.DLQ, *dlqDelivery) {
	t.Helper()
	return newMCPRoutingDeliveryWith(t, exec, nil)
}

// newMCPRoutingDeliveryWith is newMCPRoutingDelivery with a Deps mutator.
func newMCPRoutingDeliveryWith(t *testing.T, exec executor.Executor, mutate func(*mcptools.Deps)) (*mcp.Server, sessionstore.Store, *sessioninbox.MemoryInbox, *sessioninbox.DLQ, *dlqDelivery) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	inbox := sessioninbox.NewMemoryInbox(10)
	dlq := sessioninbox.NewDLQ(rc, 10)
	coord := sessioninbox.NewCoordinator(sessioninbox.Config{Inbox: inbox, DLQ: dlq})
	store := memstore.New()
	if exec == nil {
		exec = executor.NewEchoExecutor()
	}
	del := &dlqDelivery{coord: coord}
	deps := mcptools.Deps{
		SessionService:    ownerprincipal.ToolAccess{},
		SessionAccess:     ownerprincipal.ToolAccess{},
		DevTenantFallback: true,
		Store:             store,
		Executor:          toolseam.Executor(exec),
		InputWaits:        inputwait.NewRegistry(),
		Messaging:         toolseam.Messaging(coord),
		Delivery:          del,
		Clock:             func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:            func() string { return "msg_fixed" },
		TenantID:          "acme",
	}
	if mutate != nil {
		mutate(&deps)
	}
	srv := mcp.NewServer()
	mcptools.Register(srv, deps)
	return srv, store, inbox, dlq, del
}

// rejectingChain is an interceptor chain whose PreMessageDelivery phase
// rejects every message.
func rejectingChain(t *testing.T) *interceptor.Chain {
	t.Helper()
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreMessageDelivery, staticInterceptor{
		result: interceptor.Result{Action: interceptor.ActionReject, Reason: "refused"},
	}); err != nil {
		t.Fatalf("register interceptor: %v", err)
	}
	return chain
}
