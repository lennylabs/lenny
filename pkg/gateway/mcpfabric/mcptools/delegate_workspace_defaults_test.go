// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// unreadableRuntime fails every Get of one runtime with a transient error.
type unreadableRuntime struct {
	runtimestore.Store
	name string
}

func (u unreadableRuntime) Get(ctx context.Context, name string) (runtimestore.Runtime, error) {
	if name == u.name {
		return runtimestore.Runtime{}, errors.New("connection reset")
	}
	return u.Store.Get(ctx, name)
}

// TestDelegateTaskDefaultsLookupIsServiceUnavailable_spec_14_1 pins that a
// delegation failed by a transient read of the child runtime's
// workspaceDefaults surfaces as the retryable SERVICE_UNAVAILABLE tool error
// rather than the INTERNAL_ERROR fallback, and creates no child.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime), 15.2.1 (REST/MCP Consistency Contract)
func TestDelegateTaskDefaultsLookupIsServiceUnavailable_spec_14_1(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "child-agent", Type: runtimestore.TypeAgent}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		DevTenantFallback: true,
		Store:             store,
		Executor:          executor.NewEchoExecutor(),
		Runtimes:          runtimes,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc:   func() string { return "sess_child" },
			Runtimes: unreadableRuntime{Store: runtimes, name: "child-agent"},
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, sessionstore.Session{
		ID: "sess_parent", State: session.StateRunning, TenantID: "acme", UserID: "user_alice",
		RuntimeRef: "child-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "SERVICE_UNAVAILABLE" {
		t.Fatalf("code = %v, want SERVICE_UNAVAILABLE (envelope %v)", env["code"], env)
	}
	if env["retryable"] != true {
		t.Errorf("retryable = %v, want true", env["retryable"])
	}
	if _, err := store.Get(ctx, "acme", "sess_child"); err == nil {
		t.Error("a child row exists after the defaults lookup failed")
	}
}
