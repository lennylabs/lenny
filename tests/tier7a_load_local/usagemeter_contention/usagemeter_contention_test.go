// SPDX-License-Identifier: MIT

//go:build load_local

// Package usagemeter_contention exercises the §11.2 direct-mode adapter
// SessionUsageMeter under concurrent llm_request_completed folds and
// concurrent gateway ReportUsage reads, at the tier-7a load_local flake
// budget with the race detector enabled.
//
// The meter is the direct-mode token accumulator (proposal 0024 S3):
// every §4.7 llm_request_completed frame folds its (input, output) token
// counts into a per-session running cumulative total under a mutex, and a
// gateway ReportUsage pull reads either the incremental delta since the
// last read (steady state) or the running cumulative total (the §11.2
// crash-recovery MAX rule). The mutex protects the cumulative totals and
// the per-session last-read watermark; a lost update, a torn read, or a
// watermark that moves backward would silently under- or over-count a
// tenant's usage, so the accounting must be exact and -race clean under
// contention.
//
// The in-package smoke test pkg/adapter/usage_test.go
// TestSessionUsageMeterConcurrentFoldRaceSmoke_spec_11_2 defers the
// flake-budget assertion to this tier; this package supplies it. Run to a
// stress budget with:
//
//	lenny-test stress --test TestSessionUsageMeterConcurrentAccountingIsExact_spec_11_2 --runs 50
//
// so a data race or a lost update surfaces across many runs rather than
// once.
//
// TESTING.md §12.7.a regression scenarios.
package usagemeter_contention

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// fixedClock returns a frozen clock so the meter's wall-clock accounting is
// deterministic under contention and a WallClockMS read never races real
// time. The tests assert token totals, not wall-clock, so a frozen clock is
// sufficient and keeps the token assertion exact.
func fixedClock() func() time.Time {
	t0 := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t0 }
}

// spec: §11.2 (direct-mode usage), §4.7 (ReportUsage incremental contract).
// diagnosis: a failure means the adapter SessionUsageMeter lost or double-counted
// a direct-mode token fold under concurrent llm_request_completed accumulation and
// gateway ReportUsage delta reads, or its per-session last-read watermark raced,
// so a tenant's billed/enforced usage diverged from the tokens the runtime reported
// (proposal 0024 S3 mutex-guarded accumulator not race-safe under the flake budget).
func TestSessionUsageMeterConcurrentAccountingIsExact_spec_11_2(t *testing.T) {
	t.Parallel()

	const (
		sessions     = 8
		addersPerSid = 8
		perAdder     = 500
		readers      = 4
		perFrameIn   = 1
		perFrameOut  = 2
	)

	m := adapter.NewSessionUsageMeter(fixedClock())

	sids := make([]string, sessions)
	for i := range sids {
		sids[i] = fmt.Sprintf("sess-%02d", i)
	}

	// drained accumulates every delta the readers observe, per session. The
	// meter must report each folded token exactly once across all delta
	// reads: no loss under a lost update, no duplication under a raced
	// watermark. Guarded because concurrent readers write it.
	var drainedMu sync.Mutex
	drainedIn := make(map[string]int64, sessions)
	drainedOut := make(map[string]int64, sessions)

	// foldsDone gates the readers: they keep draining deltas while any fold
	// is in flight, so the read path genuinely contends with the fold path,
	// and exit once every fold has completed.
	foldsDone := make(chan struct{})

	var foldWG, readerWG sync.WaitGroup

	// Fold: addersPerSid goroutines per session each fold perAdder frames of
	// (perFrameIn, perFrameOut). Every session's expected total is exact.
	for _, sid := range sids {
		for a := 0; a < addersPerSid; a++ {
			foldWG.Add(1)
			go func(sid string) {
				defer foldWG.Done()
				for j := 0; j < perAdder; j++ {
					m.Add(sid, perFrameIn, perFrameOut)
				}
			}(sid)
		}
	}

	// Read: readers goroutines drain incremental deltas across all sessions
	// concurrently with the folds until foldsDone closes. Each observed delta
	// is accumulated so the final reconciliation proves no token was lost or
	// double-reported.
	for r := 0; r < readers; r++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			localIn := make(map[string]int64, sessions)
			localOut := make(map[string]int64, sessions)
			for {
				for _, sid := range sids {
					u, err := m.Usage(context.Background(), sid)
					if err != nil {
						t.Errorf("Usage(%s): %v", sid, err)
						return
					}
					localIn[sid] += u.InputTokens
					localOut[sid] += u.OutputTokens
				}
				select {
				case <-foldsDone:
					// One final drain pass after the folds finish flushes any
					// delta produced after this reader's last pass; the final
					// per-session Usage below then reconciles the tail.
					for _, sid := range sids {
						u, err := m.Usage(context.Background(), sid)
						if err != nil {
							t.Errorf("Usage(%s): %v", sid, err)
							return
						}
						localIn[sid] += u.InputTokens
						localOut[sid] += u.OutputTokens
					}
					drainedMu.Lock()
					for _, sid := range sids {
						drainedIn[sid] += localIn[sid]
						drainedOut[sid] += localOut[sid]
					}
					drainedMu.Unlock()
					return
				default:
				}
			}
		}()
	}

	foldWG.Wait()
	close(foldsDone)
	readerWG.Wait()

	// Reconcile. For each session, the tokens the readers drained via delta
	// reads, plus any residual delta not yet read, must equal the exact total
	// folded (addersPerSid * perAdder frames), and the cumulative read must
	// report that same total. A lost update makes the sum fall short; a raced
	// watermark makes a delta read double-count and overshoot.
	wantIn := int64(addersPerSid * perAdder * perFrameIn)
	wantOut := int64(addersPerSid * perAdder * perFrameOut)
	for _, sid := range sids {
		residual, err := m.Usage(context.Background(), sid)
		if err != nil {
			t.Fatalf("final Usage(%s): %v", sid, err)
		}
		gotIn := drainedIn[sid] + residual.InputTokens
		gotOut := drainedOut[sid] + residual.OutputTokens
		if gotIn != wantIn {
			t.Errorf("session %s: drained+residual input = %d, want %d (lost or double-counted fold)", sid, gotIn, wantIn)
		}
		if gotOut != wantOut {
			t.Errorf("session %s: drained+residual output = %d, want %d (lost or double-counted fold)", sid, gotOut, wantOut)
		}

		// The cumulative read reports the running total regardless of prior
		// delta reads (it does not subtract the watermark), so it must equal
		// the exact folded total for the session.
		cum, err := m.Cumulative(context.Background(), sid)
		if err != nil {
			t.Fatalf("Cumulative(%s): %v", sid, err)
		}
		if cum.InputTokens != wantIn || cum.OutputTokens != wantOut {
			t.Errorf("session %s: cumulative = (%d,%d), want (%d,%d)", sid, cum.InputTokens, cum.OutputTokens, wantIn, wantOut)
		}
	}
}

// spec: §11.2 (crash-recovery MAX rule), §4.7 (cumulative read advances the watermark).
// diagnosis: a failure means the SessionUsageMeter watermark accounting raced under
// concurrent folds interleaved with a cumulative recovery read: a post-recovery delta
// read returned non-zero (re-adding already-recovered tokens, so the crash-recovery
// MAX rule double-counts) or the cumulative read lost a fold, under contention
// (proposal 0024 S3 watermark advance not atomic with the cumulative read).
func TestSessionUsageMeterCumulativeRecoveryNoDoubleCount_spec_11_2(t *testing.T) {
	t.Parallel()

	const (
		rounds       = 200
		addersPerSid = 6
		perAdder     = 50
	)
	const sid = "sess-recover"

	// Each round folds a fixed batch, then a single "recovery" cumulative
	// read snapshots the running total and advances the watermark to it. A
	// delta read immediately after the cumulative read, once the round's
	// folds have all settled, must return zero: the watermark advanced to
	// the cumulative total, so a reconnected replica seeded from the
	// cumulative total does not re-add the recovered tokens on its first
	// steady-state delta. This is the load-bearing §11.2 crash-recovery
	// no-double-count invariant, exercised under the flake budget.
	m := adapter.NewSessionUsageMeter(fixedClock())

	var priorCum int64
	for round := 0; round < rounds; round++ {
		var foldWG sync.WaitGroup
		for a := 0; a < addersPerSid; a++ {
			foldWG.Add(1)
			go func() {
				defer foldWG.Done()
				for j := 0; j < perAdder; j++ {
					m.Add(sid, 3, 5)
				}
			}()
		}
		foldWG.Wait()

		// Recovery read: snapshot the cumulative total and advance the
		// watermark to it. The total only ever grows, so each round's
		// cumulative must be at least the previous round's.
		cum, err := m.Cumulative(context.Background(), sid)
		if err != nil {
			t.Fatalf("round %d Cumulative: %v", round, err)
		}
		if cum.InputTokens < priorCum {
			t.Fatalf("round %d cumulative input %d < prior %d (watermark or total moved backward)", round, cum.InputTokens, priorCum)
		}
		priorCum = cum.InputTokens

		// Immediately after the cumulative recovery read, with no new fold in
		// flight, the delta read must be zero: the watermark advanced to the
		// cumulative total. A non-zero delta here is the exact §11.2
		// double-count the recovery MAX rule exists to prevent.
		delta, err := m.Usage(context.Background(), sid)
		if err != nil {
			t.Fatalf("round %d post-recovery Usage: %v", round, err)
		}
		if delta.InputTokens != 0 || delta.OutputTokens != 0 {
			t.Fatalf("round %d: post-recovery delta = (%d,%d), want (0,0) — recovered tokens re-added (double-count)", round, delta.InputTokens, delta.OutputTokens)
		}
	}

	// Final cumulative equals the exact total folded across all rounds.
	wantIn := int64(rounds * addersPerSid * perAdder * 3)
	wantOut := int64(rounds * addersPerSid * perAdder * 5)
	final, err := m.Cumulative(context.Background(), sid)
	if err != nil {
		t.Fatalf("final Cumulative: %v", err)
	}
	if final.InputTokens != wantIn || final.OutputTokens != wantOut {
		t.Fatalf("final cumulative = (%d,%d), want (%d,%d)", final.InputTokens, final.OutputTokens, wantIn, wantOut)
	}
}
