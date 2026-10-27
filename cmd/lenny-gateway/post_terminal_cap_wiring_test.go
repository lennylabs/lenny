// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"testing"
	"time"

	apisession "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/retentiongc"
)

// flagPostureWiring runs buildPersistenceStores in the memory posture with
// the given extra flags.
func flagPostureWiring(t *testing.T, args ...string) *gatewayWiring {
	t.Helper()
	savedCmdLine, savedArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = savedCmdLine, savedArgs })
	flag.CommandLine = flag.NewFlagSet(savedArgs[0], flag.ContinueOnError)
	os.Args = append([]string{savedArgs[0]}, args...)
	w := &gatewayWiring{f: parseFlags()}
	w.buildPersistenceStores()
	return w
}

// TestPostTerminalCapIsTheTerminalPipelineBudget_spec_7_1 pins the
// composition root's post-terminal cap: buildPersistenceStores sets it to
// the terminal pipeline budget of the seal window and the stream-failure
// report timeout in effect, and hands the same value to the memory and the
// Postgres session store constructions.
//
// spec: §7.1 (Post-terminal window), §7.1 (Terminal pipeline context)
func TestPostTerminalCapIsTheTerminalPipelineBudget_spec_7_1(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want time.Duration
	}{
		{"defaults", nil, sessionserver.TerminalPipelineBudgetFor(sessionserver.DefaultWorkspaceSealMaxDuration, sessionserver.DefaultStreamFailureReportTimeout)},
		{"configured", []string{"--workspace-seal-max-duration-seconds=100", "--stream-failure-report-timeout=10s"}, 110 * time.Second},
		{"non-positive seal falls back", []string{"--workspace-seal-max-duration-seconds=0"}, sessionserver.TerminalPipelineBudgetFor(sessionserver.DefaultWorkspaceSealMaxDuration, sessionserver.DefaultStreamFailureReportTimeout)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := flagPostureWiring(t, tc.args...)
			if w.postTerminalCap != tc.want {
				t.Fatalf("postTerminalCap = %v, want %v", w.postTerminalCap, tc.want)
			}
			mem, ok := w.sessions.(*memstore.Store)
			if !ok {
				t.Fatalf("memory posture session store is %T", w.sessions)
			}
			if mem.PostTerminalCap() != tc.want {
				t.Errorf("memstore cap = %v, want %v", mem.PostTerminalCap(), tc.want)
			}
			if got := w.newPGSessionStore(nil, nil).PostTerminalCap(); got != tc.want {
				t.Errorf("Postgres session store cap = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPostTerminalCapGatesErasureDeletionAndRetention_spec_12_8 pins the
// three readers of the cap: a terminal, unreleased row inside the budget
// blocks the erasure release gate, the tenant-deletion terminator, and a
// retention collector built from retentionGCOptions; once the release is
// recorded none of them is held. With the cap zero, the gate and the
// terminator return an error wrapping ErrReleaseCapUnset and the collector
// collects no row that names a pod.
//
// spec: §12.8 (Compliance Interfaces), §7.1 (Post-terminal window)
func TestPostTerminalCapGatesErasureDeletionAndRetention_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w := memoryPostureWiring(t)
	seedTenantUser(t, w, "acme", "alice@acme.com")
	w.sessionSrv = sessionserver.New(w.sessions, sessionserver.Options{DevTenantHeader: true, DefaultRetention: time.Second})
	t.Cleanup(w.sessionSrv.Close)
	now := clockinject.Now()
	if err := w.sessions.Create(ctx, sessionstore.Session{
		ID: "sess-sealing", TenantID: "acme", UserID: "alice@acme.com", State: apisession.StateCompleted,
		PodAssignment: "pod-a", TerminatedAt: now.Add(-time.Minute), RetentionExpiresAt: now.Add(-time.Second),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	term := w.newTenantSessionTerminator(&sleepyDrainTerminator{}, nil, time.Second)
	collect := func() int {
		arts := &countingArtifacts{}
		c := retentiongc.New(w.sessions, retentiongc.StaticTenants{"acme"}, []retentiongc.Artifact{arts.artifact()}, w.retentionGCOptions(time.Minute))
		n, err := c.Tick(ctx, clockinject.Now())
		if err != nil {
			t.Fatalf("Tick: %v", err)
		}
		return n
	}

	if ids, err := w.erasureReleaseGate()(ctx, "acme", "alice@acme.com"); err != nil || len(ids) != 1 {
		t.Errorf("erasure gate = (%v, %v), want the sealing session", ids, err)
	}
	if err := term.TerminateTenantSessions(ctx, "acme"); err == nil {
		t.Error("tenant terminator = nil, want the post-terminal wait")
	}
	if n := collect(); n != 0 {
		t.Errorf("retention collected %d rows inside the post-terminal window, want 0", n)
	}

	budget := w.postTerminalCap
	w.postTerminalCap = 0
	if _, err := w.erasureReleaseGate()(ctx, "acme", "alice@acme.com"); !errors.Is(err, admin.ErrReleaseCapUnset) {
		t.Errorf("erasure gate with no cap = %v, want ErrReleaseCapUnset", err)
	}
	if err := w.newTenantSessionTerminator(&sleepyDrainTerminator{}, nil, time.Second).TerminateTenantSessions(ctx, "acme"); !errors.Is(err, admin.ErrReleaseCapUnset) {
		t.Errorf("tenant terminator with no cap = %v, want ErrReleaseCapUnset", err)
	}
	zeroArts := &countingArtifacts{}
	zc := retentiongc.New(w.sessions, retentiongc.StaticTenants{"acme"}, []retentiongc.Artifact{zeroArts.artifact()}, w.retentionGCOptions(time.Minute))
	if n, err := zc.Tick(ctx, now.Add(24*time.Hour)); err != nil || n != 0 {
		t.Errorf("retention with no cap = (%d, %v), want nothing collected", n, err)
	}
	w.postTerminalCap = budget

	if err := sessionstore.RecordPodRelease(ctx, w.sessions, "acme", "sess-sealing", now); err != nil {
		t.Fatalf("record release: %v", err)
	}
	if ids, err := w.erasureReleaseGate()(ctx, "acme", "alice@acme.com"); err != nil || len(ids) != 0 {
		t.Errorf("erasure gate after the release = (%v, %v), want none", ids, err)
	}
	if err := term.TerminateTenantSessions(ctx, "acme"); err != nil {
		t.Errorf("tenant terminator after the release = %v, want nil", err)
	}
	if n := collect(); n != 1 {
		t.Errorf("retention after the release collected %d rows, want 1", n)
	}
}

// countingArtifacts is a retention artifact store that counts deletions.
type countingArtifacts struct{ n int }

func (c *countingArtifacts) artifact() retentiongc.Artifact {
	return retentiongc.Artifact{Name: "artifacts", Delete: func(context.Context, string, string) (int, error) {
		c.n++
		return 1, nil
	}}
}
