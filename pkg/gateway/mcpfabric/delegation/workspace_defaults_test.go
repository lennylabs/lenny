// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// failingRuntimeGet fails Get for the named runtimes with err.
type failingRuntimeGet struct {
	runtimestore.Store
	fail map[string]error
}

func (f failingRuntimeGet) Get(ctx context.Context, name string) (runtimestore.Runtime, error) {
	if err := f.fail[name]; err != nil {
		return runtimestore.Runtime{}, err
	}
	return f.Store.Get(ctx, name)
}

// defaultsRuntimes registers the delegation target "claude" and a variant
// runtime, each with its own workspaceDefaults.
func defaultsRuntimes(t *testing.T) *runtimestore.Memory {
	t.Helper()
	mem := runtimestore.NewMemory()
	for _, rt := range []runtimestore.Runtime{
		{Name: "claude", Image: "lenny/claude@sha256:abc", Type: runtimestore.TypeAgent, WorkspaceDefaults: &runtimestore.WorkspaceDefaults{
			Files:         []runtimestore.WorkspaceFile{{Path: "CLAUDE.md", Content: "default"}},
			SetupCommands: []runtimestore.WorkspaceSetupCommand{{Cmd: "echo defaults", TimeoutSeconds: 15}},
		}},
		{Name: "claude-variant", Image: "lenny/claude@sha256:def", Type: runtimestore.TypeAgent, WorkspaceDefaults: &runtimestore.WorkspaceDefaults{
			Files: []runtimestore.WorkspaceFile{{Path: "variant.txt", Content: "v"}},
		}},
	} {
		if err := mem.Create(context.Background(), rt); err != nil {
			t.Fatalf("seed runtime %s: %v", rt.Name, err)
		}
	}
	return mem
}

// childPlanPaths parses a child row's plan and lists its source paths.
func childPlanPaths(t *testing.T, raw []byte) []string {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	plan, _, err := workspaceplan.ParseStored(raw)
	if err != nil {
		t.Fatalf("parse child plan %s: %v", raw, err)
	}
	out := make([]string, 0, len(plan.Sources))
	for _, s := range plan.Sources {
		out = append(out, s.Variant.Path())
	}
	return out
}

func exportResult() export.Result {
	return export.Result{
		Sources: []workspaceplan.Source{{
			Type:    workspaceplan.TypeUploadFile,
			Variant: workspaceplan.UploadFile{PathField: "docs/a.txt", UploadRef: "lenny-blob://acme/export/sess_child/docs/a.txt?ttl=3600"},
			Raw: map[string]any{
				"type": workspaceplan.TypeUploadFile, "path": "docs/a.txt",
				"uploadRef": "lenny-blob://acme/export/sess_child/docs/a.txt?ttl=3600",
			},
		}},
		FileCount: 1,
	}
}

// TestDelegatedChildPlanComposesRuntimeDefaults_spec_14_1 pins that a
// delegated child's plan carries its routed runtime's defaults ahead of the
// file-export sources, that a child with no export binds the defaults alone,
// and that a transient read of the runtime fails the delegation with the
// lookup sentinel, writes no child row, and undoes the export.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime), 10.7 (Experiment Primitives)
func TestDelegatedChildPlanComposesRuntimeDefaults_spec_14_1(t *testing.T) {
	t.Run("defaults-then-export", func(t *testing.T) {
		f, svc := newDelegationFixture(t, true, func(o *delegation.Options) { o.Runtimes = defaultsRuntimes(t) })
		f.mat.result = exportResult()
		res, err := delegateExport(context.Background(), svc)
		if err != nil {
			t.Fatalf("Delegate: %v", err)
		}
		got := strings.Join(childPlanPaths(t, res.Child.WorkspacePlan), "|")
		if got != "CLAUDE.md|docs/a.txt" {
			t.Errorf("child plan sources = %s, want CLAUDE.md|docs/a.txt", got)
		}
		plan, _, _ := workspaceplan.ParseStored(res.Child.WorkspacePlan)
		if len(plan.SetupCommands) != 1 || plan.SetupCommands[0].TimeoutSeconds != 15 {
			t.Errorf("child setupCommands = %+v, want the default with timeout 15", plan.SetupCommands)
		}
	})
	t.Run("defaults-only", func(t *testing.T) {
		_, svc := newDelegationFixture(t, true, func(o *delegation.Options) { o.Runtimes = defaultsRuntimes(t) })
		res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
			ParentSessionID: "sess_parent", RuntimeRef: "claude", PoolRef: "pool-a",
		})
		if err != nil {
			t.Fatalf("Delegate: %v", err)
		}
		if got := strings.Join(childPlanPaths(t, res.Child.WorkspacePlan), "|"); got != "CLAUDE.md" {
			t.Errorf("child plan sources = %s, want CLAUDE.md", got)
		}
	})
	t.Run("routed-variant", func(t *testing.T) {
		router := &fakeExperimentRouter{route: func(row *sessionstore.Session) error {
			row.RuntimeRef = "claude-variant"
			return nil
		}}
		_, svc := newDelegationFixture(t, true, func(o *delegation.Options) {
			o.Runtimes = defaultsRuntimes(t)
			o.ExperimentRouter = router
		})
		res, err := svc.Delegate(context.Background(), "acme", delegation.Request{
			ParentSessionID: "sess_parent", RuntimeRef: "claude", PoolRef: "pool-a",
		})
		if err != nil {
			t.Fatalf("Delegate: %v", err)
		}
		if got := strings.Join(childPlanPaths(t, res.Child.WorkspacePlan), "|"); got != "variant.txt" {
			t.Errorf("child plan sources = %s, want the variant runtime's variant.txt only", got)
		}
	})
	t.Run("transient-lookup-fails-closed", func(t *testing.T) {
		f, svc := newDelegationFixture(t, true, func(o *delegation.Options) {
			o.Runtimes = failingRuntimeGet{Store: defaultsRuntimes(t), fail: map[string]error{"claude": errors.New("connection reset")}}
		})
		f.mat.result = exportResult()
		_, err := delegateExport(context.Background(), svc)
		if !errors.Is(err, runtimestore.ErrWorkspaceDefaultsLookup) {
			t.Fatalf("err = %v, want ErrWorkspaceDefaultsLookup", err)
		}
		if _, gerr := f.mem.Get(context.Background(), "acme", "sess_child"); gerr == nil {
			t.Error("a child row was written after the defaults lookup failed")
		}
		if f.store.count("CreateReserved") != 0 || f.store.count("Create") != 0 {
			t.Errorf("insert calls = %v, want none", f.store.calls)
		}
		if len(f.objects.deleted) != 1 || f.objects.deleted[0] != "sess_child" {
			t.Errorf("export objects deleted = %v, want the child's", f.objects.deleted)
		}
		if ids := f.reserved(t); len(ids) != 0 {
			t.Errorf("reservations = %v, want the child reservation released", ids)
		}
	})
}
