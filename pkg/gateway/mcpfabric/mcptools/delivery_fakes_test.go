// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// dlqDelivery is a SessionDelivery fake: HoldMessage enqueues to the
// coordinator's DLQ and the other methods record their calls.
type dlqDelivery struct {
	coord *sessioninbox.Coordinator

	mu       sync.Mutex
	locked   int
	unlocked int
	holds    int
	// undelivered counts HoldUndeliveredForward calls.
	undelivered int
	outcome     sessionserver.HoldOutcome
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

func (d *dlqDelivery) HoldMessage(ctx context.Context, tenantID, sessionID string, msg sessioninbox.Message) (sessionserver.HoldOutcome, session.State, error) {
	d.mu.Lock()
	d.holds++
	outcome, herr := d.outcome, d.holdErr
	d.mu.Unlock()
	if herr != nil {
		return sessionserver.HoldQueued, "", herr
	}
	if outcome == sessionserver.HoldTerminal {
		return outcome, session.StateCompleted, nil
	}
	if _, err := d.coord.EnqueueDLQ(ctx, tenantID, sessionID, msg, 0); err != nil {
		return sessionserver.HoldQueued, "", err
	}
	return outcome, session.StateSuspended, nil
}

// HoldUndeliveredForward records the hold of a message whose forward was not
// delivered and holds it as HoldMessage does.
func (d *dlqDelivery) HoldUndeliveredForward(ctx context.Context, row sessionstore.Session, msg sessioninbox.Message) (sessionserver.HoldOutcome, session.State, error) {
	d.mu.Lock()
	d.undelivered++
	d.mu.Unlock()
	return d.HoldMessage(ctx, row.TenantID, row.ID, msg)
}

func (d *dlqDelivery) DeliverHeld(_ context.Context, row sessionstore.Session) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.order = append(d.order, "held")
	if d.deliverErr != nil {
		return d.deliverErr
	}
	d.delivered = append(d.delivered, row)
	return nil
}

func (d *dlqDelivery) ResumeSuspended(_ context.Context, row sessionstore.Session) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.resumed = append(d.resumed, row)
	return nil
}

// AdmitResume records the check and answers admitErr.
func (d *dlqDelivery) AdmitResume(context.Context, sessionstore.Session) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.admits++
	d.order = append(d.order, "admit")
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
		SessionService:    ownerprincipal.SessionAccess{},
		DevTenantFallback: true,
		Store:             store,
		Executor:          exec,
		InputWaits:        inputwait.NewRegistry(),
		Messaging:         coord,
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
