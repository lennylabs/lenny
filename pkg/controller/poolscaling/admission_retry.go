// SPDX-License-Identifier: MIT

package poolscaling

import (
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// admissionDeniedTotal is the §16.1
// lenny_pool_scaling_admission_denied_total counter the §16.5
// PoolScalingAdmissionStuck alert evaluates against. It is labeled
// by namespace and pool so the alert and downstream dashboards can
// pinpoint the stuck row. Registration is package-level so the
// controller-runtime metrics registry exposes it on the controller's
// existing /metrics endpoint without extra wiring per Reconciler.
var admissionDeniedTotal = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_pool_scaling_admission_denied_total",
		Help: "PoolScalingController admission rejections by reason.",
	}, []string{"namespace", "pool"})
	if err != nil {
		panic(fmt.Sprintf("poolscaling: build admission_denied counter: %v", err))
	}
	ctrlmetrics.Registry.MustRegister(c)
	return c
}()

// DefaultAdmissionDeniedRetryCeiling is the §4.6.2 default number of
// consecutive admission rejections at which a pool is marked stuck.
// The PoolScalingAdmissionStuck alert (alert catalog §16.5) fires
// when a pool stays in this state for the alert's `for:` window.
const DefaultAdmissionDeniedRetryCeiling = 5

// admissionRetryState tracks consecutive admission-webhook rejections
// per pool key. A non-admission failure (transport, server timeout,
// validation error from the Postgres source) does not increment the
// counter; only Forbidden / 403 from the
// `lenny-pool-config-validator` admission webhook counts as an
// admission denial under §4.6.2.
//
// The struct is safe for concurrent use by a single Reconciler whose
// Sync is invoked from a leader-elected runnable. Resetting on success
// is part of the same Sync pass: a sync that lands cleanly clears
// every counter for the touched pool key.
type admissionRetryState struct {
	mu           sync.Mutex
	consecutive  map[string]int
	retryCeiling int
}

// newAdmissionRetryState constructs a retry tracker with the supplied
// retry ceiling. A non-positive ceiling falls back to
// DefaultAdmissionDeniedRetryCeiling so a misconfigured runner does
// not silently disable the gate.
func newAdmissionRetryState(retryCeiling int) *admissionRetryState {
	if retryCeiling <= 0 {
		retryCeiling = DefaultAdmissionDeniedRetryCeiling
	}
	return &admissionRetryState{
		consecutive:  map[string]int{},
		retryCeiling: retryCeiling,
	}
}

// recordOutcome updates the counter for the supplied pool key. When
// err is nil or is a non-admission failure, the counter is cleared so
// a recovering pool exits the stuck state on its first clean Sync.
// When err is an admission rejection the counter is incremented and
// the returned `stuck` reports whether the ceiling has been reached.
//
// Every admission rejection also increments the §16.1
// lenny_pool_scaling_admission_denied_total Prometheus counter the
// §16.5 PoolScalingAdmissionStuck alert reads, with the labels
// (namespace, pool) parsed back out of the key.
func (s *admissionRetryState) recordOutcome(key string, err error) (stuck bool, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil || !isAdmissionRejection(err) {
		// Any successful Sync or non-admission failure resets the
		// counter. A non-admission failure is a separate fault category;
		// the §16.5 alert is specific to webhook rejections.
		delete(s.consecutive, key)
		return false, 0
	}
	s.consecutive[key]++
	count = s.consecutive[key]
	ns, name := splitPoolKey(key)
	admissionDeniedTotal.WithLabelValues(ns, name).Inc()
	return count >= s.retryCeiling, count
}

// splitPoolKey reverses poolKey: the key is `<namespace>/<name>`.
func splitPoolKey(key string) (namespace, name string) {
	for i := range key {
		if key[i] == '/' {
			return key[:i], key[i+1:]
		}
	}
	return "", key
}

// StuckPools returns the list of pool keys at or over the retry
// ceiling. The PoolScalingController exposes this through its public
// API so an alerting integration can scrape the set; a pool exits the
// list on its first clean Sync.
func (s *admissionRetryState) StuckPools() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for k, n := range s.consecutive {
		if n >= s.retryCeiling {
			out = append(out, k)
		}
	}
	return out
}

// ConsecutiveDenials returns the current consecutive-denial count for
// the supplied pool key. Zero when no denial has been recorded.
func (s *admissionRetryState) ConsecutiveDenials(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consecutive[key]
}

// isAdmissionRejection reports whether err is a Kubernetes admission
// webhook rejection. The webhook returns 403 Forbidden via the
// AdmissionResponse.Allowed=false path; the API server surfaces it as
// a Forbidden status error to the client.
func isAdmissionRejection(err error) bool {
	if err == nil {
		return false
	}
	return apierrors.IsForbidden(err) || apierrors.IsInvalid(err)
}

// poolKey is the §16.5 alert label tuple: namespace/name. The alert
// joins on these labels so a stuck pool surfaces as one line in the
// firing-alerts view.
func poolKey(namespace, name string) string {
	return namespace + "/" + name
}
