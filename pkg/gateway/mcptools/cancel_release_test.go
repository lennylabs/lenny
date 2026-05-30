// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// releaseRecordingExecutor records the §6.2 disposition the gateway records per
// session id at teardown so the cancel_child drain test (F-11.3.1) can assert
// each cancelled descendant's pod was released, not merely its row flipped.
type releaseRecordingExecutor struct {
	mu       sync.Mutex
	released map[string]executor.Disposition
}

func (e *releaseRecordingExecutor) Send(context.Context, string, []executor.Message) ([]executor.OutputPart, error) {
	return nil, nil
}

func (e *releaseRecordingExecutor) Close(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.released[id] = ""
	return nil
}

func (e *releaseRecordingExecutor) Release(_ context.Context, id string, d executor.Disposition) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.released[id] = d
	return nil
}

func (e *releaseRecordingExecutor) dispositionOf(id string) (executor.Disposition, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.released[id]
	return d, ok
}

// TestCancelChildDrainsCancelledRuntimes_spec_11_3_1 asserts that
// lenny/cancel_child drains every cancelled session's pod via the executor,
// recording the §6.2 cancelled disposition. Before F-11.3.1 the handler only
// flipped each descendant row to cancelled and left the child agents running,
// holding tokens and charging credential leases until the watchdog's
// maxSessionAge clock fired. spec: §8.5; §11.3 line 236; §11.4 line 258.
func TestCancelChildDrainsCancelledRuntimes_spec_11_3_1(t *testing.T) {
	store := memstore.New()
	exec := &releaseRecordingExecutor{released: map[string]executor.Disposition{}}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Executor: exec,
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})

	// sess_root → sess_a (target) → sess_a1 (running), all cancel_all.
	mkSession(t, store, "sess_root", session.StateRunning, "")
	mkSession(t, store, "sess_a", session.StateRunning, "sess_root")
	mkSession(t, store, "sess_a1", session.StateRunning, "sess_a")

	resp := call(t, srv.Handler(), "lenny/cancel_child",
		`{"parentSessionId":"sess_root","childSessionId":"sess_a"}`)
	_ = resultText(t, resp)

	// Both cancelled sessions must be drained with the cancelled disposition.
	for _, id := range []string{"sess_a", "sess_a1"} {
		d, ok := exec.dispositionOf(id)
		if !ok || d != executor.DispositionCancelled {
			t.Errorf("F-11.3.1: session %s released disposition = %q (present=%v), want cancelled", id, d, ok)
		}
	}
	// The calling parent is not part of the cancelled set, so it is not drained.
	if _, ok := exec.dispositionOf("sess_root"); ok {
		t.Errorf("F-11.3.1: calling parent sess_root must not be drained by cancel_child")
	}
}
