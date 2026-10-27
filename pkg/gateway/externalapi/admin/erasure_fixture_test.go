// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// fixtureRunners holds the erasure runners each test built, keyed by the
// test's name, so the await helpers can stand in for the gateway leader's
// sweep and run the jobs the handlers record.
var fixtureRunners sync.Map // map[string][]*erasurejob.Runner

// fixtureRunnersMu serializes appends to one test's runner list.
var fixtureRunnersMu sync.Mutex

// newFixtureRunner builds an erasure runner whose session drain and
// legal-hold re-check are no-ops and whose completion receipt is emitted
// to audit as gdpr.erasure_completed, built by admin.ErasureCompletionReceipt
// and attributed to the job's persisted principal as the gateway's receipt
// sink attributes it. The runner is registered for driveRunners.
func newFixtureRunner(t *testing.T, jobs erasurejob.Store, orch *erasure.Orchestrator, audit *recordingAudit) *erasurejob.Runner {
	t.Helper()
	r := erasurejob.NewRunner(jobs, orch, erasureClock).
		WithMaxConcurrentJobs(4).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithSessionDrain(func(context.Context, string, string) error { return nil }).
		WithHoldCheck(func(context.Context, string, string) ([]erasurejob.Hold, error) { return nil, nil }).
		WithReleaseGate(func(context.Context, string, string) ([]string, error) { return nil, nil }).
		WithCompletionReceipt(func(ctx context.Context, j erasurejob.Job) error {
			if audit != nil {
				audit.EmitAdminEvent(ctx, admin.AuditEvent{
					Type:           "gdpr.erasure_completed",
					ActorSubject:   j.InitiatedBy,
					ActorTenantID:  j.TenantID,
					TargetResource: j.UserID,
					Detail:         admin.ErasureCompletionReceipt(j),
					At:             j.ReceiptAt,
				})
			}
			return nil
		})
	fixtureRunnersMu.Lock()
	defer fixtureRunnersMu.Unlock()
	prev, _ := fixtureRunners.Load(t.Name())
	list, _ := prev.([]*erasurejob.Runner)
	fixtureRunners.Store(t.Name(), append(list, r))
	t.Cleanup(func() { fixtureRunners.Delete(t.Name()) })
	return r
}

// driveRunners runs one RunPending on every runner the test built.
func driveRunners(t *testing.T) {
	t.Helper()
	v, ok := fixtureRunners.Load(t.Name())
	if !ok {
		return
	}
	for _, r := range v.([]*erasurejob.Runner) {
		if err := r.RunPending(context.Background()); err != nil {
			t.Fatalf("RunPending: %v", err)
		}
	}
}
