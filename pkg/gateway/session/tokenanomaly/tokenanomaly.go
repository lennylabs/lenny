// SPDX-License-Identifier: MIT

// Package tokenanomaly implements the §11.2 direct-mode token-usage integrity
// control: the per-session anomaly detector that observes each direct-mode
// `ReportUsage` delta the gateway pulls from a pod's adapter and emits
// `lenny_gateway_token_usage_anomaly_total` when a session's reported usage
// looks like under-reporting.
//
// In direct mode the pod egresses to the LLM provider directly and never
// reaches the §4.9 gateway proxy, so the gateway has no independent view of a
// direct-mode session's token consumption. A malicious or broken runtime can
// therefore under-report. This is an accepted residual risk for direct mode
// (restricted to single-tenant or development deployments), monitored rather
// than enforced: the detector raises a metric and a structured log so an
// operator can review the affected runtime image.
//
// The detector evaluates two §11.2 signals per session:
//
//   - `zero_delta`: the count of consecutive zero-token pulls within a session
//     exceeds the operator-tunable zero-token-window threshold (default the
//     spec's "greater than 3"). A non-zero delta resets the consecutive-zero
//     counter, so an interspersed non-zero pull (for example
//     `[0, 0, 0, nonzero, 0, 0, 0]`) does not fire.
//   - `implausibly_small`: the session's cumulative tokens-per-call ratio falls
//     below the operator-tunable implausibly-small ratio threshold, which
//     catches a runtime that reports a trickle of tokens across many LLM calls
//     rather than an outright zero.
//
// "While LLM proxy activity is absent" resolves to the direct-mode predicate:
// the recorder feeds the detector only from the direct-mode `RecordDirectUsage`
// path (a direct-mode lease has no proxy activity by definition), so the
// detector needs no separate proxy-activity check.
//
// The emitted counter is labeled by `tenant_id` and a bounded `reason`
// (`zero_delta` or `implausibly_small`) only. §16.1.1 forbids `session_id` as a
// Prometheus label; per-session attribution moves to a structured log emitted
// alongside each increment, as §16.1.1 directs.
//
// spec: §11.2 (direct-mode usage integrity), §16.1 (metric row), §16.1.1
// (forbidden labels; per-session attribution via structured logs). F-11.2.20.
package tokenanomaly

import (
	"log/slog"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

const (
	// metricName is the §11.2 / §16.1 direct-mode under-reporting counter.
	metricName = "lenny_gateway_token_usage_anomaly_total"

	// reasonZeroDelta labels an anomaly raised by the consecutive-zero-token
	// window. spec: §11.2 (zero_delta).
	reasonZeroDelta = "zero_delta"
	// reasonImplausiblySmall labels an anomaly raised by the tokens-per-call
	// ratio falling below the implausibly-small threshold. spec: §11.2
	// (implausibly_small).
	reasonImplausiblySmall = "implausibly_small"

	// DefaultZeroTokenWindow is the §11.2 default zero-token-window threshold:
	// the detector fires `zero_delta` once the count of consecutive zero-token
	// pulls within a session exceeds this value (the spec's "greater than 3").
	// Operator-tunable via the gateway config knob.
	DefaultZeroTokenWindow = 3

	// DefaultImplausiblySmallRatio is the default implausibly-small
	// tokens-per-call ratio threshold. §11.2 leaves the numeric value
	// unspecified and operator-tunable; this default treats a session
	// averaging fewer than one token per LLM call as implausibly small, which
	// no real provider response produces. A non-positive configured value
	// disables the `implausibly_small` branch. spec: §11.2.
	DefaultImplausiblySmallRatio = 1.0

	// implausibleMinCalls is the minimum number of observed calls before the
	// ratio branch evaluates, so a single early low-token pull does not fire
	// before a stable average forms. The detector needs the same evidence base
	// the zero-token window uses.
	implausibleMinCalls = DefaultZeroTokenWindow + 1
)

// Config carries the operator-tunable §11.2 firing thresholds. Both are
// documented, flag-overridable gateway configuration; §11.2 fixes only the
// zero-token-window default (greater than 3) and leaves the rest tunable.
type Config struct {
	// ZeroTokenWindow is the consecutive-zero-token count a session must
	// exceed before `zero_delta` fires. A non-positive value falls back to
	// DefaultZeroTokenWindow so a zeroed flag never disables the primary
	// under-reporting signal.
	ZeroTokenWindow int
	// ImplausiblySmallRatio is the tokens-per-call ratio below which
	// `implausibly_small` fires. A non-positive value disables the ratio
	// branch (the zero-token window still fires).
	ImplausiblySmallRatio float64
}

// withDefaults returns c with any unset field replaced by its default.
func (c Config) withDefaults() Config {
	if c.ZeroTokenWindow <= 0 {
		c.ZeroTokenWindow = DefaultZeroTokenWindow
	}
	return c
}

// sessionState is the per-session accumulator the detector keys on. It tracks
// the consecutive-zero-token run for the `zero_delta` branch and the running
// token and call totals for the `implausibly_small` ratio branch.
type sessionState struct {
	// consecutiveZero is the current run of consecutive zero-token pulls. A
	// non-zero pull resets it to zero.
	consecutiveZero int
	// totalTokens and calls form the cumulative tokens-per-call ratio.
	totalTokens int64
	calls       int64
	// firedZeroDelta and firedImplausible latch each reason so the detector
	// increments a session's series once per crossing rather than on every
	// subsequent pull, keeping the counter a signal of distinct anomalies
	// rather than a running tally of pulls.
	firedZeroDelta   bool
	firedImplausible bool
}

// Detector observes direct-mode `ReportUsage` deltas and raises the §11.2
// under-reporting metric and structured log. It implements the gateway
// recorder's directUsageObserver seam (Observe). It is safe for concurrent
// use by the direct-mode usage loops of multiple sessions.
type Detector struct {
	cfg     Config
	counter *prometheus.CounterVec
	log     *slog.Logger

	mu       sync.Mutex
	sessions map[string]*sessionState
}

// New constructs a Detector, registers its counter against reg, and returns
// it. The counter carries the §16.1.1-compliant {tenant_id, reason} label set;
// New returns the metrics.ValidationError if that set is ever changed to a
// forbidden label. A nil logger falls back to slog.Default so the per-session
// structured attribution always has a sink. spec: §11.2, §16.1.1. F-11.2.20.
func New(reg prometheus.Registerer, cfg Config, log *slog.Logger) (*Detector, error) {
	counter, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: metricName,
		Help: "Direct-mode ReportUsage under-reporting anomalies",
	}, []string{"tenant_id", "reason"})
	if err != nil {
		return nil, err
	}
	metrics.MustRegister(reg, counter)
	if log == nil {
		log = slog.Default()
	}
	return &Detector{
		cfg:      cfg.withDefaults(),
		counter:  counter,
		log:      log,
		sessions: make(map[string]*sessionState),
	}, nil
}

// Observe records one direct-mode `ReportUsage` delta for a session. input and
// output are the incremental token counts the gateway pulled since its
// previous poll. A zero-token delta extends the consecutive-zero run; a
// non-zero delta resets it. Observe fires `zero_delta` when the run first
// exceeds the configured window and `implausibly_small` when the cumulative
// tokens-per-call ratio first falls below the configured threshold, emitting
// the metric and a structured log for each. spec: §11.2. F-11.2.20.
func (d *Detector) Observe(tenantID, sessionID string, input, output int64) {
	if d == nil || sessionID == "" {
		return
	}

	delta := input + output

	d.mu.Lock()
	st, ok := d.sessions[sessionID]
	if !ok {
		st = &sessionState{}
		d.sessions[sessionID] = st
	}
	st.calls++
	st.totalTokens += delta
	if delta == 0 {
		st.consecutiveZero++
	} else {
		// spec: §11.2 — a non-zero delta is evidence the runtime is reporting
		// tokens, so it resets the consecutive-zero run. This is what keeps an
		// interspersed non-zero pull ([0,0,0,nonzero,0,0,0]) from firing.
		st.consecutiveZero = 0
	}

	fireZero := !st.firedZeroDelta && st.consecutiveZero > d.cfg.ZeroTokenWindow
	if fireZero {
		st.firedZeroDelta = true
	}

	// spec: §11.2 — evaluate the tokens-per-call ratio once a session has
	// enough calls for a stable average, so a single early low-token pull does
	// not fire before the pattern forms. A non-positive threshold disables the
	// branch. A session already flagged zero_delta (all-zero stream) is left to
	// that reason rather than double-flagged as implausibly small.
	fireImplausible := false
	if d.cfg.ImplausiblySmallRatio > 0 && !st.firedImplausible && !st.firedZeroDelta &&
		st.calls >= implausibleMinCalls {
		ratio := float64(st.totalTokens) / float64(st.calls)
		if ratio < d.cfg.ImplausiblySmallRatio {
			fireImplausible = true
			st.firedImplausible = true
		}
	}
	consecutiveZero := st.consecutiveZero
	totalTokens := st.totalTokens
	calls := st.calls
	d.mu.Unlock()

	if fireZero {
		d.fire(tenantID, sessionID, reasonZeroDelta, slog.Int("consecutive_zero_pulls", consecutiveZero))
	}
	if fireImplausible {
		d.fire(tenantID, sessionID, reasonImplausiblySmall,
			slog.Int64("total_tokens", totalTokens), slog.Int64("calls", calls))
	}
}

// fire increments the {tenant_id, reason} series and emits the per-session
// structured log that carries session_id (the §16.1.1 per-session attribution
// the forbidden Prometheus label cannot provide).
func (d *Detector) fire(tenantID, sessionID, reason string, extra ...slog.Attr) {
	d.counter.WithLabelValues(tenantID, reason).Inc()

	attrs := []any{
		slog.String("event", "token_usage_anomaly"),
		slog.String("tenant_id", tenantID),
		slog.String("session_id", sessionID),
		slog.String("reason", reason),
	}
	for _, a := range extra {
		attrs = append(attrs, a)
	}
	// spec: §16.1.1 — per-session attribution lives on the structured log, not
	// on the Prometheus label. A persistent anomaly should trigger operator
	// review of the affected runtime image (§11.2).
	d.log.Warn("token_usage_anomaly", attrs...)
}

// Forget drops sessionID's accumulated state so the detector's per-session map
// does not grow without bound as sessions settle. The gateway calls it from
// the same terminal-side-effects pipeline that forgets the other per-session
// accumulators. Nil-safe.
func (d *Detector) Forget(sessionID string) {
	if d == nil || sessionID == "" {
		return
	}
	d.mu.Lock()
	delete(d.sessions, sessionID)
	d.mu.Unlock()
}
