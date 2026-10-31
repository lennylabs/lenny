// SPDX-License-Identifier: MIT

package runtimestore_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// clientPlan parses a client plan with Parse, the way a binding request
// receives it.
func clientPlan(t *testing.T, raw string) workspaceplan.Plan {
	t.Helper()
	p, _, err := workspaceplan.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse client plan: %v", err)
	}
	return p
}

// sourcePaths lists each source's type and path, in order.
func sourcePaths(p workspaceplan.Plan) []string {
	out := make([]string, 0, len(p.Sources))
	for _, s := range p.Sources {
		path := ""
		if s.Variant != nil {
			path = s.Variant.Path()
		}
		out = append(out, s.Type+":"+path)
	}
	return out
}

// expectedComposition is the concatenation the composer must produce: the
// defaults' sources and commands followed by the client's.
func expectedComposition(w *runtimestore.WorkspaceDefaults, client workspaceplan.Plan) ([]string, []workspaceplan.SetupCommand) {
	var paths []string
	var cmds []workspaceplan.SetupCommand
	if w != nil {
		for _, f := range w.Files {
			paths = append(paths, workspaceplan.TypeInlineFile+":"+f.Path)
		}
		for _, c := range w.SetupCommands {
			cmds = append(cmds, workspaceplan.SetupCommand{Cmd: c.Cmd, TimeoutSeconds: c.TimeoutSeconds})
		}
	}
	paths = append(paths, sourcePaths(client)...)
	cmds = append(cmds, client.SetupCommands...)
	return paths, cmds
}

func assertComposed(t *testing.T, w *runtimestore.WorkspaceDefaults, client workspaceplan.Plan) {
	t.Helper()
	got, changed, err := w.ComposeInto(client)
	if err != nil {
		t.Fatalf("ComposeInto: %v", err)
	}
	wantChanged := w != nil && (len(w.Files) > 0 || len(w.SetupCommands) > 0)
	if changed != wantChanged {
		t.Fatalf("changed = %v, want %v", changed, wantChanged)
	}
	wantPaths, wantCmds := expectedComposition(w, client)
	if !equalStrings(sourcePaths(got), wantPaths) {
		t.Errorf("sources = %v, want %v", sourcePaths(got), wantPaths)
	}
	if !slices.Equal(got.SetupCommands, wantCmds) {
		t.Errorf("setupCommands = %+v, want %+v", got.SetupCommands, wantCmds)
	}
	if changed && got.SchemaVersion != workspaceplan.SchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", got.SchemaVersion, workspaceplan.SchemaVersion)
	}
}

// TestComposeIntoOrdersDefaultsFirst_spec_14_1 pins the composition order:
// the default files become inlineFile sources and the default commands keep
// their timeoutSeconds, and both precede the client's own entries. A block
// with nothing to compose leaves the plan unchanged and reports false.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime)
func TestComposeIntoOrdersDefaultsFirst_spec_14_1(t *testing.T) {
	files := []runtimestore.WorkspaceFile{
		{Path: "CLAUDE.md", Content: "default instructions"},
		{Path: "config/defaults.txt", Content: "d"},
	}
	cmds := []runtimestore.WorkspaceSetupCommand{
		{Cmd: "make deps", TimeoutSeconds: 120},
		{Cmd: "echo ready"},
	}
	blocks := map[string]*runtimestore.WorkspaceDefaults{
		"nil":           nil,
		"empty":         {},
		"files-only":    {Files: files},
		"commands-only": {SetupCommands: cmds},
		"both":          {Files: files, SetupCommands: cmds},
	}
	clients := map[string]workspaceplan.Plan{
		"empty-client": {},
		"non-empty-client": clientPlan(t, `{"schemaVersion":1,
			"sources":[{"type":"inlineFile","path":"src/main.go","content":"package main"},{"type":"mkdir","path":"out"}],
			"setupCommands":[{"cmd":"go build ./...","timeoutSeconds":60}]}`),
	}
	for bn, w := range blocks {
		for cn, client := range clients {
			t.Run(bn+"/"+cn, func(t *testing.T) { assertComposed(t, w, client) })
		}
	}
}

// TestComposeIntoOrdersDefaultsFirstRandomized_spec_14_1 checks the same
// concatenation over generated defaults and client plans. The seed is
// logged so a failure reproduces.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime)
func TestComposeIntoOrdersDefaultsFirstRandomized_spec_14_1(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	for iter := 0; iter < 200; iter++ {
		w := &runtimestore.WorkspaceDefaults{}
		for i := rng.Intn(4); i > 0; i-- {
			w.Files = append(w.Files, runtimestore.WorkspaceFile{
				Path: fmt.Sprintf("d%d/f%d.txt", iter, i), Content: fmt.Sprint(rng.Int()),
			})
		}
		for i := rng.Intn(4); i > 0; i-- {
			w.SetupCommands = append(w.SetupCommands, runtimestore.WorkspaceSetupCommand{
				Cmd: fmt.Sprintf("echo d%d", i), TimeoutSeconds: rng.Intn(3) * 30,
			})
		}
		raw := `{"schemaVersion":1,"sources":[`
		n := rng.Intn(4)
		for i := 0; i < n; i++ {
			if i > 0 {
				raw += ","
			}
			raw += fmt.Sprintf(`{"type":"inlineFile","path":"c/%d.txt","content":"c"}`, i)
		}
		raw += `],"setupCommands":[`
		m := rng.Intn(3)
		for i := 0; i < m; i++ {
			if i > 0 {
				raw += ","
			}
			raw += fmt.Sprintf(`{"cmd":"echo c%d","timeoutSeconds":%d}`, i, rng.Intn(3)*10)
		}
		raw += `]}`
		client := clientPlan(t, raw)
		t.Run(fmt.Sprintf("iter-%d", iter), func(t *testing.T) { assertComposed(t, w, client) })
	}
}

// TestComposeIntoRejectsRefAndInvalidPath_spec_5_1 pins that a default file
// carries inline content at a valid workspace-relative path: a ref, an
// absolute path, and a parent-escaping path each fail composition.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime)
func TestComposeIntoRejectsRefAndInvalidPath_spec_5_1(t *testing.T) {
	cases := map[string]runtimestore.WorkspaceFile{
		"ref":           {Path: "big.bin", Ref: "lenny-blob://acme/runtime/x/p1"},
		"absolute-path": {Path: "/etc/passwd", Content: "x"},
		"parent-escape": {Path: "../outside", Content: "x"},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			w := &runtimestore.WorkspaceDefaults{Files: []runtimestore.WorkspaceFile{f}}
			if _, changed, err := w.ComposeInto(workspaceplan.Plan{}); err == nil || changed {
				t.Fatalf("ComposeInto = (changed=%v, err=%v), want an error", changed, err)
			}
		})
	}
}

// TestComposeIntoCollisionFollowsPathRule_spec_14_1 pins that a default and a
// client source at one path are settled by the §14.1 path collision rule:
// the client source, later in the array, wins, and the default loses.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime)
func TestComposeIntoCollisionFollowsPathRule_spec_14_1(t *testing.T) {
	w := &runtimestore.WorkspaceDefaults{Files: []runtimestore.WorkspaceFile{
		{Path: "README.md", Content: "default"},
		{Path: "CLAUDE.md", Content: "default"},
	}}
	client := clientPlan(t, `{"schemaVersion":1,"sources":[{"type":"inlineFile","path":"CLAUDE.md","content":"client"}]}`)
	got, _, err := w.ComposeInto(client)
	if err != nil {
		t.Fatalf("ComposeInto: %v", err)
	}
	raw, err := workspaceplan.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, warns, err := workspaceplan.ParseStored(raw)
	if err != nil {
		t.Fatalf("ParseStored: %v", err)
	}
	var found bool
	for _, wn := range warns {
		if wn.Code != workspaceplan.WarnPathCollision {
			continue
		}
		found = true
		if wn.LosingSourceIndex == nil || *wn.LosingSourceIndex != 1 {
			t.Errorf("losing index = %v, want 1 (the default)", wn.LosingSourceIndex)
		}
		if wn.WinningSourceIndex == nil || *wn.WinningSourceIndex != 2 {
			t.Errorf("winning index = %v, want 2 (the client source)", wn.WinningSourceIndex)
		}
	}
	if !found {
		t.Fatalf("warnings = %+v, want %s", warns, workspaceplan.WarnPathCollision)
	}
}

// failingGetStore fails Get for the named runtimes with the mapped error.
type failingGetStore struct {
	runtimestore.Store
	fails map[string]error
}

func (s failingGetStore) Get(ctx context.Context, name string) (runtimestore.Runtime, error) {
	if err := s.fails[name]; err != nil {
		return runtimestore.Runtime{}, err
	}
	return s.Store.Get(ctx, name)
}

// TestComposeWorkspaceDefaultsResolve_spec_14_1 covers each return branch of
// the store-backed composer: no store or runtime, a runtime with no registry
// entry (composes nothing), a transient read of the runtime or of a derived
// runtime's base (wrapped with ErrWorkspaceDefaultsLookup), a derived runtime
// whose base is missing (a definite error that is not the lookup sentinel), a
// stored ref entry (an error), and a derived runtime whose merged base-then-
// derived defaults compose ahead of the client plan.
//
// spec: 14.1 (WorkspacePlan Schema Versioning), 5.1 (Runtime)
func TestComposeWorkspaceDefaultsResolve_spec_14_1(t *testing.T) {
	ctx := context.Background()
	mem := runtimestore.NewMemory()
	for _, rt := range []runtimestore.Runtime{
		{Name: "base", Image: "x@sha256:a", WorkspaceDefaults: &runtimestore.WorkspaceDefaults{
			Files:         []runtimestore.WorkspaceFile{{Path: "base.txt", Content: "b"}},
			SetupCommands: []runtimestore.WorkspaceSetupCommand{{Cmd: "echo base", TimeoutSeconds: 10}},
		}},
		{Name: "derived", BaseRuntime: "base", WorkspaceDefaults: &runtimestore.WorkspaceDefaults{
			Files:         []runtimestore.WorkspaceFile{{Path: "derived.txt", Content: "d"}},
			SetupCommands: []runtimestore.WorkspaceSetupCommand{{Cmd: "echo derived"}},
		}},
		{Name: "orphan", BaseRuntime: "gone"},
		{Name: "plain", Image: "x@sha256:a"},
		{Name: "stale-ref", Image: "x@sha256:a", WorkspaceDefaults: &runtimestore.WorkspaceDefaults{
			Files: []runtimestore.WorkspaceFile{{Path: "big.bin", Ref: "lenny-blob://acme/x/y/z"}},
		}},
	} {
		if err := mem.Create(ctx, rt); err != nil {
			t.Fatalf("seed %s: %v", rt.Name, err)
		}
	}
	transient := errors.New("connection reset")
	client := clientPlan(t, `{"schemaVersion":1,"sources":[{"type":"inlineFile","path":"c.txt","content":"c"}]}`)

	cases := []struct {
		name        string
		store       runtimestore.Store
		ref         string
		wantChanged bool
		wantLookup  bool
		wantErr     bool
		wantPaths   []string
	}{
		{name: "nil-store", store: nil, ref: "base", wantPaths: []string{"inlineFile:c.txt"}},
		{name: "empty-ref", store: mem, ref: "", wantPaths: []string{"inlineFile:c.txt"}},
		{name: "not-found", store: mem, ref: "absent", wantPaths: []string{"inlineFile:c.txt"}},
		{name: "no-defaults", store: mem, ref: "plain", wantPaths: []string{"inlineFile:c.txt"}},
		{
			name: "transient-runtime", ref: "derived", wantErr: true, wantLookup: true,
			store: failingGetStore{Store: mem, fails: map[string]error{"derived": transient}},
		},
		{
			name: "transient-base", ref: "derived", wantErr: true, wantLookup: true,
			store: failingGetStore{Store: mem, fails: map[string]error{"base": transient}},
		},
		{name: "missing-base", store: mem, ref: "orphan", wantErr: true},
		{name: "stored-ref", store: mem, ref: "stale-ref", wantErr: true},
		{
			name: "derived-merge", store: mem, ref: "derived", wantChanged: true,
			wantPaths: []string{"inlineFile:base.txt", "inlineFile:derived.txt", "inlineFile:c.txt"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed, err := runtimestore.ComposeWorkspaceDefaults(ctx, tc.store, tc.ref, client)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil {
				if errors.Is(err, runtimestore.ErrWorkspaceDefaultsLookup) != tc.wantLookup {
					t.Errorf("errors.Is(err, ErrWorkspaceDefaultsLookup) = %v, want %v (err=%v)", !tc.wantLookup, tc.wantLookup, err)
				}
				if tc.wantLookup && !errors.Is(err, transient) {
					t.Errorf("err = %v, want it to wrap the store error", err)
				}
				if tc.name == "missing-base" && !errors.Is(err, runtimestore.ErrNotFound) {
					t.Errorf("err = %v, want the missing-base ErrNotFound", err)
				}
				return
			}
			if changed != tc.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tc.wantChanged)
			}
			if !equalStrings(sourcePaths(got), tc.wantPaths) {
				t.Errorf("sources = %v, want %v", sourcePaths(got), tc.wantPaths)
			}
			if tc.name == "derived-merge" {
				want := []workspaceplan.SetupCommand{{Cmd: "echo base", TimeoutSeconds: 10}, {Cmd: "echo derived"}}
				if !slices.Equal(got.SetupCommands, want) {
					t.Errorf("setupCommands = %+v, want %+v", got.SetupCommands, want)
				}
			}
		})
	}
}

// getCountingStore counts the reads of a wrapped Store.
type getCountingStore struct {
	runtimestore.Store
	gets int
}

func (c *getCountingStore) Get(ctx context.Context, name string) (runtimestore.Runtime, error) {
	c.gets++
	return c.Store.Get(ctx, name)
}

// TestResolveRegisteredReadsEachRowOnce_spec_14_1 pins the read count of the
// runtime read the setupCommandPolicy check and the workspaceDefaults
// composition share: a registered runtime is read once, and an unregistered
// one is admitted rather than refused.
//
// spec: 14.1 (WorkspacePlan Schema Versioning, Runtime workspace defaults
// rule 4), 5.1 (Derived Runtime Instantiation)
func TestResolveRegisteredReadsEachRowOnce_spec_14_1(t *testing.T) {
	ctx := context.Background()
	mem := runtimestore.NewMemory()
	if err := mem.Create(ctx, runtimestore.Runtime{Name: "base", Image: "x@sha256:a"}); err != nil {
		t.Fatal(err)
	}
	c := &getCountingStore{Store: mem}
	if _, registered, err := runtimestore.ResolveRegistered(ctx, c, "base"); err != nil || !registered {
		t.Fatalf("ResolveRegistered(base) = %v, %v", registered, err)
	}
	if c.gets != 1 {
		t.Errorf("reads = %d, want 1", c.gets)
	}
	if _, registered, err := runtimestore.ResolveRegistered(ctx, c, "absent"); err != nil || registered {
		t.Fatalf("ResolveRegistered(absent) = %v, %v, want false and nil", registered, err)
	}
}
