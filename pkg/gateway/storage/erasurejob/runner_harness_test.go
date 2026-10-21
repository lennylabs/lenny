// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// This file holds the recording harness the runner phase, completion, and
// sweep tests share: every hook and store call the runner makes appends to
// one ordered event log, so a test asserts both how often a step ran and in
// what order.

// countingBilling wraps the in-memory billing ledger and records each
// PseudonymizeUser and CountUser call. failVerify makes CountUser report a
// surviving event, which fails the §12.8 post-pseudonymization check.
type countingBilling struct {
	*billingstore.Memory
	h          *runHarness
	failVerify bool
}

func (c *countingBilling) PseudonymizeUser(ctx context.Context, tenantID, userID string, salt []byte) (int, error) {
	c.h.record("pseudonymize:" + tenantID)
	if c.h.pseudoGate != nil {
		c.h.pseudoGate(ctx, tenantID)
	}
	return c.Memory.PseudonymizeUser(ctx, tenantID, userID, salt)
}

func (c *countingBilling) CountUser(ctx context.Context, tenantID, userID string) (int, error) {
	c.h.record("verify:" + tenantID)
	c.h.mu.Lock()
	fail := c.failVerify
	c.h.mu.Unlock()
	if fail {
		return 1, nil
	}
	return c.Memory.CountUser(ctx, tenantID, userID)
}

// runHarness wires a Runner over a Memory job store, a userstore.Memory
// restriction writer, a tenant store, a counting billing ledger, and
// recording hooks.
type runHarness struct {
	t       *testing.T
	jobs    *erasurejob.Memory
	users   *userstore.Memory
	tenants *tenantstore.Memory
	billing *countingBilling
	runner  *erasurejob.Runner
	clock   func() time.Time

	mu       sync.Mutex
	events   []string
	receipts [][]byte
	failures []string

	// Behavior knobs the tests set before running.
	drainErr     error
	holdErr      error
	holds        []erasurejob.Hold
	deleteErr    error
	deleteCount  int
	receiptErr   error
	preflightErr error
	// Optional blocking points. Each is called with the run's context.
	drainGate   func(ctx context.Context, tenantID string)
	deleteGate  func(ctx context.Context, tenantID string)
	receiptGate func(ctx context.Context, j erasurejob.Job)
	pseudoGate  func(ctx context.Context, tenantID string)
}

// harnessTenants are the tenants the harness seeds; each has users alice
// and bob.
var harnessTenants = []string{"acme", "globex", "initech", "umbrella"}

func newRunHarness(t *testing.T) *runHarness {
	t.Helper()
	h := &runHarness{t: t, deleteCount: 1}
	h.users = userstore.NewMemory()
	h.tenants = tenantstore.NewMemory()
	for _, tn := range harnessTenants {
		if err := h.tenants.Create(context.Background(), tenantstore.Tenant{ID: tn}); err != nil {
			t.Fatalf("seed tenant: %v", err)
		}
		for _, u := range []string{"alice", "bob"} {
			if err := h.users.Create(context.Background(), userstore.User{TenantID: tn, Subject: u}); err != nil {
				t.Fatalf("seed user: %v", err)
			}
		}
	}
	h.jobs = erasurejob.NewMemory(erasurejob.WithRestrictionWriter(h.users))
	h.billing = &countingBilling{Memory: billingstore.NewMemory(), h: h}
	h.clock = func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }
	h.runner = h.newRunner()
	return h
}

// newRunner builds a fresh Runner over the harness's stores and hooks, so
// a test can run two executors against one job store.
func (h *runHarness) newRunner() *erasurejob.Runner {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{{
		Name: "sessions",
		DeleteByUser: func(ctx context.Context, tenantID, _ string) (int, error) {
			h.record("delete:" + tenantID)
			if h.deleteGate != nil {
				h.deleteGate(ctx, tenantID)
			}
			h.mu.Lock()
			n, err := h.deleteCount, h.deleteErr
			h.mu.Unlock()
			return n, err
		},
	}}})
	return erasurejob.NewRunner(h.jobs, orch, h.clock).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithBilling(erasurejob.NewBillingEraser(h.billing, h.tenants)).
		WithMemoryPreflight(func(context.Context) error {
			h.record("preflight")
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.preflightErr
		}).
		WithSessionDrain(func(ctx context.Context, tenantID, _ string) error {
			h.record("drain:" + tenantID)
			if h.drainGate != nil {
				h.drainGate(ctx, tenantID)
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.drainErr
		}).
		WithHoldCheck(func(context.Context, string, string) ([]erasurejob.Hold, error) {
			h.record("hold")
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.holds, h.holdErr
		}).
		WithCompletionReceipt(func(ctx context.Context, j erasurejob.Job) error {
			h.record("receipt:" + j.TenantID)
			if h.receiptGate != nil {
				h.receiptGate(ctx, j)
			}
			b, err := json.Marshal(j)
			if err != nil {
				return err
			}
			h.mu.Lock()
			h.receipts = append(h.receipts, b)
			rerr := h.receiptErr
			h.mu.Unlock()
			return rerr
		}).
		WithFailureObserver(func(tenantID, phase string) {
			h.mu.Lock()
			h.failures = append(h.failures, tenantID+":"+phase)
			h.mu.Unlock()
		})
}

func (h *runHarness) record(ev string) {
	h.mu.Lock()
	h.events = append(h.events, ev)
	h.mu.Unlock()
}

// count returns how many recorded events equal ev.
func (h *runHarness) count(ev string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, e := range h.events {
		if e == ev {
			n++
		}
	}
	return n
}

// order returns the recorded events, in order.
func (h *runHarness) order() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.events...)
}

// failureCount returns the failure-observer calls so far.
func (h *runHarness) failureCount() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.failures...)
}

// receiptList returns the receipts written so far.
func (h *runHarness) receiptList() [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([][]byte(nil), h.receipts...)
}

// set runs fn under the harness lock, for changing a knob mid-test.
func (h *runHarness) set(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn()
}

// start records a job for tenant/user and returns it.
func (h *runHarness) start(tenantID, userID string, override map[string]any) erasurejob.Job {
	h.t.Helper()
	j, err := h.runner.Start(context.Background(), tenantID, userID, "carol", override)
	if err != nil {
		h.t.Fatalf("Start %s/%s: %v", tenantID, userID, err)
	}
	return j
}

// job reads a job.
func (h *runHarness) job(id string) erasurejob.Job {
	h.t.Helper()
	j, err := h.jobs.Get(context.Background(), erasurejob.AllTenants, id)
	if err != nil {
		h.t.Fatalf("Get %s: %v", id, err)
	}
	return j
}

// restricted reports the user's processing_restricted flag and the job it
// names.
func (h *runHarness) restricted(tenantID, userID string) (bool, string) {
	h.t.Helper()
	u, err := h.users.Get(context.Background(), tenantID, userID)
	if err != nil {
		h.t.Fatalf("Get user: %v", err)
	}
	return u.ProcessingRestricted, u.ErasureJobID
}

// moveTo rewrites a job into phase p with the transition logged, as a
// crash after that phase's advance leaves it. extra, when non-nil, is
// applied in the same write.
func (h *runHarness) moveTo(id string, p erasurejob.Phase, extra func(*erasurejob.Job)) {
	h.t.Helper()
	if _, err := h.jobs.Update(context.Background(), id, func(j *erasurejob.Job) error {
		j.Phase = p
		j.PhaseLog = append(j.PhaseLog, erasurejob.PhaseTransition{Phase: p, At: h.clock()})
		if extra != nil {
			extra(j)
		}
		return nil
	}); err != nil {
		h.t.Fatalf("move %s to %s: %v", id, p, err)
	}
}

// phases returns the phases of a job's PhaseLog.
func phases(j erasurejob.Job) []erasurejob.Phase {
	out := make([]erasurejob.Phase, 0, len(j.PhaseLog))
	for _, t := range j.PhaseLog {
		out = append(out, t.Phase)
	}
	return out
}

// indexOf returns the position of the first event equal to ev, or -1.
func indexOf(events []string, ev string) int {
	for i, e := range events {
		if e == ev {
			return i
		}
	}
	return -1
}

// errBoom is a generic injected failure.
var errBoom = errors.New("boom")
