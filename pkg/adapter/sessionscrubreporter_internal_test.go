// SPDX-License-Identifier: MIT

package adapter

import "testing"

// spec: §4.7, §5.2 (adapter pod identity) — New reads the adapter's own pod
// name from the Downward API POD_NAME env and caches it as the pod identity the
// ReportSessionScrub and ReportPodScrub report paths key on. F-5.2.31.
//
// diagnosis: a failure means the adapter does not cache its pod identity from
// the Downward API env, so every session- and pod-scrub report carries the
// wrong pod_id (or an empty one the gateway rejects InvalidArgument), silently
// disabling sessions_served advancement, the leak ledger, and the whole-pod
// scrub trigger.
func TestNewCachesPodIDFromEnv_spec_5_2(t *testing.T) {
	t.Setenv(podNameEnvVar, "claude-code-pool-abc123")
	s := New("podid-test")
	if s.podID != "claude-code-pool-abc123" {
		t.Errorf("podID = %q, want the POD_NAME env value; the report paths key on the wrong pod identity", s.podID)
	}
}

// spec: §4.7, §5.2 (adapter pod identity, fail-closed pod id) — with POD_NAME
// unset (a missing or misnamed Downward API env), New leaves the cached podID
// empty rather than inventing an identity, so the gateway rejects every report
// InvalidArgument and the whole scrub-report chain is disabled fail-closed
// rather than reporting under an empty key. F-5.2.31.
//
// diagnosis: a failure means the adapter fabricated a pod identity when the
// Downward API env is absent, so a broken pod-spec env is not surfaced and the
// scrub-report chain runs against a wrong key instead of failing closed.
func TestNewLeavesPodIDEmptyWhenEnvUnset_spec_5_2(t *testing.T) {
	t.Setenv(podNameEnvVar, "")
	s := New("podid-test")
	if s.podID != "" {
		t.Errorf("podID = %q, want empty when POD_NAME is unset; the adapter must not fabricate a pod identity", s.podID)
	}
}
