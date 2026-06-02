// SPDX-License-Identifier: MIT

// Package failopen implements the §12.4 / §11.2 degraded-mode quota
// controls a gateway replica applies while Redis is unavailable.
//
// The pure §11.2 ceiling arithmetic lives in pkg/quota (FailOpenCeiling,
// PerUserFailOpenCeiling). This package supplies the stateful runtime the
// spec names but pkg/quota deliberately omits: the per-replica cumulative
// fail-open timer (a true sliding window with on-disk persistence across
// restarts), the in-memory per-user / per-tenant emergency backstop
// counters that bound a single user during the outage window, and the
// cached Kubernetes replica count the per-replica ceiling divides by.
//
// spec: §12.4 lines 220-224 (bounded fail-open, per-user ceiling,
// per-tenant budget, cumulative timer); §11.2 ("Maximum Overshoot
// Formula").
package failopen

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// DefaultCumulativeWindow is the §12.4 line 224 rolling window over which
// cumulative fail-open time is summed: 1 hour, not calendar-aligned.
const DefaultCumulativeWindow = time.Hour

// DefaultCumulativeMaxSeconds is the §12.4 line 224
// `quotaFailOpenCumulativeMaxSeconds` default (300s). Once cumulative
// fail-open time within the rolling window exceeds this, the replica
// transitions to fail-closed for quota enforcement.
const DefaultCumulativeMaxSeconds = 300 * time.Second

// DefaultCumulativeStatePath is the §12.4 line 224 local file each replica
// persists on every fail-open transition so a restart resumes the
// cumulative timer rather than resetting it (the CrashLoopBackOff bypass
// the spec calls out).
const DefaultCumulativeStatePath = "/run/lenny/failopen-cumulative.json"

// interval is one closed fail-open episode [Start, End).
type interval struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// persistState is the on-disk shape written to the cumulative state file.
// UpdatedAt is the freshness stamp the spec checks on restart: a file
// older than the rolling window is treated as a cold start and ignored.
type persistState struct {
	UpdatedAt time.Time  `json:"updatedAt"`
	Intervals []interval `json:"intervals"`
	OpenSince *time.Time `json:"openSince,omitempty"`
}

// CumulativeTimer is the §12.4 line 224 cumulative fail-open timer: a true
// sliding window counter that accumulates total time the replica has spent
// in fail-open mode across every Redis outage within the rolling window.
// The counter is in-memory (never in Redis) and is persisted to a local
// file on every transition so a restart during a sustained outage cannot
// reset it.
//
// spec: §12.4 line 224.
type CumulativeTimer struct {
	mu        sync.Mutex
	window    time.Duration
	maxDur    time.Duration
	closed    []interval
	openSince time.Time // zero when the replica is not currently fail-open
	path      string
	now       func() time.Time
	onChange  func(cumulativeSeconds float64)
}

// CumulativeConfig configures a CumulativeTimer.
type CumulativeConfig struct {
	// Window is the rolling window cumulative time is summed over. Zero
	// selects DefaultCumulativeWindow.
	Window time.Duration
	// MaxSeconds is the cumulative threshold past which the replica fails
	// closed for quota. Zero selects DefaultCumulativeMaxSeconds; a
	// negative value disables the threshold (cumulative never trips).
	MaxSeconds time.Duration
	// StatePath is the persistence file. Empty selects
	// DefaultCumulativeStatePath; "-" disables persistence (tests).
	StatePath string
	// Now overrides the clock. Nil selects time.Now.
	Now func() time.Time
	// OnChange, when set, receives the new cumulative-seconds value after
	// every transition so the caller can mirror it onto the
	// lenny_quota_failopen_cumulative_seconds gauge. spec: §12.4 line 224.
	OnChange func(cumulativeSeconds float64)
}

// NewCumulativeTimer builds a CumulativeTimer and, when persistence is
// enabled and a fresh state file exists, resumes the cumulative value from
// it. A state file older than the rolling window, missing, or corrupt is
// treated as a cold start (the timer resets to zero) per §12.4 line 224.
func NewCumulativeTimer(cfg CumulativeConfig) *CumulativeTimer {
	t := &CumulativeTimer{
		window:   cfg.Window,
		maxDur:   cfg.MaxSeconds,
		path:     cfg.StatePath,
		now:      cfg.Now,
		onChange: cfg.OnChange,
	}
	if t.window <= 0 {
		t.window = DefaultCumulativeWindow
	}
	if t.maxDur == 0 {
		t.maxDur = DefaultCumulativeMaxSeconds
	}
	if t.now == nil {
		t.now = time.Now
	}
	if t.path == "" {
		t.path = DefaultCumulativeStatePath
	}
	t.restore()
	return t
}

// restore loads the persisted state when it is fresh. A crash that left
// the replica mid-episode (OpenSince set) is credited as a closed interval
// [OpenSince, UpdatedAt] — the fail-open time the replica is known to have
// accrued before it died — and the replica starts healthy.
func (t *CumulativeTimer) restore() {
	if t.path == "-" {
		return
	}
	raw, err := os.ReadFile(t.path)
	if err != nil {
		return
	}
	var st persistState
	if json.Unmarshal(raw, &st) != nil {
		return
	}
	now := t.now()
	// spec: §12.4 line 224 — resume only when the file's timestamp is
	// within the rolling window; otherwise this is a cold start.
	if st.UpdatedAt.IsZero() || now.Sub(st.UpdatedAt) > t.window {
		return
	}
	t.closed = st.Intervals
	if st.OpenSince != nil && !st.OpenSince.IsZero() && st.UpdatedAt.After(*st.OpenSince) {
		t.closed = append(t.closed, interval{Start: *st.OpenSince, End: st.UpdatedAt})
	}
	t.prune(now)
}

// Enter records the leading edge of a fail-open episode. It returns true
// only on the healthy→fail-open transition so the caller emits the
// §16.7 quota_failopen_started audit event exactly once per episode.
// spec: §12.4 line 224.
func (t *CumulativeTimer) Enter() bool {
	t.mu.Lock()
	if !t.openSince.IsZero() {
		t.mu.Unlock()
		return false
	}
	now := t.now()
	t.openSince = now
	t.prune(now)
	cum := t.cumulativeLocked(now)
	t.persistLocked(now)
	t.mu.Unlock()
	t.notify(cum)
	return true
}

// Exit records the recovery edge, closing the current episode into the
// window. It is a no-op when the replica is not currently fail-open.
// spec: §12.4 line 224.
func (t *CumulativeTimer) Exit() {
	t.mu.Lock()
	if t.openSince.IsZero() {
		t.mu.Unlock()
		return
	}
	now := t.now()
	if now.After(t.openSince) {
		t.closed = append(t.closed, interval{Start: t.openSince, End: now})
	}
	t.openSince = time.Time{}
	t.prune(now)
	cum := t.cumulativeLocked(now)
	t.persistLocked(now)
	t.mu.Unlock()
	t.notify(cum)
}

// CumulativeSeconds returns the total fail-open seconds within the rolling
// window ending now, including any in-flight episode. spec: §12.4 line 224.
func (t *CumulativeTimer) CumulativeSeconds() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cumulativeLocked(t.now())
}

// Exceeded reports whether cumulative fail-open time within the rolling
// window has reached the configured maximum. A non-positive maximum
// disables the check. spec: §12.4 line 224 (transition to fail-closed for
// quota enforcement).
func (t *CumulativeTimer) Exceeded() bool {
	if t.maxDur <= 0 {
		return false
	}
	return t.CumulativeSeconds() >= t.maxDur.Seconds()
}

// MaxSeconds returns the configured cumulative cap so callers can report
// it (the §16.5 QuotaFailOpenCumulativeThreshold pre-breach warning fires
// at 80% of this).
func (t *CumulativeTimer) MaxSeconds() float64 { return t.maxDur.Seconds() }

// cumulativeLocked sums the overlap of each tracked episode with the
// rolling window [now-window, now]. The caller holds t.mu.
func (t *CumulativeTimer) cumulativeLocked(now time.Time) float64 {
	windowStart := now.Add(-t.window)
	var total time.Duration
	add := func(start, end time.Time) {
		if start.Before(windowStart) {
			start = windowStart
		}
		if end.After(now) {
			end = now
		}
		if end.After(start) {
			total += end.Sub(start)
		}
	}
	for _, iv := range t.closed {
		add(iv.Start, iv.End)
	}
	if !t.openSince.IsZero() {
		add(t.openSince, now)
	}
	return total.Seconds()
}

// prune drops closed intervals that have fully fallen out of the rolling
// window so the slice cannot grow without bound. The caller holds t.mu.
func (t *CumulativeTimer) prune(now time.Time) {
	windowStart := now.Add(-t.window)
	kept := t.closed[:0]
	for _, iv := range t.closed {
		if iv.End.After(windowStart) {
			kept = append(kept, iv)
		}
	}
	t.closed = kept
}

// persistLocked writes the current state to the local file (best-effort).
// The caller holds t.mu. A write failure is swallowed: the in-memory
// timer stays authoritative and the only loss is restart resumption.
func (t *CumulativeTimer) persistLocked(now time.Time) {
	if t.path == "-" {
		return
	}
	st := persistState{UpdatedAt: now, Intervals: t.closed}
	if !t.openSince.IsZero() {
		open := t.openSince
		st.OpenSince = &open
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = os.WriteFile(t.path, raw, 0o600)
}

func (t *CumulativeTimer) notify(cum float64) {
	if t.onChange != nil {
		t.onChange(cum)
	}
}
