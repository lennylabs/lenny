// SPDX-License-Identifier: MIT

package sandbox

import (
	"fmt"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// The warm-up error types the WarmPoolController emits on
// lenny_warmpool_warmup_failure_total. §16.1 lists a fourth,
// setup_command_failed, which counts a session whose setup commands fail
// and which the gateway emits, because setup runs per session after the
// pod is claimed.
//
// spec: §16.1 (Metrics, the warm-up failure row).
const (
	warmupErrorImagePull     = "image_pull_error"
	warmupErrorQuotaExceeded = "resource_quota_exceeded"
	warmupErrorNodePressure  = "node_pressure"
)

// warmupFailureTotal is the controller half of the §16.1
// lenny_warmpool_warmup_failure_total counter, labeled by pool,
// runtime_class, and error_type. It counts a pod that fails to reach idle
// during warm-up. It is registered against the controller-runtime metrics
// registry because the WarmPoolController and the gateway run as separate
// processes. The §16.5 WarmPoolReplenishmentFailing and
// PoolScaleoutBlockedByQuota alerts read it.
//
// spec: §16.1 (Metrics); §16.5 (Alerting Rules and SLOs); §17.2 (Namespace
// Layout, the ResourceQuota paragraph).
var warmupFailureTotal = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_warmpool_warmup_failure_total",
		Help: "Warm-pool pods that failed to reach idle during warm-up, by error_type (§16.1).",
	}, []string{"pool", "runtime_class", "error_type"})
	if err != nil {
		panic(fmt.Sprintf("sandbox: build warmup-failure counter: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, c)
	return c
}()

// imagePullWaitingReasons are the kubelet container waiting reasons that
// mean the container image could not be pulled.
var imagePullWaitingReasons = map[string]bool{
	"ErrImagePull":      true,
	"ImagePullBackOff":  true,
	"InvalidImageName":  true,
	"ErrImageNeverPull": true,
}

// classifyCreateError reports the warm-up error type of a rejected pod
// creation. The ResourceQuota admission plugin answers a creation that
// would exceed a namespace quota with Forbidden and an "exceeded quota"
// message, which is the resource_quota_exceeded case. Any other error is
// not a §16.1 warm-up error type.
//
// spec: §16.1 (Metrics); §17.2 (Namespace Layout, the ResourceQuota
// paragraph).
func classifyCreateError(err error) (string, bool) {
	if err == nil || !apierrors.IsForbidden(err) {
		return "", false
	}
	if strings.Contains(err.Error(), "exceeded quota") {
		return warmupErrorQuotaExceeded, true
	}
	return "", false
}

// classifyWarmupPod reports the warm-up error type a warming pod's status
// shows. A kubelet node-pressure eviction fails the pod with reason
// Evicted, which is node_pressure. A container of the pod waiting on an
// image pull failure is image_pull_error. A pod showing neither has no
// §16.1 warm-up error type.
//
// spec: §16.1 (Metrics).
func classifyWarmupPod(pod *corev1.Pod) (string, bool) {
	if pod == nil {
		return "", false
	}
	if pod.Status.Phase == corev1.PodFailed && pod.Status.Reason == "Evicted" {
		return warmupErrorNodePressure, true
	}
	for _, statuses := range [][]corev1.ContainerStatus{pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses} {
		for i := range statuses {
			if w := statuses[i].State.Waiting; w != nil && imagePullWaitingReasons[w.Reason] {
				return warmupErrorImagePull, true
			}
		}
	}
	return "", false
}

// inWarmup reports whether a Sandbox phase is a warm-up phase, in which
// the pod has not yet reached idle.
//
// spec: §6.2 (Pod State Machine).
func inWarmup(phase string) bool {
	switch state.State(phase) {
	case "", state.Warming, state.SDKConnecting:
		return true
	}
	return false
}

// podRuntimeClass returns the RuntimeClass name a pod runs under, or the
// empty string when the pod names none.
func podRuntimeClass(pod *corev1.Pod) string {
	if pod != nil && pod.Spec.RuntimeClassName != nil {
		return *pod.Spec.RuntimeClassName
	}
	return ""
}

// warmupFailureMeter counts each warm-up failure once. A reconcile is
// level-triggered, so a pod stuck in an image pull failure, or a Sandbox
// whose pod creation the quota keeps rejecting, is observed on every
// reconcile. The meter records the failure it last counted for each
// Sandbox and counts again only when the failing pod differs. The record
// is in memory, so a controller restart counts a failure still in
// progress one more time.
type warmupFailureMeter struct {
	// mu guards counted.
	mu sync.Mutex
	// counted maps a Sandbox to the failure last counted for it: the
	// failing pod's UID, or createRejectedKey for a rejected creation.
	counted map[types.NamespacedName]string
	// inc increments the counter. Tests substitute it.
	inc func(pool, runtimeClass, errorType string)
}

// createRejectedKey marks a counted pod-creation rejection, which has no
// pod UID.
const createRejectedKey = "create-rejected"

func newWarmupFailureMeter() *warmupFailureMeter {
	return &warmupFailureMeter{
		counted: map[types.NamespacedName]string{},
		inc: func(pool, runtimeClass, errorType string) {
			warmupFailureTotal.WithLabelValues(pool, runtimeClass, errorType).Inc()
		},
	}
}

// record counts one failure for the Sandbox under key unless the same
// failure is already counted.
func (m *warmupFailureMeter) record(sb *lennyv1.Sandbox, key, runtimeClass, errorType string) {
	id := types.NamespacedName{Namespace: sb.Namespace, Name: sb.Name}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counted[id] == key {
		return
	}
	m.counted[id] = key
	m.inc(sb.Spec.PoolRef, runtimeClass, errorType)
}

// observePod counts a warming pod whose status shows a §16.1 warm-up
// error type. A Sandbox past warm-up drops its record, so the map holds
// only Sandboxes still warming.
func (m *warmupFailureMeter) observePod(sb *lennyv1.Sandbox, pod *corev1.Pod, podPresent bool) {
	if !inWarmup(sb.Status.Phase) {
		m.forget(types.NamespacedName{Namespace: sb.Namespace, Name: sb.Name})
		return
	}
	if !podPresent {
		return
	}
	errorType, ok := classifyWarmupPod(pod)
	if !ok {
		return
	}
	m.record(sb, string(pod.UID), podRuntimeClass(pod), errorType)
}

// observeCreateError counts a pod creation the API server rejected with a
// §16.1 warm-up error type.
func (m *warmupFailureMeter) observeCreateError(sb *lennyv1.Sandbox, pod *corev1.Pod, err error) {
	errorType, ok := classifyCreateError(err)
	if !ok {
		return
	}
	m.record(sb, createRejectedKey, podRuntimeClass(pod), errorType)
}

// forget drops the record for a Sandbox that left warm-up or was deleted.
func (m *warmupFailureMeter) forget(id types.NamespacedName) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.counted, id)
}
