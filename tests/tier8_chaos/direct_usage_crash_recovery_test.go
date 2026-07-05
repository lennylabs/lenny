// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §11.2 line 46 direct-mode gateway-crash-recovery
// MAX rule. It is the failure/recovery path proposal 0024 S14 names: a
// direct-mode session accumulates a known pod-reported cumulative token total
// C in its runtime adapter meter, a gateway replica crashes and its Redis quota
// counter is lost (restarted below C), and a reconnected replica pulls the
// pod-reported cumulative total (§4.7 ReportUsage cumulative=true) and
// reconstructs the direct-mode quota counter to
// MAX(redis_current, postgres_checkpoint, pod_reported_cumulative) so the
// recovered counter equals C rather than a stale checkpoint below C.
//
// The scenario drives the real §11.2 reconstruction end to end against a real
// Redis quotastore.Counter (the store the crash wipes): a real
// adapter.SessionUsageMeter (proposal 0024 S3) is the pod-reported source, a
// gateway-side quotacheckpoint.PodUsageReader pulls its cumulative total exactly
// as the S15 directUsageRecoveryReader does, and quotacheckpoint.Service.Reconcile
// (S15's fold site) applies the MAX rule to the live Redis key. This exercises
// two invariants a fake counter cannot: the real Redis restoreScript MAX write,
// and the S3 watermark advance that keeps the first post-recovery steady-state
// delta pull at zero so the counter is not double-counted to 2C.
//
// The unit-level fold coverage (the MAX arithmetic against a fake counter, the
// reader's cumulative pull, the per-pass pull-once invariant) lives in
// pkg/gateway/quota/quotacheckpoint/podusage_reconcile_test.go and
// cmd/lenny-gateway/direct_usage_recovery_test.go; this chaos test pins the
// same contract against real Redis with a real meter across a crash.

package tier8_chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/quota"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// recoveryClock is the fixed instant the crash-recovery reconcile pass runs at.
// A fixed clock keeps the Redis window key (the §12.4 hourly bucket) stable
// across the crash so the "stale checkpoint" and the reconstructed counter
// address the same key.
var recoveryClock = time.Date(2026, 6, 2, 14, 30, 0, 0, time.UTC)

// The direct-mode session under recovery.
const (
	recoveryTenant  = "acme"
	recoveryUser    = "alice@acme.com"
	recoverySession = "s-direct-crash"
)

// meterPodUsageReader is the tier-8 quotacheckpoint.PodUsageReader that pulls a
// direct-mode session's pod-reported cumulative token total from a real
// adapter.SessionUsageMeter (proposal 0024 S3) with cumulative=true, exactly as
// the production S15 directUsageRecoveryReader does over the §4.7 ReportUsage
// RPC. It caches the pull per reconcile pass (keyed on the pass `at`) because
// the meter's cumulative read advances the last-read watermark: a second pull
// in the same pass would return zero and the MAX fold would under-count. This
// mirrors directUsageRecoveryReader.aggregate; the tier-8 test uses it directly
// so the recovery path runs against the real meter rather than a stub.
type meterPodUsageReader struct {
	meter   *adapter.SessionUsageMeter
	tenant  string
	user    string
	session string
	period  quota.ResetPeriod

	cachedAt time.Time
	cache    int64
}

// pull reads the session's cumulative total once per pass and caches it. The
// real meter advances its watermark on the cumulative read, so calling pull a
// second time in the same pass would read zero; the pass-keyed cache guarantees
// each window fold site (UserWindow, TenantRollup, Snapshot) sees the same
// total.
func (r *meterPodUsageReader) pull(ctx context.Context, at time.Time) int64 {
	if !r.cachedAt.IsZero() && r.cachedAt.Equal(at) {
		return r.cache
	}
	u, _ := r.meter.Cumulative(ctx, r.session)
	r.cache = u.InputTokens + u.OutputTokens
	r.cachedAt = at
	return r.cache
}

func (r *meterPodUsageReader) UserWindow(ctx context.Context, tenantID, userID string, period quota.ResetPeriod, at time.Time) int64 {
	if tenantID != r.tenant || userID != r.user || period != r.period {
		return 0
	}
	return r.pull(ctx, at)
}

func (r *meterPodUsageReader) TenantRollup(ctx context.Context, tenantID string, period quota.ResetPeriod, at time.Time) int64 {
	if tenantID != r.tenant || period != r.period {
		return 0
	}
	return r.pull(ctx, at)
}

func (r *meterPodUsageReader) Snapshot(ctx context.Context, now time.Time) []quotacheckpoint.PodUsageSample {
	total := r.pull(ctx, now)
	if total <= 0 {
		return nil
	}
	return []quotacheckpoint.PodUsageSample{
		{TenantID: r.tenant, UserID: r.user, Period: r.period, Tokens: total},
		{TenantID: r.tenant, UserID: "", Period: r.period, Tokens: total},
	}
}

var _ quotacheckpoint.PodUsageReader = (*meterPodUsageReader)(nil)

// recoveryStore is an in-memory quotacheckpoint.Store holding the Postgres
// checkpoint rows the pre-crash replica persisted. The crash-recovery contract
// under test is the Redis-counter reconstruction, so the durable checkpoint is
// held in memory while the counter writes hit real Redis; this isolates the
// §11.2 line 46 MAX rule from Postgres RLS plumbing (covered in the tier-2
// store suites).
type recoveryStore struct {
	rows []quotacheckpoint.Row
}

func (s *recoveryStore) Write(_ context.Context, rows []quotacheckpoint.Row) error {
	s.rows = append(s.rows, rows...)
	return nil
}

func (s *recoveryStore) ListActive(_ context.Context) ([]quotacheckpoint.Row, error) {
	return s.rows, nil
}

func (s *recoveryStore) ListByTenant(_ context.Context, tenantID string) ([]quotacheckpoint.Row, error) {
	var out []quotacheckpoint.Row
	for _, r := range s.rows {
		if r.TenantID == tenantID {
			out = append(out, r)
		}
	}
	return out, nil
}
func (s *recoveryStore) DeleteByUser(context.Context, string, string) (int, error) { return 0, nil }
func (s *recoveryStore) DeleteByTenant(context.Context, string) (int, error)       { return 0, nil }

var _ quotacheckpoint.Store = (*recoveryStore)(nil)

// spec: 11.2 (line 46 gateway-crash-recovery MAX rule; pod-reported cumulative
// total re-reported on reconnection), 4.7 (ReportUsage cumulative read; the
// watermark advance that keeps the post-recovery delta at zero), 12 (Redis
// counter reconstruction after a replica crash).
//
// diagnosis: a failure means the §11.2 line 46 direct-mode crash-recovery MAX
// rule did not hold end to end against real Redis. Either (a) the reconnected
// replica silently UNDER-COUNTED — it reconstructed the direct-mode counter to
// a stale checkpoint below the pod-reported cumulative total C because the
// pod-reported source was dropped or the cumulative pull returned a delta
// instead of the running total, un-recovering the exact under-count protection
// §11.2 line 46 exists to provide; or (b) it DOUBLE-COUNTED — the first
// post-recovery steady-state delta pull re-added the already-recovered tokens
// for a session total of 2C, because the meter's cumulative read did not advance
// its watermark (the S3 watermark-advance invariant regressed to a reset).
func TestDirectModeGatewayCrashRecoveryReconstructsMaxWithoutDoubleCount(t *testing.T) {
	ctx := context.Background()

	// A real Redis quota counter: the store the gateway crash wipes.
	rd := containers.StartRedis(t, containers.RedisOptions{})
	counter := quotastore.New(rd.Client)

	// The pod-reported source: a real adapter meter accumulating a known
	// cumulative total for the direct-mode session. Pre-crash the session ran
	// checkpointTokens worth of work that the last Postgres checkpoint captured,
	// then postCheckpointTokens more the crash lost from Redis, so the meter's
	// cumulative total is C = checkpointTokens + postCheckpointTokens.
	const (
		checkpointTokens     = 500  // durable Postgres checkpoint value D (< C)
		postCheckpointTokens = 1500 // usage after the checkpoint, lost by the crash
		cumulativeC          = checkpointTokens + postCheckpointTokens
		staleRedisAfterCrash = 200 // Redis came back well below C
	)
	meter := adapter.NewSessionUsageMeter(func() time.Time { return recoveryClock })

	// Pre-crash: the session accumulated the checkpointed tokens, and the
	// pre-crash replica took a steady-state read (advancing the watermark) that
	// fed the durable Postgres checkpoint D. This is the real ordering: the
	// meter's watermark sits at D when the replica crashes.
	meter.Add(recoverySession, checkpointTokens, 0)
	if u, _ := meter.Usage(ctx, recoverySession); u.InputTokens != checkpointTokens {
		t.Fatalf("precondition: pre-crash steady-state read = %d, want %d", u.InputTokens, checkpointTokens)
	}
	// After the checkpoint, more work accrues that the crash drops from Redis.
	meter.Add(recoverySession, postCheckpointTokens, 0)

	period := quota.ResetHourly
	label, err := quotastore.WindowLabel(period, recoveryClock)
	if err != nil {
		t.Fatalf("WindowLabel: %v", err)
	}

	// The durable Postgres checkpoint the pre-crash replica persisted: value D,
	// below the true cumulative C. Restoring only from this stale checkpoint is
	// the silent under-count §11.2 line 46 prevents.
	store := &recoveryStore{}
	_ = store.Write(ctx, []quotacheckpoint.Row{
		{TenantID: recoveryTenant, Scope: quotacheckpoint.ScopeUser, SubjectID: recoveryUser, Period: string(period), WindowLabel: label, TokenTotal: checkpointTokens},
		{TenantID: recoveryTenant, Scope: quotacheckpoint.ScopeTenant, Period: string(period), WindowLabel: label, TokenTotal: checkpointTokens},
	})

	// Inject the crash: Redis restarts and the counter comes back below C (it
	// lost the post-checkpoint usage). Seed the live Redis window to the stale
	// value so the reconcile has a real redis_current input below C.
	if _, err := counter.Add(ctx, recoveryTenant, recoveryUser, period, recoveryClock, staleRedisAfterCrash); err != nil {
		t.Fatalf("seed stale post-crash Redis user counter: %v", err)
	}
	// The tenant rollup came back empty (0), a distinct stale-below-C input.

	// The reconnected replica's pod-usage source, pulling the cumulative total
	// from the real meter with cumulative=true.
	podUsage := &meterPodUsageReader{
		meter: meter, tenant: recoveryTenant, user: recoveryUser,
		session: recoverySession, period: period,
	}

	// The reconnected replica runs the §11.2 line 46 reconcile against real
	// Redis, folding the pod-reported cumulative total into the MAX.
	svc := &quotacheckpoint.Service{
		Store:    store,
		Reader:   &counterWindowReader{counter},
		Restorer: counter,
		PodUsage: podUsage,
		Now:      func() time.Time { return recoveryClock },
	}
	if _, err := svc.Reconcile(ctx, quotacheckpoint.ReconcileScope{AllTenants: true}); err != nil {
		t.Fatalf("crash-recovery Reconcile: %v", err)
	}

	// (a) The recovered per-user counter equals C, the pod-reported cumulative
	// total — MAX(200 stale redis, 500 checkpoint, 2000 pod) = 2000. Not the
	// stale checkpoint (500) and not the stale Redis value (200).
	userNow, err := counter.Usage(ctx, recoveryTenant, recoveryUser, period, recoveryClock)
	if err != nil {
		t.Fatalf("read recovered user counter: %v", err)
	}
	if userNow != cumulativeC {
		t.Fatalf("recovered user counter = %d, want %d (MAX of stale redis %d, checkpoint %d, pod-reported cumulative %d); "+
			"a value of %d means the pod-reported source was dropped and the replica silently under-counted",
			userNow, cumulativeC, staleRedisAfterCrash, checkpointTokens, cumulativeC, checkpointTokens)
	}
	// The tenant rollup is reconstructed to C too (its stale Redis input was 0).
	rollupNow, err := counter.TenantRollupUsage(ctx, recoveryTenant, period, recoveryClock)
	if err != nil {
		t.Fatalf("read recovered tenant rollup: %v", err)
	}
	if rollupNow != cumulativeC {
		t.Fatalf("recovered tenant rollup = %d, want %d (pod-reported cumulative)", rollupNow, cumulativeC)
	}

	// (b) The first post-recovery steady-state delta pull returns zero: the
	// cumulative recovery read advanced the meter's watermark to C, so no new
	// tokens are outstanding. Recording that delta must leave the counter at C,
	// not double-count it to 2C.
	delta, _ := meter.Usage(ctx, recoverySession)
	if got := delta.InputTokens + delta.OutputTokens; got != 0 {
		t.Fatalf("first post-recovery steady-state delta = %d, want 0 (the cumulative recovery read must advance the watermark; "+
			"a non-zero delta re-adds the recovered tokens and double-counts to 2C)", got)
	}
	// Fold the (zero) delta into the counter as the steady-state loop would; the
	// counter must stay at C.
	if delta.InputTokens+delta.OutputTokens > 0 {
		if _, err := counter.Add(ctx, recoveryTenant, recoveryUser, period, recoveryClock, delta.InputTokens+delta.OutputTokens); err != nil {
			t.Fatalf("post-recovery delta Add: %v", err)
		}
	}
	finalUser, err := counter.Usage(ctx, recoveryTenant, recoveryUser, period, recoveryClock)
	if err != nil {
		t.Fatalf("read post-recovery user counter: %v", err)
	}
	if finalUser != cumulativeC {
		t.Fatalf("post-recovery user counter = %d, want %d (not %d); the recovered counter must equal C, not double-count to 2C",
			finalUser, cumulativeC, 2*cumulativeC)
	}
}

// counterWindowReader adapts a *quotastore.Counter to the
// quotacheckpoint.WindowReader the reconcile reads the live redis_current input
// from. The Counter already satisfies the read methods; the named type keeps the
// intent explicit at the Service wiring.
type counterWindowReader struct{ *quotastore.Counter }

var _ quotacheckpoint.WindowReader = (*counterWindowReader)(nil)
