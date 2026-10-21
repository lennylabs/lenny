// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// defaultUsers are the (tenant, subject) pairs the runner and sampler
// fixtures erase.
var defaultUsers = [][2]string{
	{"acme", "alice"},
	{"acme", "bob"},
	{"acme", "alice@acme"},
	{"regulated", "alice"},
	{"globex", "alice"},
}

// newJobStoreUsers returns a Memory job store whose restriction writer is a
// userstore.Memory seeded with defaultUsers, so a Start sets the flag on a
// real users row, and returns that user store.
func newJobStoreUsers(t testing.TB) (*erasurejob.Memory, *userstore.Memory) {
	t.Helper()
	users := userstore.NewMemory()
	for _, u := range defaultUsers {
		if err := users.Create(context.Background(), userstore.User{TenantID: u[0], Subject: u[1]}); err != nil {
			t.Fatalf("seed user %s/%s: %v", u[0], u[1], err)
		}
	}
	return erasurejob.NewMemory(erasurejob.WithRestrictionWriter(users)), users
}

// newJobStore is newJobStoreUsers without the user store.
func newJobStore(t testing.TB) *erasurejob.Memory {
	t.Helper()
	jobs, _ := newJobStoreUsers(t)
	return jobs
}

// newRunner builds a Runner whose session drain and legal-hold re-check
// are no-ops and whose completion receipt is accepted, so a fixture that
// drives a job past initiated reaches completion. A test of a missing or
// failing hook overrides the hook on the returned runner.
func newRunner(jobs erasurejob.Store, orch *erasure.Orchestrator, clock func() time.Time) *erasurejob.Runner {
	return erasurejob.NewRunner(jobs, orch, clock).
		WithSessionDrain(func(context.Context, string, string) error { return nil }).
		WithHoldCheck(func(context.Context, string, string) ([]erasurejob.Hold, error) { return nil, nil }).
		WithCompletionReceipt(func(context.Context, erasurejob.Job) error { return nil }).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// runnerStart starts a job for (tenantID, userID) with no initiator and no
// override and returns its id.
func runnerStart(ctx context.Context, r *erasurejob.Runner, tenantID, userID string) (string, error) {
	j, err := r.Start(ctx, tenantID, userID, "", nil)
	return j.ID, err
}
