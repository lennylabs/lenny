// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the §8.3 tracing-context
// registration the gateway performs for `lenny/set_tracing_context`. Two
// independent writers reach that handler for the same session: the adapter
// forwards the §28.5.3 `set_tracing_context` JSONL frame the runtime writes,
// and the runtime's own platform MCP client calls the tool directly. They run
// on separate goroutines with no ordering between them, so the merge is only
// additive if it is computed against the row the store's update transaction
// holds locked.
//
// spec: §8.3 (a child entry cannot overwrite or remove a parent entry),
// §28.5.3 (set_tracing_context registration).

package tier7a_load_local_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// tracingBarrierStore wraps a session store and runs a barrier once, on the
// first Update, before the wrapped store takes its row lock. It reproduces
// the interleaving the two registration legs produce: one writer is inside
// the handler with its read already taken while the other writer completes a
// whole registration.
type tracingBarrierStore struct {
	sessionstore.Store
	armed   atomic.Bool
	barrier func()
}

func (s *tracingBarrierStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if s.armed.CompareAndSwap(true, false) {
		s.barrier()
	}
	return s.Store.Update(ctx, tenantID, id, mutate)
}

// newTracingMCP builds the platform MCP surface over store with one running
// session seeded, and returns its handler.
func newTracingMCP(t *testing.T, store sessionstore.Store, sessionID string) http.Handler {
	t.Helper()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: sessionID, TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session %s: %v", sessionID, err)
	}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Clock:    func() time.Time { return now },
		TenantID: "acme",
	})
	return srv.Handler()
}

// registerTracingContext calls lenny/set_tracing_context with one identifier
// and fails the test when the call reports an error.
func registerTracingContext(t *testing.T, h http.Handler, sessionID, key, value string) {
	t.Helper()
	args := fmt.Sprintf(`{"sessionId":%q,"context":{%q:%q}}`, sessionID, key, value)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/set_tracing_context","arguments":` + args + `}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode set_tracing_context response: %v; body=%s", err, rr.Body.String())
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result registering %s=%s: %+v", key, value, resp)
	}
	if result["isError"] == true {
		t.Fatalf("registering %s=%s reported an error: %+v", key, value, result)
	}
}

// spec: 8.3 (tracing-context merge is additive), 28.5.3
// (set_tracing_context registration)
//
// diagnosis: the merge the gateway registers is computed outside the session
//
//	row's update transaction. A failure means one leg's registration
//	overwrote the other's while the tool still reported success, so the
//	identifiers named in the result are not the identifiers the session
//	carries and the child leases the row feeds lose an entry.
func TestConcurrentTracingContextRegistrationsBothSurvive_spec_8_3(t *testing.T) {
	const sessionID = "sess_tracing_race"
	store := &tracingBarrierStore{Store: memstore.New()}
	h := newTracingMCP(t, store, sessionID)

	// The barrier fires once the first registration is inside Update: the
	// second leg completes its whole registration there, so the first leg's
	// write lands on a row that already carries the second leg's identifier.
	store.barrier = func() {
		registerTracingContext(t, h, sessionID, "runtime_leg", "mcp")
	}
	store.armed.Store(true)
	registerTracingContext(t, h, sessionID, "adapter_leg", "jsonl")

	row, err := store.Get(context.Background(), "acme", sessionID)
	if err != nil {
		t.Fatalf("read session %s: %v", sessionID, err)
	}
	for key, want := range map[string]string{"adapter_leg": "jsonl", "runtime_leg": "mcp"} {
		if row.TracingContext[key] != want {
			t.Errorf("§8.3 violation: session %s carries tracingContext %v, want %s=%s: a registration that "+
				"interleaved with another was reported as successful and then discarded",
				sessionID, row.TracingContext, key, want)
		}
	}
}

// spec: 8.3 (tracing-context merge is additive), 28.5.3
// (set_tracing_context registration)
//
// diagnosis: the same lost-update defect under real parallelism rather than a
//
//	forced interleaving. A failure means the registration path drops
//	entries whenever two writers overlap, which is the steady state on a
//	pod whose runtime writes the JSONL frame while its MCP client
//	registers over the platform leg.
func TestParallelTracingContextRegistrationsAllSurvive_spec_8_3(t *testing.T) {
	const (
		sessionID = "sess_tracing_parallel"
		writers   = 16
	)
	store := memstore.New()
	h := newTracingMCP(t, store, sessionID)

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			registerTracingContext(t, h, sessionID, fmt.Sprintf("run_id_%02d", i), fmt.Sprintf("v%02d", i))
		}(i)
	}
	wg.Wait()

	row, err := store.Get(context.Background(), "acme", sessionID)
	if err != nil {
		t.Fatalf("read session %s: %v", sessionID, err)
	}
	for i := 0; i < writers; i++ {
		key := fmt.Sprintf("run_id_%02d", i)
		if want := fmt.Sprintf("v%02d", i); row.TracingContext[key] != want {
			t.Errorf("§8.3 violation: session %s is missing %s=%s after %d parallel registrations; it carries %v",
				sessionID, key, want, writers, row.TracingContext)
		}
	}
}
