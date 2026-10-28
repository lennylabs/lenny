// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// parentEndingStore wraps a session store so the parent session moves to a
// terminal state after every admission gate passed and before the child's
// insert reaches the wrapped store, on both the reserved and the unreserved
// insert path.
type parentEndingStore struct {
	sessionstore.Store
	inserts []string
}

func (s *parentEndingStore) endParent(ctx context.Context) error {
	_, err := s.Store.Update(ctx, "acme", "sess_parent", func(r *sessionstore.Session) error {
		r.State = session.StateCancelled
		return nil
	})
	return err
}

func (s *parentEndingStore) Create(ctx context.Context, row sessionstore.Session) error {
	if row.ID == "sess_child" {
		s.inserts = append(s.inserts, "Create")
		if err := s.endParent(ctx); err != nil {
			return err
		}
	}
	return s.Store.Create(ctx, row)
}

func (s *parentEndingStore) CreateReserved(ctx context.Context, row sessionstore.Session) error {
	s.inserts = append(s.inserts, "CreateReserved")
	if err := s.endParent(ctx); err != nil {
		return err
	}
	return s.Store.CreateReserved(ctx, row)
}

// okExportMaterializer is a §8.7 export materializer that writes nothing and
// succeeds, so the delegation reaches the reserved insert.
type okExportMaterializer struct{}

func (okExportMaterializer) Materialize(context.Context, export.Params) (export.Result, error) {
	return export.Result{}, nil
}

// deletedObjects records each export-object deletion the delegation's undo
// runs.
type deletedObjects struct {
	mu  sync.Mutex
	ids []string
}

func (d *deletedObjects) DeleteBySession(_ context.Context, _, sessionID string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ids = append(d.ids, sessionID)
	return 1, nil
}

// TestDelegateTaskParentEndedBeforeInsertAnswersParentRevoked_spec_8_2 moves
// the parent to a terminal state between admission and the child's insert,
// on the reserved (file-export) path and on the unreserved path. The tool
// answers DELEGATION_PARENT_REVOKED with details.parentSessionId, which is the
// delegate_task mapping of the fence's CreationRefusal. No child row exists,
// the reserved path's export objects are deleted and its reservation
// released, and the child materializer claims no pod.
//
// spec: 8.2 (Parent freshness), 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.1 (Error catalog)
func TestDelegateTaskParentEndedBeforeInsertAnswersParentRevoked_spec_8_2(t *testing.T) {
	cases := []struct {
		name       string
		args       string
		wantInsert string
	}{
		{
			name:       "reserved",
			args:       `{"parentSessionId":"sess_parent","target":"child-agent","poolRef":"pool-b","task":{"input":[{"type":"text","inline":"do work"}],"workspaceFiles":{"export":[{"source":"docs/*.txt"}]}}}`,
			wantInsert: "CreateReserved",
		},
		{name: "unreserved", args: delegateArgs, wantInsert: "Create"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := memstore.New()
			store := &parentEndingStore{Store: mem}
			objects := &deletedObjects{}
			pods := &fakeChildMaterializer{state: session.StateRunning}
			runtimes := runtimestore.NewMemory()
			srv := mcp.NewServer()
			mcptools.Register(srv, mcptools.Deps{
				DevTenantFallback: true,
				Store:             store,
				Executor:          executor.NewEchoExecutor(),
				Runtimes:          runtimes,
				ChildMaterializer: pods,
				Delegation: delegation.NewService(store, delegation.Options{
					IDFunc:             func() string { return "sess_child" },
					ExportMaterializer: okExportMaterializer{},
					SessionObjects:     objects,
				}),
				Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
				IDFunc:   func() string { return "sess_mcp" },
				TenantID: "acme",
			})
			ctx := context.Background()
			if err := runtimes.Create(ctx, runtimestore.Runtime{Name: "child-agent", Type: runtimestore.TypeAgent}); err != nil {
				t.Fatalf("seed runtime: %v", err)
			}
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			if err := mem.Create(ctx, sessionstore.Session{
				ID: "sess_parent", TenantID: "acme", UserID: "user_alice", State: session.StateRunning,
				RuntimeRef: "child-agent", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
				CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatalf("seed parent: %v", err)
			}

			resp := call(t, srv.Handler(), "lenny/delegate_task", tc.args)
			result, _ := resp["result"].(map[string]any)
			env := readLennyErrorEnvelope(t, result)
			if env["code"] != "DELEGATION_PARENT_REVOKED" {
				t.Fatalf("code = %v, want DELEGATION_PARENT_REVOKED (envelope %v)", env["code"], env)
			}
			details, _ := env["details"].(map[string]any)
			if details["parentSessionId"] != "sess_parent" {
				t.Errorf("details = %v, want parentSessionId sess_parent", details)
			}
			if len(store.inserts) != 1 || store.inserts[0] != tc.wantInsert {
				t.Errorf("inserts = %v, want one %s", store.inserts, tc.wantInsert)
			}
			if _, err := mem.Get(ctx, "acme", "sess_child"); !errors.Is(err, sessionstore.ErrNotFound) {
				t.Errorf("child row after the refusal: %v, want ErrNotFound", err)
			}
			if len(pods.calls) != 0 {
				t.Errorf("the child materializer claimed a pod for %v after the refusal", pods.calls)
			}
			if tc.wantInsert == "Create" {
				if len(objects.ids) != 0 {
					t.Errorf("unreserved refusal deleted objects %v, want none", objects.ids)
				}
				return
			}
			if len(objects.ids) != 1 || objects.ids[0] != "sess_child" {
				t.Errorf("export objects deleted = %v, want [sess_child]", objects.ids)
			}
			ids, err := mem.ReservedSessionIDs(ctx, "acme", "user_alice")
			if err != nil {
				t.Fatalf("ReservedSessionIDs: %v", err)
			}
			if len(ids) != 0 {
				t.Errorf("reservation kept after the refusal: %v", ids)
			}
		})
	}
}
