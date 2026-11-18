// SPDX-License-Identifier: MIT

package podspec

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	// PodNameEnvVar is the Downward API environment variable the adapter
	// reads to learn its own pod name. spec: §4.7, §5.2 — the adapter reports
	// the outcome of a cleanup a `Shutdown` performs to reclaim a slot that
	// reached `running` via ReportSessionScrub and the whole-pod scrub outcome
	// via ReportPodScrub, both keyed on the pod identity. A session Shutdown
	// carries no podId and the recycle Shutdown carries it only inside
	// RecycleScrub, so the adapter takes its pod identity from this Downward
	// API env and caches it. An absent or misnamed env yields an empty cached
	// podID, which the gateway rejects InvalidArgument, so the name is load-bearing.
	PodNameEnvVar = "POD_NAME"
)

// podNameEnv returns the Downward API POD_NAME env var the adapter reads
// to learn its own pod name. spec: §4.7, §5.2 — the adapter (the pod's
// gateway-facing process) reports the outcome of a cleanup a `Shutdown`
// performs to reclaim a slot that reached `running` via ReportSessionScrub
// and the whole-pod scrub outcome via ReportPodScrub, both keyed on the pod
// identity, and caches it off this env. Both deployment models mount it: in
// the sidecar model the adapter container is that process; in the embedded
// model the single runtime container is the adapter. An absent env yields an
// empty cached podID, which the gateway rejects InvalidArgument, silently
// disabling the scrub-report chain, so the env is load-bearing on both models.
func podNameEnv() corev1.EnvVar {
	return corev1.EnvVar{
		Name: PodNameEnvVar,
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
		},
	}
}
