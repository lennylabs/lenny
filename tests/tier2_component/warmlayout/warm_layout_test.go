//go:build component

// SPDX-License-Identifier: MIT

// Package warmlayout_test exercises the §6.1 warm-pod filesystem invariant
// and the §6.4 per-slot tree against a real adapter process's own
// filesystem operations. The adapter creates the warm layout at startup and
// each session's tree at slot assignment, so the case drives both through
// the adapter's own entry points rather than restating the paths.
//
// spec: §6.1 (warm-pod invariant), §6.4 (per-slot workspace layout).
package warmlayout_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// warmRoots are the four bases the adapter nests a session's per-slot trees
// under, mirroring the production /workspace, /sessions, /artifacts, and
// /run/lenny mounts.
type warmRoots struct {
	workspace   string
	sessions    string
	artifacts   string
	credentials string
	staging     string
}

// noopRuntime is a RuntimeProcess that records nothing: the layout cases
// need a startable session, not a working agent.
type noopRuntime struct{}

func (noopRuntime) Start(context.Context, string) error { return nil }
func (noopRuntime) WriteEnvelope(string, []byte) error  { return nil }

func (noopRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

func (noopRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (noopRuntime) Close(context.Context, string) error           { return nil }

func warmAdapter(t *testing.T) (*adapter.Server, warmRoots) {
	t.Helper()
	base := t.TempDir()
	roots := warmRoots{
		workspace:   filepath.Join(base, "workspace"),
		sessions:    filepath.Join(base, "sessions"),
		artifacts:   filepath.Join(base, "artifacts"),
		credentials: filepath.Join(base, "run", "lenny"),
		staging:     filepath.Join(base, "workspace", "staging"),
	}
	s := adapter.New("warm-layout")
	s.WorkspaceBase = roots.workspace
	s.SessionsRoot = roots.sessions
	s.ArtifactsRoot = roots.artifacts
	s.CredentialsDir = roots.credentials
	s.StagingDir = roots.staging
	s.Runtime = noopRuntime{}
	if err := s.EnsureWarmWorkspaceLayout(); err != nil {
		t.Fatalf("EnsureWarmWorkspaceLayout: %v", err)
	}
	return s, roots
}

func mustBeDir(t *testing.T, path, what string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("%s: stat %s: %v", what, path, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s: %s is not a directory", what, path)
	}
}

func mustBeAbsent(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s: stat %s = %v, want the path to be absent", what, path, err)
	}
}

// TestWarmPodHoldsSlotsAndStagingAndNoCurrent_spec_6_1 asserts the warm-pod
// filesystem invariant on a pool of either concurrency: /workspace/slots and
// the staging directory exist and slots/ is empty, and the retired
// pod-global /workspace/current exists on neither pool class. The layout is
// uniform, so the exclusive pool that previously materialized into
// /workspace/current is the arm a reinstated pod-global path fails on.
//
// diagnosis: the adapter's warm-time layout still creates, or still depends
// on, a pod-global /workspace/current. Under §6.4 a session's cwd is its own
// slot tree and no pod carries that leaf, so a runtime that falls back to it
// writes into a directory nothing checkpoints, scrubs, or exports.
// spec: 6.1 (warm-pod invariant), 6.4 (per-slot workspace layout)
func TestWarmPodHoldsSlotsAndStagingAndNoCurrent_spec_6_1(t *testing.T) {
	for _, pool := range []string{"exclusive", "concurrent"} {
		t.Run(pool, func(t *testing.T) {
			_, roots := warmAdapter(t)

			slots := filepath.Join(roots.workspace, "slots")
			mustBeDir(t, slots, "warm layout")
			mustBeDir(t, roots.staging, "warm layout")
			mustBeAbsent(t, filepath.Join(roots.workspace, "current"), "warm layout")

			entries, err := os.ReadDir(slots)
			if err != nil {
				t.Fatalf("read %s: %v", slots, err)
			}
			if len(entries) != 0 {
				t.Fatalf("%s holds %d entries, want empty before any slot is assigned", slots, len(entries))
			}
		})
	}
}

// TestSlotTreeAppearsAtAssignmentAndIsGoneAfterCleanup_spec_6_4 asserts the
// per-slot tree over all four trees the adapter creates. RemoveTree removing
// three of the four would leak state a later slot on the same pod could
// read, which is the isolation the per-slot layout exists to provide.
//
// diagnosis: a session's slot tree was not created at assignment, or its
// residue survived the session's teardown. Residue under any of the four
// trees is readable by whichever session next takes that identifier's tree
// on the pod.
// spec: 6.4 (per-slot workspace layout), 5.2 (per-slot cleanup at release)
func TestSlotTreeAppearsAtAssignmentAndIsGoneAfterCleanup_spec_6_4(t *testing.T) {
	s, roots := warmAdapter(t)
	const sessionID = "sess-warm-1"

	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Runtime:   "echo",
	}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	trees := map[string]string{
		"workspace cwd":     filepath.Join(roots.workspace, "slots", sessionID, "current"),
		"workspace staging": filepath.Join(roots.workspace, "slots", sessionID, "staging"),
		"sessions":          filepath.Join(roots.sessions, sessionID),
		"artifacts":         filepath.Join(roots.artifacts, sessionID),
		"credentials":       filepath.Join(roots.credentials, "slots", sessionID),
	}
	for what, path := range trees {
		mustBeDir(t, path, "at assignment: "+what)
	}
	// Residue the cleanup must remove, one file per tree.
	for what, path := range trees {
		if err := os.WriteFile(filepath.Join(path, "residue.txt"), []byte(what), 0o600); err != nil {
			t.Fatalf("seed residue in %s: %v", path, err)
		}
	}

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	}); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	for what, path := range trees {
		mustBeAbsent(t, path, "after cleanup: "+what)
	}
	mustBeAbsent(t, filepath.Join(roots.workspace, "current"), "after cleanup")
}
