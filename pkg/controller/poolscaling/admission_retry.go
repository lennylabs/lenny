// SPDX-License-Identifier: MIT

package poolscaling

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// admissionDeniedTotal is the §16.1
// lenny_pool_scaling_admission_denied_total counter the §16.5
// PoolScalingAdmissionStuck alert evaluates against. Per §4.6.2 item 1
// it is labeled by pool, crd ∈ {SandboxTemplate, SandboxWarmPool}, and
// the webhook-supplied reason code. The HTTP status class is captured
// on the structured log event but deliberately not on the metric: all
// 4xx denials share the single admission_denied retry path, so the
// actionable distinction for operators is the reason, not the status.
// Registration is package-level so the controller-runtime metrics
// registry exposes it on the controller's existing /metrics endpoint
// without extra wiring per Reconciler.
var admissionDeniedTotal = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_pool_scaling_admission_denied_total",
		Help: "PoolScalingController admission rejections by pool, crd, and reason.",
	}, []string{"pool", "crd", "reason"})
	if err != nil {
		panic(fmt.Sprintf("poolscaling: build admission_denied counter: %v", err))
	}
	ctrlmetrics.Registry.MustRegister(c)
	return c
}()

// DefaultAdmissionDeniedRetryCeiling is the §4.6.2 item 3 default
// number of consecutive admission rejections on the same (pool, crd)
// tuple at which the PSC stops retrying and the pool is marked stuck.
// The PoolScalingAdmissionStuck alert (§16.5) fires when a pool stays
// in this state for the alert's `for:` window.
const DefaultAdmissionDeniedRetryCeiling = 10

// backoffBase is the §4.6.2 item 2 first-denial pause; the schedule
// doubles on each consecutive denial up to backoffCeiling.
const (
	backoffBase    = 1 * time.Second
	backoffCeiling = 60 * time.Second
	defaultReason  = "admission_denied"
	crdSandboxTmpl = "SandboxTemplate"
	crdSandboxPool = "SandboxWarmPool"
)

// denialKey is the §4.6.2 per-tuple backoff key: a denial on one
// (pool, crd) never blocks reconciliation of another tuple.
type denialKey struct {
	namespace string
	pool      string
	crd       string
}

// denialEntry tracks the consecutive-denial count and the time before
// which the tuple must not be re-synced (§4.6.2 item 2). A tuple at or
// over the retry ceiling is stuck (§4.6.2 item 3): it is not re-synced
// until the entry is cleared by a config change, a leader handoff (the
// state is in-memory), or an operator resume-reconciliation call.
type denialEntry struct {
	consecutive  int
	backoffUntil time.Time
}

// admissionRetryState tracks consecutive admission-webhook rejections
// per (namespace, pool, crd) tuple and the per-tuple backoff window. A
// non-admission failure (transport, server timeout, internal error)
// does not increment the counter; only a 4xx admission denial from the
// lenny-pool-config-validator webhook counts under §4.6.2.
//
// The struct is safe for concurrent use: the Sync pass mutates it from
// the leader-elected runnable while the admin resume-reconciliation
// handler clears entries from a gateway request goroutine.
type admissionRetryState struct {
	mu           sync.Mutex
	entries      map[denialKey]*denialEntry
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
		entries:      map[denialKey]*denialEntry{},
		retryCeiling: retryCeiling,
	}
}

// readyToSync reports whether the tuple may be synced now. A tuple
// with no recorded denial is always ready. A tuple in backoff is not
// ready until now reaches its backoffUntil. A stuck tuple (at or over
// the ceiling) is never ready until its entry is cleared (§4.6.2
// item 3): backoff alone would let a stuck pool resume after 60s, so
// the ceiling gate is checked first.
func (s *admissionRetryState) readyToSync(key denialKey, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		return true
	}
	if e.consecutive >= s.retryCeiling {
		return false
	}
	return !now.Before(e.backoffUntil)
}

// recordOutcome updates the tuple's denial state. When err is nil or a
// non-admission failure, the entry is cleared so a recovering tuple
// exits backoff on its first clean Sync. When err is an admission
// rejection the consecutive count increments, the backoff doubles
// (capped at backoffCeiling), the §16.1 counter increments labeled by
// (pool, crd, reason), and the returned stuck reports whether the
// ceiling has been reached.
func (s *admissionRetryState) recordOutcome(key denialKey, err error, now time.Time) (stuck bool, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil || !isAdmissionRejection(err) {
		delete(s.entries, key)
		return false, 0
	}
	e := s.entries[key]
	if e == nil {
		e = &denialEntry{}
		s.entries[key] = e
	}
	e.consecutive++
	e.backoffUntil = now.Add(backoffDuration(e.consecutive))
	admissionDeniedTotal.WithLabelValues(key.pool, key.crd, reasonFromError(err)).Inc()
	return e.consecutive >= s.retryCeiling, e.consecutive
}

// backoffDuration returns the §4.6.2 item 2 pause for the nth
// consecutive denial: 1s, 2s, 4s, ... doubling to a 60s ceiling. The
// shift is guarded so a large count cannot overflow the duration.
func backoffDuration(consecutive int) time.Duration {
	if consecutive < 1 {
		consecutive = 1
	}
	if consecutive > 7 {
		// 1s << 6 = 64s already exceeds the ceiling; beyond that the
		// shift risks overflow, so clamp directly.
		return backoffCeiling
	}
	d := backoffBase << uint(consecutive-1)
	if d > backoffCeiling {
		return backoffCeiling
	}
	return d
}

// resumePool clears the denial state for every (crd) tuple of the
// named pool, implementing §4.6.2 item 3 condition (c): an operator
// POST /v1/admin/pools/{name}/resume-reconciliation resets the
// in-memory denial counter without requiring a configuration change.
// It returns the number of tuples cleared so the admin handler can
// report whether the pool was actually stuck.
func (s *admissionRetryState) resumePool(namespace, name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	cleared := 0
	for k := range s.entries {
		if k.namespace == namespace && k.pool == name {
			delete(s.entries, k)
			cleared++
		}
	}
	return cleared
}

// stuckPools returns the <namespace>/<name> keys with at least one crd
// tuple at or over the retry ceiling. A pool is reported once even if
// both its tuples are stuck. A tuple exits the set on its first clean
// Sync or an operator resume.
func (s *admissionRetryState) stuckPools() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]struct{}{}
	out := []string{}
	for k, e := range s.entries {
		if e.consecutive >= s.retryCeiling {
			pk := k.namespace + "/" + k.pool
			if _, dup := seen[pk]; !dup {
				seen[pk] = struct{}{}
				out = append(out, pk)
			}
		}
	}
	return out
}

// consecutiveDenials returns the highest consecutive-denial count
// across the pool's crd tuples. Zero when no denial has been recorded.
func (s *admissionRetryState) consecutiveDenials(namespace, name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	max := 0
	for k, e := range s.entries {
		if k.namespace == namespace && k.pool == name && e.consecutive > max {
			max = e.consecutive
		}
	}
	return max
}

// reasonCodePattern matches the leading machine-readable failure code
// the validator prefixes onto its rejection message (for example
// `INVALID_POOL_CONFIGURATION`).
var reasonCodePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// reasonFromError extracts the webhook-supplied reason code from an
// admission rejection for the §16.1 metric label. The lenny-pool-
// config-validator prefixes its denial message with the code followed
// by ": " (pool_config_validator.reject), and the API server wraps the
// webhook response in a "...denied the request: " envelope. The reason
// is the first identifier token after that envelope. A denial that did
// not arrive through the webhook envelope (a field-level Invalid, a
// generic Forbidden) carries no validator code, so it falls back to
// the generic admission_denied label. This keeps the metric's reason
// dimension bounded to the validator's known codes rather than
// free-form status text.
func reasonFromError(err error) string {
	msg := ""
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		msg = status.Status().Message
	}
	if msg == "" {
		msg = err.Error()
	}
	i := strings.LastIndex(msg, "denied the request: ")
	if i < 0 {
		return defaultReason
	}
	msg = msg[i+len("denied the request: "):]
	head, _, found := strings.Cut(msg, ":")
	if !found {
		return defaultReason
	}
	head = strings.TrimSpace(head)
	if reasonCodePattern.MatchString(head) {
		return head
	}
	return defaultReason
}

// isAdmissionRejection reports whether err is a Kubernetes admission
// webhook rejection. The validator returns the denial via the
// AdmissionResponse.Allowed=false path which the API server surfaces
// as a Forbidden (403) or Invalid (422) status error to the client.
func isAdmissionRejection(err error) bool {
	if err == nil {
		return false
	}
	return apierrors.IsForbidden(err) || apierrors.IsInvalid(err) || apierrors.IsBadRequest(err)
}
