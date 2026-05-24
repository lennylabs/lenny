// SPDX-License-Identifier: MIT

package adapter

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// §4.7 Adapter-Agent Security Boundary counters
// (spec/04_system-components.md lines 870-888). Both are registered with
// the default Prometheus registry at package init so they appear on the
// adapter's metrics endpoint once a scrape target is wired.
var (
	soPeercredSelftestFailed = mustCounter(prometheus.CounterOpts{
		Name: "lenny_adapter_sopeercred_selftest_failed_total",
		Help: "SO_PEERCRED startup self-test failures (§4.7). A non-zero value " +
			"means the adapter exited before signalling READY.",
	})
	soPeercredDisabled = mustCounter(prometheus.CounterOpts{
		Name: "lenny_adapter_sopeercred_disabled_total",
		Help: "Pod starts in nonce-only mode with --require-so-peercred=false " +
			"(§4.7). Deployers MUST alert on a non-zero value.",
	})
)

// mustCounter builds and registers a label-free counter, panicking on a
// §16.1.1 naming violation. The empty label set keeps the metric a single
// time series; callers use WithLabelValues() with no arguments.
func mustCounter(opts prometheus.CounterOpts) *prometheus.CounterVec {
	c, err := metrics.NewCounter(opts, nil)
	if err != nil {
		panic(err)
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, c)
	return c
}

// IncSoPeercredSelftestFailed increments the §4.7 self-test failure
// counter. The adapter calls it on the fatal self-test path before exit.
func IncSoPeercredSelftestFailed() { soPeercredSelftestFailed.WithLabelValues().Inc() }

// IncSoPeercredDisabled increments the §4.7 nonce-only-mode counter. The
// adapter calls it once on every pod start when --require-so-peercred is
// false.
func IncSoPeercredDisabled() { soPeercredDisabled.WithLabelValues().Inc() }
