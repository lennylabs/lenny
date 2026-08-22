// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the accessor that names the
// session a pod-global surface may act as.
//
// One runtime process serves every slot on a pod, and a per-session
// Runtime.Close deletes a bookkeeping entry and returns without ending
// that session's code inside the process. The accessor therefore counts
// the sessions the process has been given rather than the entries the slot
// registry holds, and it is empty whenever another session's code may
// still be resident. The pod-global surfaces that read it are the
// intra-pod MCP providers, the direct-mode token fold, and the
// control-event session stamp, so a session named wrongly here dispatches
// one user's tool call under another user's principal and folds one
// session's tokens into another's budget.
//
// The window this case drives is the one only concurrency reaches: two
// co-tenants ending at once, one release returned from Runtime.Close and
// the other not, and an incoming session admitted in between. The registry
// holds exactly one entry at that instant, which is what a predicate
// reading the registry, in any form, names.
//
// spec: §4.7 (session start and teardown), §9.1 (platform tool surface
// identity), §10.1 (intra-pod surfaces outliving a session).
package tier7a_load_local_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: 4.7 (teardown order), 9.1 (the surface names one session or
// none), 10.1 (a shared runtime process outliving a session)
// diagnosis: the accessor named a session while another session's code was
// still resident in the pod's shared runtime process. Every pod-global
// surface reads it, so a wrong answer here dispatches an intra-pod
// tools/call under a principal that is not the caller's, charges a
// departing session's token fold to a co-tenant's §11.2 budget, and stamps
// a control event with a session that did not raise it. A predicate keyed
// on the slot registry produces exactly this failure, because the registry
// is down to the incoming session's single entry at the moment it is
// admitted.
func TestSoleSessionIsEmptyAcrossAConcurrentRelease_spec_9_1(t *testing.T) {
	rt := newGatedRuntime()
	base := t.TempDir()
	s := adapter.New("test")
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")
	s.CredentialsDir = filepath.Join(base, "run", "lenny")
	s.ManifestDir = t.TempDir()
	s.Runtime = rt

	ctx := context.Background()
	startDrainSession(t, s, "alice")
	startDrainSession(t, s, "bob")
	if got := s.SoleSessionID(); got != "" {
		t.Fatalf("SoleSessionID on a pod given two sessions = %q, want empty", got)
	}

	// Both co-tenants end at once. bob's close parks, so the pod reaches
	// the state where one release has returned from Runtime.Close and the
	// other has not.
	bobClosing, releaseBob := rt.gate("bob", true)
	bobDone := make(chan struct{})
	go func() {
		defer close(bobDone)
		_, _ = s.Shutdown(ctx, &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "bob"},
		})
	}()
	waitClosed(t, bobClosing, "bob's runtime close to begin")

	aliceDone := make(chan struct{})
	go func() {
		defer close(aliceDone)
		_, _ = s.Shutdown(ctx, &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "alice"},
		})
	}()
	waitClosed(t, aliceDone, "alice's Shutdown to return")

	// The registry now holds nothing, and bob's code is still resident.
	if got := s.SoleSessionID(); got != "" {
		t.Errorf("SoleSessionID with an outstanding close = %q, want empty", got)
	}

	// The incoming session is admitted in that window. The registry holds
	// its one entry, and bob's close has still not returned.
	startDrainSession(t, s, "carol")
	if got := s.SoleSessionID(); got != "" {
		t.Errorf("SoleSessionID after a start admitted with an outstanding close = %q, want empty; "+
			"the departing session's code may still be resident in the shared runtime process", got)
	}

	close(releaseBob)
	waitClosed(t, bobDone, "bob's Shutdown to return")
	// The generation is not reset by that return, because carol is still
	// resident in the same process, so the accessor stays empty rather than
	// falling back to the survivor.
	if got := s.SoleSessionID(); got != "" {
		t.Errorf("SoleSessionID after the outstanding close returned = %q, want empty", got)
	}

	// Once every session the process was given has closed, the next start
	// is on a process serving nobody else and is named.
	if _, err := s.Shutdown(ctx, &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: "carol"},
	}); err != nil {
		t.Fatalf("shut carol down: %v", err)
	}
	if got := s.SoleSessionID(); got != "" {
		t.Errorf("SoleSessionID on an idle pod = %q, want empty", got)
	}
	startDrainSession(t, s, "dave")
	if got := s.SoleSessionID(); got != "dave" {
		t.Errorf("SoleSessionID after a fresh sole start = %q, want dave", got)
	}
	t.Cleanup(func() {
		_, _ = s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
			SessionId: &adapterv1.SessionId{Value: "dave"},
		})
	})
}
