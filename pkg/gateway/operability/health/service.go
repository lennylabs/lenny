// SPDX-License-Identifier: MIT

// Package health implements the §25.3 Platform Health API. It
// aggregates the health of the gateway's subsystems into the
// agent-operability surface an AI-DevOps agent reads to decide
// whether the platform needs remediation.
//
// The package is transport-agnostic: a Checker reports a Component
// status, the Aggregator rolls Components into an overall verdict,
// and the §15.1 HTTP handler in handler.go serves the result.
package health

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// probeCacheTTL is the §25.3 per-replica probe-result cache window: a
// component's last Check result is reused for this long so concurrent
// health requests do not stampede the backing dependency.
// spec: §25.3 line 526 — "Component probe results are cached in-memory
// for 5 seconds to avoid probe storms from concurrent health checks."
const probeCacheTTL = 5 * time.Second

// Status is the §25.3 health verdict enum.
type Status string

const (
	// StatusHealthy — the component is fully operational.
	StatusHealthy Status = "healthy"

	// StatusDegraded — the component is operational but impaired
	// (e.g., a replica is down, a cache is cold).
	StatusDegraded Status = "degraded"

	// StatusUnhealthy — the component is not operational.
	StatusUnhealthy Status = "unhealthy"
)

// rank orders statuses worst-last so an aggregate can take the worst.
func (s Status) rank() int {
	switch s {
	case StatusHealthy:
		return 0
	case StatusDegraded:
		return 1
	case StatusUnhealthy:
		return 2
	default:
		return 2
	}
}

// Component is the §25.3 per-subsystem health record.
type Component struct {
	// Name identifies the subsystem (e.g., `sessionstore`,
	// `blobstore`, `executor`).
	Name string `json:"name"`

	// Status is the subsystem verdict.
	Status Status `json:"status"`

	// Detail is a human-readable description of the current state.
	Detail string `json:"detail,omitempty"`

	// Issue is the §25.7 Path B health-API issue code (e.g.,
	// `POSTGRES_UNREACHABLE`, `WARM_POOL_EXHAUSTED`,
	// `CIRCUIT_BREAKER_OPEN`). The §17.7 line 741 issueRunbooks
	// table resolves this to the runbook the agent should fetch.
	// spec: §25.7 lines 3217-3234.
	Issue string `json:"issue,omitempty"`

	// SuggestedAction is the §25.3 singular machine-executable
	// remediation hint, populated when one canonical response exists.
	// Nil when the component is healthy, when no action is known, or
	// when the issue presents ranked alternatives (SuggestedActions)
	// instead. spec: §25.3 lines 459-501.
	SuggestedAction *conventions.SuggestedAction `json:"suggestedAction,omitempty"`

	// SuggestedActions is the §25.3 ordered (descending confidence) set
	// of remediation alternatives for the capacity/throttling issues
	// that have more than one reasonable response (WARM_POOL_EXHAUSTED,
	// WARM_POOL_LOW, CREDENTIAL_POOL_EXHAUSTED, CIRCUIT_BREAKER_OPEN).
	// Empty for the singular form. spec: §25.3 lines 484-487.
	SuggestedActions []conventions.SuggestedAction `json:"suggestedActions,omitempty"`
}

// AlertStatusSource reports the §25.3 alert-derived health of a component:
// the worst severity among firing §16.5 alerts mapped to it (a critical
// alert maps to unhealthy, a warning alert to degraded). The gateway wires
// this over its in-process alert tracker (§25.13) so /v1/admin/health
// reflects firing alerts even when Prometheus is unreachable. ok is false
// when no firing alert maps to the component, in which case the dependency
// probe's verdict stands. firing names the firing alerts for the Detail
// line. spec: §25.3 lines 443-451.
type AlertStatusSource interface {
	ComponentStatus(component string) (status Status, firing []string, ok bool)
}

// Checker reports the health of one subsystem. Implementations must
// be fast and non-blocking — Check runs on the §25.3 health request
// path; expensive probes belong in a background goroutine that
// caches its result.
type Checker interface {
	// Name returns the subsystem name. Must be stable.
	Name() string

	// Check returns the current Component health.
	Check(ctx context.Context) Component
}

// CheckerFunc adapts a function to the Checker interface.
type CheckerFunc struct {
	ComponentName string
	Fn            func(ctx context.Context) Component
}

// Name implements Checker.
func (c CheckerFunc) Name() string { return c.ComponentName }

// Check implements Checker.
func (c CheckerFunc) Check(ctx context.Context) Component { return c.Fn(ctx) }

// Report is the §25.3 aggregate health response.
type Report struct {
	// Status is the worst component status — healthy only when
	// every component is healthy.
	Status Status `json:"status"`

	// Components is the per-subsystem breakdown, name-sorted.
	Components []Component `json:"components"`

	// Degradation carries the §25.2 canonical envelope. The gateway's
	// in-process alert tracker always evaluates the compiled-in
	// thresholds, so a single-replica Report stamps
	// `thresholdSource: "compiled-in-defaults"` per §25.13 line 4848.
	// `lenny-ops` overrides the envelope when its aggregated view
	// derives from the operator's Prometheus rules.
	// spec: §25.2.
	Degradation *conventions.Degradation `json:"degradation,omitempty"`
}

// cachedProbe is a component's Check result stamped with the time it
// was produced so the Aggregator can serve it within probeCacheTTL.
type cachedProbe struct {
	comp Component
	at   time.Time
}

// Aggregator holds the registered Checkers and rolls them up.
// Goroutine-safe.
type Aggregator struct {
	mu             sync.RWMutex
	checkers       map[string]Checker
	lastStatus     Status
	lastComponents []Component
	onTransition   func(prev, curr Status, prevComponents, currComponents []Component)

	// §25.3 line 526 per-replica probe-result cache. cacheTTL of 0
	// disables caching (every request runs the probes); the clock is
	// injectable so tests can advance it deterministically.
	cacheMu  sync.Mutex
	cache    map[string]cachedProbe
	cacheTTL time.Duration
	now      func() time.Time

	// §25.3 lines 538-542 health metrics. Nil until SetMetrics wires the
	// prom-backed emitter; the Aggregator records probe latency and the
	// derived status on every real (cache-miss) probe.
	metrics Metrics

	// §25.3 lines 443-451 alert-derived status overlay. Nil until
	// SetAlertSource wires the in-process alert tracker; when set, the
	// /v1/admin/health verdict (Report and the public Component) overlays
	// the worst firing-alert severity onto each component's probe verdict.
	// The readiness path (HardDependencyStatus) deliberately reads the raw
	// probe so a transient alert cannot pull a replica out of the Service.
	alertSource AlertStatusSource
}

// NewAggregator returns an empty Aggregator with the §25.3 5-second
// per-replica probe-result cache enabled (line 526).
func NewAggregator() *Aggregator {
	return newAggregator(probeCacheTTL, time.Now)
}

// NewAggregatorWithCache returns an Aggregator whose probe-result cache
// uses the given TTL and clock. A non-positive ttl disables caching. A
// nil clock falls back to time.Now. The gateway uses NewAggregator;
// this variant exists so the §25.3 cache can be exercised against a
// controllable clock.
func NewAggregatorWithCache(ttl time.Duration, now func() time.Time) *Aggregator {
	return newAggregator(ttl, now)
}

func newAggregator(ttl time.Duration, now func() time.Time) *Aggregator {
	if now == nil {
		now = time.Now
	}
	return &Aggregator{
		checkers: map[string]Checker{},
		cache:    map[string]cachedProbe{},
		cacheTTL: ttl,
		now:      now,
	}
}

// probe runs c's Check unless a result cached within probeCacheTTL is
// still fresh, in which case the cached Component is returned without
// touching the backing dependency. It returns the raw probe verdict; the
// §25.3 alert overlay is applied separately by componentWithAlerts so the
// readiness path can read the dependency verdict without alert-driven
// flapping. spec: §25.3 line 526.
func (a *Aggregator) probe(ctx context.Context, c Checker) Component {
	name := c.Name()
	if a.cacheTTL > 0 {
		a.cacheMu.Lock()
		if hit, ok := a.cache[name]; ok && a.now().Sub(hit.at) < a.cacheTTL {
			a.cacheMu.Unlock()
			return hit.comp
		}
		a.cacheMu.Unlock()
	}
	a.mu.RLock()
	m := a.metrics
	a.mu.RUnlock()
	started := a.now()
	comp := c.Check(ctx)
	if comp.Name == "" {
		comp.Name = name
	}
	// spec: §25.3 lines 538-542 — record probe latency only on a real
	// Check (a cache hit ran no probe). The derived-status gauge is set by
	// componentWithAlerts so it reflects the alert overlay.
	if m != nil {
		m.ObserveCheckDuration(comp.Name, a.now().Sub(started).Seconds())
	}
	// spec: §25.3 lines 459-501 / §25.7 line 3234 — when the checker
	// stamps an Issue but leaves the remediation hint empty, the catalog
	// resolves the structured suggestedAction (singular) or
	// suggestedActions (ranked, for capacity issues) so the agent
	// receives a machine-executable hint, with the runbook pointer
	// sourced from the §17.7 issueRunbooks table, without per-checker
	// duplication. A checker that already populated either form keeps
	// control.
	if comp.Issue != "" && comp.SuggestedAction == nil && len(comp.SuggestedActions) == 0 {
		comp.SuggestedAction, comp.SuggestedActions = ActionsForIssue(comp.Issue, comp.Name)
	}
	if a.cacheTTL > 0 {
		a.cacheMu.Lock()
		a.cache[name] = cachedProbe{comp: comp, at: a.now()}
		a.cacheMu.Unlock()
	}
	return comp
}

// componentWithAlerts runs the raw probe and overlays the §25.3
// alert-derived verdict: a firing alert mapped to this component
// degrades/fails it even when the dependency probe itself is healthy. The
// overlay is evaluated fresh on every call (never cached) because the
// in-process alert tracker reads the live metric registry, so a cache hit
// would otherwise mask a just-fired alert. This is the verdict the
// /v1/admin/health endpoint reports, and the only path that records the
// derived status on lenny_health_status.
// spec: §25.3 lines 443-451, 538-542.
func (a *Aggregator) componentWithAlerts(ctx context.Context, c Checker) Component {
	comp := a.probe(ctx, c)
	a.mu.RLock()
	src := a.alertSource
	m := a.metrics
	a.mu.RUnlock()
	comp = applyAlertStatus(src, comp)
	if m != nil {
		m.SetStatus(comp.Name, comp.Status)
	}
	return comp
}

// applyAlertStatus overlays the §25.3 alert-derived verdict onto a probe
// result. The overlay only ever worsens the status (a firing alert cannot
// mark a probe-failed component healthy); on a worsening it appends the
// firing alert names to the Detail line so the operator sees why.
// spec: §25.3 lines 443-451.
func applyAlertStatus(src AlertStatusSource, comp Component) Component {
	if src == nil {
		return comp
	}
	st, firing, ok := src.ComponentStatus(comp.Name)
	if !ok || st.rank() <= comp.Status.rank() {
		return comp
	}
	comp.Status = st
	if d := firingDetail(firing); d != "" {
		if comp.Detail == "" {
			comp.Detail = d
		} else {
			comp.Detail += "; " + d
		}
	}
	return comp
}

// firingDetail renders the firing-alert names into a stable Detail string.
func firingDetail(firing []string) string {
	if len(firing) == 0 {
		return ""
	}
	names := append([]string(nil), firing...)
	sort.Strings(names)
	return "firing alerts: " + strings.Join(names, ", ")
}

// SetMetrics wires the §25.3 health metrics. The Aggregator records each
// real probe's latency on lenny_health_check_duration_seconds and the
// derived verdict on lenny_health_status. spec: §25.3 lines 538-542.
func (a *Aggregator) SetMetrics(m Metrics) {
	a.mu.Lock()
	a.metrics = m
	a.mu.Unlock()
}

// SetAlertSource wires the §25.3 alert-derived status overlay. Once set,
// Report and the public Component overlay the worst firing-alert severity
// onto each component so /v1/admin/health reflects the §16.5 alert
// catalogue, not only the dependency probes. spec: §25.3 lines 443-451.
func (a *Aggregator) SetAlertSource(src AlertStatusSource) {
	a.mu.Lock()
	a.alertSource = src
	a.mu.Unlock()
}

// OnTransition registers a callback the Aggregator invokes when a
// Report computes an aggregate status that differs from the previous
// Report's. It is the §25.3 health_status_changed hook. The first
// Report establishes the baseline and fires no transition.
//
// prevComponents and currComponents are the per-component snapshots
// from the previous and the new Report, so the caller can identify the
// triggering component(s) — the components whose individual status
// changed between the two snapshots — the same way the §25.4
// ops_health_status_changed payload names its triggering check(s).
// spec: §25.3 (event types — health_status_changed payload: "Old
// status, new status, triggering component").
func (a *Aggregator) OnTransition(fn func(prev, curr Status, prevComponents, currComponents []Component)) {
	a.mu.Lock()
	a.onTransition = fn
	a.mu.Unlock()
}

// Register adds a Checker. A later Register with the same Name
// replaces the earlier one.
func (a *Aggregator) Register(c Checker) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checkers[c.Name()] = c
}

// Report runs every registered Checker and returns the aggregate. The
// checkers fan out across goroutines so the aggregate runtime is
// bounded by the slowest probe rather than the sum of all probe
// latencies; the 2-second per-probe timeout is enforced by the
// individual Checker implementations against the supplied context.
// spec: §25.3 line 441 — "Each probe has a hard timeout of 2 seconds.
// Probes run in parallel."
func (a *Aggregator) Report(ctx context.Context) Report {
	a.mu.RLock()
	checkers := make([]Checker, 0, len(a.checkers))
	for _, c := range a.checkers {
		checkers = append(checkers, c)
	}
	a.mu.RUnlock()

	components := make([]Component, len(checkers))
	var wg sync.WaitGroup
	for i, c := range checkers {
		wg.Add(1)
		go func(i int, c Checker) {
			defer wg.Done()
			components[i] = a.componentWithAlerts(ctx, c)
		}(i, c)
	}
	wg.Wait()

	worst := StatusHealthy
	for _, comp := range components {
		if comp.Status.rank() > worst.rank() {
			worst = comp.Status
		}
	}
	sort.Slice(components, func(i, j int) bool { return components[i].Name < components[j].Name })

	// §25.3: detect an aggregate-status transition against the previous
	// Report and fire the health_status_changed hook outside the lock.
	// prevComponents is the component snapshot from the previous Report;
	// it lets the hook identify the triggering component(s) alongside
	// the old/new aggregate status.
	a.mu.Lock()
	prev := a.lastStatus
	prevComponents := a.lastComponents
	a.lastStatus = worst
	a.lastComponents = components
	fn := a.onTransition
	a.mu.Unlock()
	if fn != nil && prev != "" && prev != worst {
		fn(prev, worst, prevComponents, components)
	}
	// spec: §25.13 line 4848 — the gateway's in-process tracker
	// evaluates the compiled-in thresholds. Surface the source on the
	// envelope so callers (and `lenny-ops` re-aggregation) can decide
	// whether the per-replica view aligns with the operator's
	// Prometheus-side customisation.
	return Report{
		Status:     worst,
		Components: components,
		Degradation: &conventions.Degradation{
			Level:           degradationLevelFor(worst),
			ThresholdSource: conventions.ThresholdSourceCompiledInDefaults,
		},
	}
}

// degradationLevelFor maps the §25.3 worst-status to the §25.2
// envelope level. healthy → healthy, degraded → degraded, unhealthy →
// failed.
func degradationLevelFor(s Status) conventions.DegradationLevel {
	switch s {
	case StatusDegraded:
		return conventions.DegradationDegraded
	case StatusUnhealthy:
		return conventions.DegradationFailed
	default:
		return conventions.DegradationHealthy
	}
}

// Component returns the single named component's health, or
// (Component{}, false) when no Checker with that name is
// registered.
func (a *Aggregator) Component(ctx context.Context, name string) (Component, bool) {
	a.mu.RLock()
	c, ok := a.checkers[name]
	a.mu.RUnlock()
	if !ok {
		return Component{}, false
	}
	// Shares the §25.3 line 526 probe-result cache with Report so a
	// single-component pull does not bypass the 5-second window, and
	// applies the same alert-derived overlay so GET /v1/admin/health/{name}
	// matches the aggregate Report's per-component verdict.
	return a.componentWithAlerts(ctx, c), true
}

// HardDependencyStatus probes the named checkers and returns the worst
// Status among those that are registered, skipping any name that has no
// registered Checker. When none of the names is registered it returns
// StatusHealthy, so a deployment that wired no hard backend has nothing
// to gate on.
//
// The §10.4 readiness probe uses this to reflect the gateway replica's
// hard backend dependencies (the externalized session-truth store) in
// its verdict without rolling in the §11.7 SIEM-delivery checker, which
// is deliberately non-gating so a shared-SIEM outage cannot pull every
// replica out of the Service. The probe-result cache is shared with
// Report so the readiness probe does not stampede the backend.
//
// The readiness verdict reads the raw dependency probes (no §25.3 alert
// overlay) so a transient firing alert cannot pull a replica out of the
// Service; only an actual backend probe failure removes a replica.
//
// spec: §10.4 line 386 ("Readiness probes remove unhealthy replicas
// from traffic"). F-10.4.6.
func (a *Aggregator) HardDependencyStatus(ctx context.Context, names ...string) Status {
	worst := StatusHealthy
	for _, name := range names {
		a.mu.RLock()
		c, ok := a.checkers[name]
		a.mu.RUnlock()
		if !ok {
			continue
		}
		comp := a.probe(ctx, c)
		if comp.Status.rank() > worst.rank() {
			worst = comp.Status
		}
	}
	return worst
}

// TriggeringComponents returns the names of the components whose
// individual status changed between prev and curr, sorted by name.
// These are the components that drove the aggregate health transition
// an OnTransition hook observes. A component present in curr but absent
// from prev (for example, one registered after the baseline Report)
// counts as a trigger when its new status is not healthy.
//
// spec: §25.3 (event types — health_status_changed payload: "Old
// status, new status, triggering component").
func TriggeringComponents(prev, curr []Component) []string {
	prevStatus := make(map[string]Status, len(prev))
	for _, c := range prev {
		prevStatus[c.Name] = c.Status
	}
	var changed []string
	for _, c := range curr {
		before, ok := prevStatus[c.Name]
		switch {
		case ok && before != c.Status:
			changed = append(changed, c.Name)
		case !ok && c.Status != StatusHealthy:
			changed = append(changed, c.Name)
		}
	}
	sort.Strings(changed)
	return changed
}
