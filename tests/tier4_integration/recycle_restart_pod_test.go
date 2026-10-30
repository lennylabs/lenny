// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import "testing"

// restartPodCase is one outcome kind of the supervised scrub sequence on a
// `restart` pod: the scenario the case drives and the outcome it asserts.
type restartPodCase struct {
	name     string
	scenario string
	asserts  string
}

// restartPodCases lists one case per outcome kind of the §5.2 Supervised
// scrub sequence table, and the edit from `keep` to `restart` that the
// runtime-process acknowledgment governs.
var restartPodCases = []restartPodCase{
	{
		name:     "attested end",
		scenario: "a clean release of the last session on a restart pod; the supervisor attests the end and the sweep",
		asserts:  "ReportPodScrub carries runtime_live true and podscrub.Decide reuses the pod",
	},
	{
		name:     "failed end",
		scenario: "the supervisor replies to end with an error",
		asserts:  "the pod retires under onScrubFailure warn",
	},
	{
		name:     "failed sweep",
		scenario: "the sweep worker reports a failure",
		asserts:  "the pod retires under onScrubFailure warn",
	},
	{
		name:     "sweep walk bound",
		scenario: "the sweep walk reaches its bound",
		asserts:  "the pod retires under onScrubFailure warn",
	},
	{
		name:     "cleanup error",
		scenario: "the cleaned reply carries an error",
		asserts:  "the scrub fails under onScrubFailure, and the second end and sweep still run",
	},
	{
		name: "keep to restart edit",
		scenario: "an edit from keep to restart that drops acknowledgeProcessLevelIsolation on a sequential pool " +
			"holding a used keep pod, then a restart pod created after the edit",
		asserts: "an acquisition refuses and drains the used keep pod and admits the restart pod",
	},
}

// TestRecyclePathOnRestartPod_spec_5_2 drives the recycle path on a
// `restart` pod, one case per outcome kind of the supervised scrub sequence.
//
// spec: §5.2 (Supervised scrub sequence; Runtime-process lifetime; Deployer acknowledgment (runtime process kept across sessions))
// diagnosis: a failure means a `restart` pod's whole-pod scrub reports an
// outcome other than the supervised scrub sequence table states, retires a
// pod it should reuse or reuses one it should retire, skips the second end
// and sweep after a cleanup failure, or an acquisition admits a used `keep`
// pod after an edit to `restart` that removed the acknowledgment.
func TestRecyclePathOnRestartPod_spec_5_2(t *testing.T) {
	for _, tc := range restartPodCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("scenario: %s; asserts: %s", tc.scenario, tc.asserts)
			// Gateway pool admission refuses recycle.runtimeProcess: restart
			// until proposal 0084 rebinds the intra-pod MCP servers per
			// runtime generation, so no `restart` pool is admitted for these
			// cases to run on. Proposal 0084 removes the refusal and this skip
			// together.
			t.Skip("blocked: pool admission refuses recycle.runtimeProcess restart until proposal 0084 rebinds the intra-pod MCP servers per runtime generation")
		})
	}
}
