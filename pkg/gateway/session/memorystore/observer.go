// SPDX-License-Identifier: MIT

package memorystore

import "log"

// Operation labels enumerate the six §9.4 / §16.1 line 151
// `lenny_memory_store_operation_duration_seconds` operation labels.
// Every MemoryStore implementation records one observation per call
// under exactly one of these. The whole-scope erasure labels
// OpDeleteByUser and OpDeleteByTenant drive the §16.5
// MemoryStoreErasureDurationHigh alert and are distinct from the per-
// record OpDelete (a Delete(ctx, scope, ids) call against a single id).
//
// spec: §9.4 line 200, §16.1 line 151.
const (
	OpWrite          = "write"
	OpQuery          = "query"
	OpDelete         = "delete"
	OpList           = "list"
	OpDeleteByUser   = "delete_by_user"
	OpDeleteByTenant = "delete_by_tenant"
)

// ThresholdFraction is the §9.4 line 202 per-user headroom fraction.
// A Write commit that leaves the writing user at >= ThresholdFraction
// of memory.maxMemoriesPerUser increments the headroom counter.
const ThresholdFraction = 0.8

// Observer is the §9.4 / §16.1 instrumentation hook every MemoryStore
// implementation calls into. Implementations record one
// ObserveOperation per completed call, increment IncError on a non-nil
// error return, refresh SetRecordCount on writes/deletes (or via a
// periodic sampler), and increment IncUserOverThreshold on each Write
// commit that crosses the §9.4 ThresholdFraction headroom.
//
// A nil Observer is permitted; the default backends short-circuit when
// no Observer is wired. NoopObserver is the explicit no-op for unit
// tests that exercise the instrumentation paths without surfacing
// metrics. spec: §9.4 line 200.
type Observer interface {
	ObserveOperation(operation string, seconds float64)
	IncError(operation, errorType string)
	SetRecordCount(tenantID string, count int)
	IncUserOverThreshold(tenantID string)
}

// NoopObserver is the no-op Observer for tests and any backend that
// has no instrumentation sink wired.
type NoopObserver struct{}

// ObserveOperation implements Observer.
func (NoopObserver) ObserveOperation(string, float64) {}

// IncError implements Observer.
func (NoopObserver) IncError(string, string) {}

// SetRecordCount implements Observer.
func (NoopObserver) SetRecordCount(string, int) {}

// IncUserOverThreshold implements Observer.
func (NoopObserver) IncUserOverThreshold(string) {}

// ThresholdCount returns the §9.4 line 202 80%-of-cap rounding-up
// threshold for maxPerUser. A maxPerUser of 10000 yields 8000; a
// small bound (used in tests) still allows the threshold to be reached
// before the cap. A non-positive maxPerUser yields 0.
func ThresholdCount(maxPerUser int) int {
	if maxPerUser <= 0 {
		return 0
	}
	t := (maxPerUser * 8) / 10
	if t == 0 {
		t = 1
	}
	return t
}

// ClassifyError maps an error returned by a Store method to the
// `error_type` label on `lenny_memory_store_errors_total`. The shape
// is bounded by §16.1.1 — empty-scope errors collapse to
// `empty_scope`; every other error is `internal`. Callers that have a
// finer classification can pass the label directly to
// Observer.IncError.
func ClassifyError(err error) string {
	if err == nil {
		return ""
	}
	switch err {
	case ErrEmptyTenant, ErrEmptyUser:
		return "empty_scope"
	default:
		return "internal"
	}
}

// LogOverThreshold writes a structured log line that accompanies an
// IncUserOverThreshold call, attributing the over-cap user to the
// §9.4 line 202 "structured logs emitted alongside each increment"
// contract. The function is on the package so every backend that
// satisfies the §9.4 contract emits the same line format.
func LogOverThreshold(backend, tenantID, userID string, count, max int) {
	log.Printf("memorystore: §9.4 line 202 user over threshold backend=%s tenant=%s user=%s count=%d max=%d threshold=%.2f",
		backend, tenantID, userID, count, max, ThresholdFraction)
}
