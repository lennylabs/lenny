// SPDX-License-Identifier: MIT

package inproceval

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/alerting/evaluator"
	"github.com/lennylabs/lenny/pkg/alerting/rules"
)

// inproceval implements the §25.13 ExprEvaluator surface.
var _ evaluator.ExprEvaluator = (*Evaluator)(nil)

// regBuilder accumulates gauges and counters into a fresh registry for a
// single test, mirroring the in-process registry the gateway exposes.
type regBuilder struct {
	t   *testing.T
	reg *prometheus.Registry
}

func newReg(t *testing.T) *regBuilder {
	return &regBuilder{t: t, reg: prometheus.NewRegistry()}
}

// gauge registers a single-series gauge with no labels.
func (b *regBuilder) gauge(name string, v float64) *regBuilder {
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: name})
	g.Set(v)
	if err := b.reg.Register(g); err != nil {
		b.t.Fatalf("register %s: %v", name, err)
	}
	return b
}

// counter registers a single-series counter with no labels.
func (b *regBuilder) counter(name string, v float64) *regBuilder {
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: name})
	c.Add(v)
	if err := b.reg.Register(c); err != nil {
		b.t.Fatalf("register %s: %v", name, err)
	}
	return b
}

// gaugeVec registers a labelled gauge and sets one value per label-value
// tuple. labelNames fixes the label order for the samples slice.
func (b *regBuilder) gaugeVec(name string, labelNames []string, samples []labeledSample) *regBuilder {
	gv := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: name}, labelNames)
	for _, s := range samples {
		gv.WithLabelValues(s.values...).Set(s.v)
	}
	if err := b.reg.Register(gv); err != nil {
		b.t.Fatalf("register %s: %v", name, err)
	}
	return b
}

type labeledSample struct {
	values []string
	v      float64
}

func (b *regBuilder) eval() *Evaluator { return New(b.reg) }

// TestActiveInstantThresholds_spec_25_13_4676 covers the bare gauge and
// counter comparison forms the in-process fallback must evaluate.
func TestActiveInstantThresholds_spec_25_13_4676(t *testing.T) {
	tests := []struct {
		name string
		reg  *regBuilder
		expr string
		want bool
	}{
		{"gt true", newReg(t).gauge("lenny_postgres_replication_lag_seconds", 2), "lenny_postgres_replication_lag_seconds > 1", true},
		{"gt false", newReg(t).gauge("lenny_postgres_replication_lag_seconds", 0.5), "lenny_postgres_replication_lag_seconds > 1", false},
		{"eq zero fires", newReg(t).gauge("lenny_postgres_primary_up", 0), "lenny_postgres_primary_up == 0", true},
		{"eq zero clears", newReg(t).gauge("lenny_postgres_primary_up", 1), "lenny_postgres_primary_up == 0", false},
		{"neq true", newReg(t).gauge("lenny_ops_self_health_status", 2), "lenny_ops_self_health_status != 1", true},
		{"neq false", newReg(t).gauge("lenny_ops_self_health_status", 1), "lenny_ops_self_health_status != 1", false},
		{"counter gt zero", newReg(t).counter("lenny_audit_grant_drift_total", 3), "lenny_audit_grant_drift_total > 0", true},
		{"counter at zero", newReg(t).counter("lenny_audit_grant_drift_total", 0), "lenny_audit_grant_drift_total > 0", false},
		{"circuit state eq", newReg(t).gauge("lenny_token_service_circuit_state", 2), "lenny_token_service_circuit_state == 2", true},
		{"float threshold", newReg(t).gauge("lenny_quota_user_failopen_fraction", 0.6), "lenny_quota_user_failopen_fraction >= 0.5", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.reg.eval().Active(context.Background(), tc.expr)
			if err != nil {
				t.Fatalf("Active(%q): unexpected error %v", tc.expr, err)
			}
			if got != tc.want {
				t.Fatalf("Active(%q) = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestActiveLabelMatchers_spec_25_13_4676 verifies that `=`/`!=` label
// matchers restrict which series the comparison sees.
func TestActiveLabelMatchers_spec_25_13_4676(t *testing.T) {
	reg := newReg(t).gaugeVec("lenny_gateway_gc_pause_fleet_p99_ms",
		[]string{"deployment_tier"},
		[]labeledSample{
			{values: []string{"tier3"}, v: 60},
			{values: []string{"tier1"}, v: 10},
		})
	got, err := reg.eval().Active(context.Background(), `lenny_gateway_gc_pause_fleet_p99_ms{deployment_tier="tier3"} > 50`)
	if err != nil || !got {
		t.Fatalf("tier3 series should fire: got=%v err=%v", got, err)
	}

	reg2 := newReg(t).gaugeVec("lenny_gateway_gc_pause_fleet_p99_ms",
		[]string{"deployment_tier"},
		[]labeledSample{
			{values: []string{"tier3"}, v: 10},
			{values: []string{"tier1"}, v: 99},
		})
	got, err = reg2.eval().Active(context.Background(), `lenny_gateway_gc_pause_fleet_p99_ms{deployment_tier="tier3"} > 50`)
	if err != nil || got {
		t.Fatalf("only tier1 exceeds, tier3 filtered: got=%v err=%v", got, err)
	}
}

// TestActiveAggregations_spec_25_13_4676 covers min/max/sum/count/avg
// grouping by labels.
func TestActiveAggregations_spec_25_13_4676(t *testing.T) {
	minReg := newReg(t).gaugeVec("lenny_credential_pool_assignable_count",
		[]string{"pool"},
		[]labeledSample{
			{values: []string{"a"}, v: 0},
			{values: []string{"b"}, v: 5},
		})
	got, err := minReg.eval().Active(context.Background(), `min by (pool) (lenny_credential_pool_assignable_count) == 0`)
	if err != nil || !got {
		t.Fatalf("pool a depleted: got=%v err=%v", got, err)
	}

	okReg := newReg(t).gaugeVec("lenny_credential_pool_assignable_count",
		[]string{"pool"},
		[]labeledSample{
			{values: []string{"a"}, v: 2},
			{values: []string{"b"}, v: 5},
		})
	got, err = okReg.eval().Active(context.Background(), `min by (pool) (lenny_credential_pool_assignable_count) == 0`)
	if err != nil || got {
		t.Fatalf("no pool depleted: got=%v err=%v", got, err)
	}

	maxReg := newReg(t).gaugeVec("lenny_gateway_subsystem_circuit_state",
		[]string{"subsystem"},
		[]labeledSample{
			{values: []string{"upload"}, v: 2},
			{values: []string{"proxy"}, v: 0},
		})
	got, err = maxReg.eval().Active(context.Background(), `max by (subsystem) (lenny_gateway_subsystem_circuit_state) == 2`)
	if err != nil || !got {
		t.Fatalf("upload subsystem open: got=%v err=%v", got, err)
	}

	// sum without `by` reduces every series to one group.
	sumReg := newReg(t).gaugeVec("lenny_x",
		[]string{"k"},
		[]labeledSample{
			{values: []string{"1"}, v: 3},
			{values: []string{"2"}, v: 4},
		})
	got, err = sumReg.eval().Active(context.Background(), `sum(lenny_x) > 6`)
	if err != nil || !got {
		t.Fatalf("sum=7 > 6: got=%v err=%v", got, err)
	}

	// count of series.
	got, err = sumReg.eval().Active(context.Background(), `count(lenny_x) == 2`)
	if err != nil || !got {
		t.Fatalf("count==2: got=%v err=%v", got, err)
	}

	// avg = 3.5.
	got, err = sumReg.eval().Active(context.Background(), `avg(lenny_x) > 3`)
	if err != nil || !got {
		t.Fatalf("avg=3.5 > 3: got=%v err=%v", got, err)
	}
}

// TestActiveScalarRHS_spec_25_13_4676 covers scalar() readbacks and
// constant arithmetic on the right-hand side.
func TestActiveScalarRHS_spec_25_13_4676(t *testing.T) {
	below := newReg(t).
		gauge("lenny_gateway_replica_count", 1).
		gauge("lenny_gateway_min_replicas", 3)
	got, err := below.eval().Active(context.Background(), `lenny_gateway_replica_count < scalar(lenny_gateway_min_replicas)`)
	if err != nil || !got {
		t.Fatalf("1 < 3: got=%v err=%v", got, err)
	}

	above := newReg(t).
		gauge("lenny_gateway_replica_count", 5).
		gauge("lenny_gateway_min_replicas", 3)
	got, err = above.eval().Active(context.Background(), `lenny_gateway_replica_count < scalar(lenny_gateway_min_replicas)`)
	if err != nil || got {
		t.Fatalf("5 < 3 is false: got=%v err=%v", got, err)
	}

	// RHS literal-times-metric arithmetic.
	arith := newReg(t).
		gauge("lenny_pool_bootstrap_target_min_warm", 10).
		gauge("lenny_pool_bootstrap_min_warm_override", 3)
	got, err = arith.eval().Active(context.Background(), `lenny_pool_bootstrap_target_min_warm > 3 * lenny_pool_bootstrap_min_warm_override`)
	if err != nil || !got {
		t.Fatalf("10 > 9: got=%v err=%v", got, err)
	}

	// RHS literal-times-scalar().
	pct := newReg(t).
		gauge("lenny_controller_workqueue_depth", 60).
		gauge("lenny_controller_workqueue_max_depth", 100)
	got, err = pct.eval().Active(context.Background(), `lenny_controller_workqueue_depth > 0.50 * scalar(lenny_controller_workqueue_max_depth)`)
	if err != nil || !got {
		t.Fatalf("60 > 50: got=%v err=%v", got, err)
	}
}

// TestActiveDivision_spec_25_13_4676 covers vector/vector and
// vector/scalar() ratios.
func TestActiveDivision_spec_25_13_4676(t *testing.T) {
	hi := newReg(t).
		gauge("lenny_redis_memory_used_bytes", 900).
		gauge("lenny_redis_maxmemory_bytes", 1000)
	got, err := hi.eval().Active(context.Background(), `lenny_redis_memory_used_bytes / lenny_redis_maxmemory_bytes > 0.80`)
	if err != nil || !got {
		t.Fatalf("0.9 > 0.8: got=%v err=%v", got, err)
	}

	lo := newReg(t).
		gauge("lenny_redis_memory_used_bytes", 500).
		gauge("lenny_redis_maxmemory_bytes", 1000)
	got, err = lo.eval().Active(context.Background(), `lenny_redis_memory_used_bytes / lenny_redis_maxmemory_bytes > 0.80`)
	if err != nil || got {
		t.Fatalf("0.5 > 0.8 is false: got=%v err=%v", got, err)
	}

	sc := newReg(t).
		gauge("lenny_gateway_active_streams", 90).
		gauge("lenny_gateway_stream_ceiling", 100)
	got, err = sc.eval().Active(context.Background(), `lenny_gateway_active_streams / scalar(lenny_gateway_stream_ceiling) > 0.80`)
	if err != nil || !got {
		t.Fatalf("0.9 > 0.8: got=%v err=%v", got, err)
	}
}

// TestActiveUnsupported_spec_25_13_4676 verifies that constructs needing
// a time-series history or label-set joins are reported unsupported so
// the state machine preserves state and leaves them to Prometheus.
func TestActiveUnsupported_spec_25_13_4676(t *testing.T) {
	reg := newReg(t).counter("lenny_quota_redis_fallback_total", 5).
		gauge("lenny_warmpool_idle_pods", 0)
	exprs := []string{
		`rate(lenny_quota_redis_fallback_total[2m]) > 0`,
		`increase(lenny_orphaned_claims_total[15m]) > 10`,
		`time() - lenny_backup_last_successful_timestamp{type="full"} > 172800`,
		`histogram_quantile(0.95, sum by (le) (rate(lenny_checkpoint_duration_seconds_bucket[5m]))) > 2.5`,
		`lenny_audit_siem_configured == 0 and lenny_env_production == 1`,
		`(max by (pool) (lenny_a) > 0) or (max by (tenant_id) (lenny_b) > 0)`,
		`(min by (pool) (lenny_warmpool_idle_pods) == 0) unless on (pool) (lenny_warmpool_fill_grace_active == 1)`,
		`lenny_warmpool_idle_pods / on(pool) group_left lenny_warmpool_min_warm < 0.25`,
		`lenny_runtime_upgrade_state{state=~"expanding|draining"} == 1`,
	}
	for _, expr := range exprs {
		got, err := reg.eval().Active(context.Background(), expr)
		if !errors.Is(err, ErrUnsupportedExpr) {
			t.Fatalf("Active(%q) err = %v, want ErrUnsupportedExpr (got=%v)", expr, err, got)
		}
	}
}

// TestActiveAbsentMetric_spec_25_13_4676 confirms that a metric the
// gateway does not export resolves to inactive without error, matching
// PromQL instant-vector semantics, so external-only alerts never fire
// through the in-process fallback.
func TestActiveAbsentMetric_spec_25_13_4676(t *testing.T) {
	reg := newReg(t).gauge("lenny_present", 1)
	got, err := reg.eval().Active(context.Background(), `kube_deployment_status_replicas_ready{deployment="lenny-agent-dns"} == 0`)
	if err != nil {
		t.Fatalf("absent metric should not error: %v", err)
	}
	if got {
		t.Fatalf("absent metric must be inactive")
	}
}

// TestActiveScalarAbsentPreservesState_spec_25_13_4676 verifies that a
// threshold drawn from an absent scalar metric is unsupported (preserve
// state) rather than firing or resolving on a missing threshold.
func TestActiveScalarAbsentPreservesState_spec_25_13_4676(t *testing.T) {
	reg := newReg(t).gauge("lenny_credential_pool_utilization", 0.9)
	_, err := reg.eval().Active(context.Background(), `lenny_credential_pool_utilization > scalar(lenny_credential_pool_low_threshold)`)
	if !errors.Is(err, ErrUnsupportedExpr) {
		t.Fatalf("absent scalar threshold should be unsupported, got %v", err)
	}
}

// TestActiveNilGatherer_spec_25_13_4676 confirms a nil gatherer never
// fires.
func TestActiveNilGatherer_spec_25_13_4676(t *testing.T) {
	_, err := New(nil).Active(context.Background(), "lenny_x > 0")
	if !errors.Is(err, ErrUnsupportedExpr) {
		t.Fatalf("nil gatherer should be unsupported, got %v", err)
	}
}

// TestEvaluatorStateMachineFires_spec_25_13_4676 is a tier-2 integration
// test: the real inproceval backend drives the §16.5 evaluator state
// machine through inactive → firing → resolved against a live registry,
// invoking the OnFired / OnResolved edge callbacks the gateway wires to
// the operational event stream.
func TestEvaluatorStateMachineFires_spec_25_13_4676(t *testing.T) {
	reg := prometheus.NewRegistry()
	primaryUp := prometheus.NewGauge(prometheus.GaugeOpts{Name: "lenny_postgres_primary_up", Help: "h"})
	primaryUp.Set(0)
	if err := reg.Register(primaryUp); err != nil {
		t.Fatalf("register: %v", err)
	}

	catalog := []rules.Rule{{
		Name:     "PostgresPrimaryDown",
		Expr:     "lenny_postgres_primary_up == 0",
		Severity: rules.SeverityWarning,
		Summary:  "primary unreachable",
	}}

	var fired, resolved int
	e := evaluator.New(catalog, New(reg), evaluator.Options{
		OnFired:    func(evaluator.Alert) { fired++ },
		OnResolved: func(evaluator.Alert) { resolved++ },
	})

	now := time.Unix(0, 0)
	if got := e.Tick(context.Background(), now); got != 1 {
		t.Fatalf("first tick firing count = %d, want 1", got)
	}
	if fired != 1 {
		t.Fatalf("OnFired called %d times, want 1", fired)
	}
	if st, _ := e.State("PostgresPrimaryDown"); st != evaluator.StateFiring {
		t.Fatalf("state = %s, want firing", st)
	}

	// Recover: the primary comes back, the fallback resolves the alert.
	primaryUp.Set(1)
	if got := e.Tick(context.Background(), now.Add(time.Minute)); got != 0 {
		t.Fatalf("post-recovery firing count = %d, want 0", got)
	}
	if resolved != 1 {
		t.Fatalf("OnResolved called %d times, want 1", resolved)
	}
}

// catalogRule returns the bundled §16.5 rule of the given name from the
// authoritative rules.Catalog(), so the firing tests below exercise the
// exact expression the gateway and the Helm-rendered PrometheusRule both
// carry rather than a hand-written copy that could drift from it.
func catalogRule(t *testing.T, name string) rules.Rule {
	t.Helper()
	for _, r := range rules.Catalog() {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("bundled alert %q not found in rules.Catalog()", name)
	return rules.Rule{}
}

// TestBundledUniversalAlertsFireOnTriggerCondition_spec_25_13 drives the
// in-process alert state tracker with the triggering condition of each
// Universal alert (§25.13 line 4731) whose expression the per-replica
// in-process fallback can evaluate, and asserts the named bundled alert
// transitions to StateFiring and appears in the tracker's Firing() set.
//
// It closes the §25.13 gap that no test drove a triggering condition and
// asserted the corresponding bundled alert fires: the store-outage chaos
// tests assert store health but never the alert firing itself.
//
// The rule expressions come from the authoritative rules.Catalog(), the
// backend is the real inproceval evaluator over a live registry, and the
// state machine is the real pkg/alerting/evaluator — the same three
// pieces the spec says the gateway compiles in as the per-replica
// fallback used when Prometheus is unreachable:
//
//	"The gateway binary — the in-process alert state tracker (Section
//	25.3, Health API) evaluates these expressions against the in-process
//	metric registry. This is the per-replica fallback used when
//	Prometheus is unreachable." (§25.13)
//
// spec: §25.13 (bundled rules, single source of truth, in-process
// tracker); §16.5 (alert catalogue); §25.3 (Health API firing-alert set).
func TestBundledUniversalAlertsFireOnTriggerCondition_spec_25_13(t *testing.T) {
	// DualStoreUnavailable: the §10.1 dual-store outage pins
	// lenny_dual_store_unavailable to 1 (For == 0, fires on the first
	// tick). This is the "dual store down" triggering condition.
	t.Run("DualStoreUnavailable", func(t *testing.T) {
		rule := catalogRule(t, "DualStoreUnavailable")
		reg := prometheus.NewRegistry()
		g := prometheus.NewGauge(prometheus.GaugeOpts{Name: "lenny_dual_store_unavailable", Help: "h"})
		reg.MustRegister(g)

		e := evaluator.New([]rules.Rule{rule}, New(reg), evaluator.Options{})
		now := time.Unix(0, 0)

		// Store is healthy: the gauge sits at 0, the alert stays inactive.
		if got := e.Tick(context.Background(), now); got != 0 {
			t.Fatalf("firing count with dual store healthy = %d, want 0", got)
		}
		if firing := e.Firing(); len(firing) != 0 {
			t.Fatalf("Firing() with dual store healthy = %v, want empty", firing)
		}

		// Inject the dual-store outage: the gauge goes to 1.
		g.Set(1)
		if got := e.Tick(context.Background(), now.Add(time.Second)); got != 1 {
			t.Fatalf("firing count under dual-store outage = %d, want 1", got)
		}
		if st, _ := e.State(rule.Name); st != evaluator.StateFiring {
			t.Fatalf("%s state = %s, want %s", rule.Name, st, evaluator.StateFiring)
		}
		if !containsAlert(e.Firing(), rule.Name) {
			t.Fatalf("Firing() = %v, want it to contain %q", e.Firing(), rule.Name)
		}
	})

	// CertExpiryImminent: a managed cert within 1h of expiry drives
	// min(lenny_cert_expiry_seconds) below 3600 (For == 0). This is the
	// "cert near expiry" triggering condition.
	t.Run("CertExpiryImminent", func(t *testing.T) {
		rule := catalogRule(t, "CertExpiryImminent")
		reg := prometheus.NewRegistry()
		gv := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "lenny_cert_expiry_seconds", Help: "h"}, []string{"pod"})
		reg.MustRegister(gv)
		gv.WithLabelValues("gateway-a").Set(7200)
		gv.WithLabelValues("gateway-b").Set(7200)

		e := evaluator.New([]rules.Rule{rule}, New(reg), evaluator.Options{})
		now := time.Unix(0, 0)

		// Both certs have >1h left: the alert stays inactive.
		if got := e.Tick(context.Background(), now); got != 0 {
			t.Fatalf("firing count with certs healthy = %d, want 0", got)
		}

		// One cert's renewal fails and it drops under the hour: the min
		// over the series crosses the threshold and the alert fires.
		gv.WithLabelValues("gateway-b").Set(1800)
		if got := e.Tick(context.Background(), now.Add(time.Second)); got != 1 {
			t.Fatalf("firing count with a cert under 1h = %d, want 1", got)
		}
		if st, _ := e.State(rule.Name); st != evaluator.StateFiring {
			t.Fatalf("%s state = %s, want %s", rule.Name, st, evaluator.StateFiring)
		}
		if !containsAlert(e.Firing(), rule.Name) {
			t.Fatalf("Firing() = %v, want it to contain %q", e.Firing(), rule.Name)
		}
	})

	// SessionStoreUnavailable has a 15s For sustain: the triggering
	// condition (lenny_postgres_primary_up == 0) must hold across the
	// sustain window before the tracker fires. This exercises the
	// pending → firing edge for a bundled Universal alert, not just the
	// instantaneous For == 0 case.
	t.Run("SessionStoreUnavailable_ForSustain", func(t *testing.T) {
		rule := catalogRule(t, "SessionStoreUnavailable")
		if rule.For <= 0 {
			t.Fatalf("expected %s to carry a non-zero For sustain, got %s", rule.Name, rule.For)
		}
		reg := prometheus.NewRegistry()
		primaryUp := prometheus.NewGauge(prometheus.GaugeOpts{Name: "lenny_postgres_primary_up", Help: "h"})
		reg.MustRegister(primaryUp)

		e := evaluator.New([]rules.Rule{rule}, New(reg), evaluator.Options{})
		now := time.Unix(0, 0)

		// Primary goes down: the first tick moves the alert to pending,
		// not firing, because the 15s sustain has not elapsed.
		primaryUp.Set(0)
		if got := e.Tick(context.Background(), now); got != 0 {
			t.Fatalf("firing count at t=0 (within For window) = %d, want 0", got)
		}
		if st, _ := e.State(rule.Name); st != evaluator.StatePending {
			t.Fatalf("%s state within For window = %s, want %s", rule.Name, st, evaluator.StatePending)
		}

		// After the sustain window the alert fires.
		if got := e.Tick(context.Background(), now.Add(rule.For)); got != 1 {
			t.Fatalf("firing count after For sustain = %d, want 1", got)
		}
		if !containsAlert(e.Firing(), rule.Name) {
			t.Fatalf("Firing() = %v, want it to contain %q after the sustain window", e.Firing(), rule.Name)
		}
	})

	// WarmPoolExhausted names "warm pool == 0 for >60s" as its triggering
	// condition, but its bundled expression uses `unless on (pool)`, a
	// label-set join the in-process fallback cannot evaluate against a
	// single registry snapshot. The fallback therefore reports the
	// expression unsupported, the state machine preserves state, and the
	// alert never fires through the in-process tracker: it is evaluated by
	// Prometheus, whose TSDB carries the series the join needs. Asserting
	// the negative here documents the split and guards against a future
	// change that silently makes this alert fire (or misfire) on the
	// fallback path.
	t.Run("WarmPoolExhausted_leftToPrometheus", func(t *testing.T) {
		rule := catalogRule(t, "WarmPoolExhausted")
		reg := prometheus.NewRegistry()
		gv := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "lenny_warmpool_idle_pods", Help: "h"}, []string{"pool"})
		reg.MustRegister(gv)
		gv.WithLabelValues("default").Set(0) // the triggering condition

		// The expression is unsupported by the in-process fallback.
		if _, err := New(reg).Active(context.Background(), rule.Expr); !errors.Is(err, ErrUnsupportedExpr) {
			t.Fatalf("%s expression should be unsupported by the in-process fallback, got err=%v", rule.Name, err)
		}

		// Driven through the state machine, the unsupported expression
		// leaves the alert inactive rather than firing.
		e := evaluator.New([]rules.Rule{rule}, New(reg), evaluator.Options{})
		now := time.Unix(0, 0)
		e.Tick(context.Background(), now)
		e.Tick(context.Background(), now.Add(2*rule.For))
		if st, _ := e.State(rule.Name); st != evaluator.StateInactive {
			t.Fatalf("%s state on the in-process fallback = %s, want %s (evaluated by Prometheus)",
				rule.Name, st, evaluator.StateInactive)
		}
		if firing := e.Firing(); len(firing) != 0 {
			t.Fatalf("Firing() = %v, want empty: %s is a Prometheus-evaluated alert", firing, rule.Name)
		}
	})
}

// containsAlert reports whether the tracker's Firing() set names alert.
func containsAlert(firing []string, alert string) bool {
	for _, n := range firing {
		if n == alert {
			return true
		}
	}
	return false
}

// TestCatalogNeverPanics_spec_25_13_4676 runs every bundled §16.5 rule
// expression through the evaluator against an empty registry. Each must
// resolve to inactive or report ErrUnsupportedExpr — never a parser
// panic and never a spurious fire on an empty registry.
func TestCatalogNeverPanics_spec_25_13_4676(t *testing.T) {
	reg := newReg(t)
	e := reg.eval()
	for _, r := range rules.Catalog() {
		got, err := e.Active(context.Background(), r.Expr)
		if err != nil && !errors.Is(err, ErrUnsupportedExpr) {
			t.Fatalf("rule %q: unexpected error %v", r.Name, err)
		}
		if err == nil && got {
			t.Fatalf("rule %q fired against an empty registry", r.Name)
		}
	}
}
