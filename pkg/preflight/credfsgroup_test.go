// SPDX-License-Identifier: MIT

package preflight_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/preflight"
)

const credReadersGID int64 = 65534

func i64(v int64) *int64 { return &v }

// spec: §13.1 line 25 — a compliant agent pod carries the
// lenny-cred-readers fsGroup and supplementalGroups. F-13.1.4.
func TestCheckAgentPodCredFSGroupPassesForCompliantPods(t *testing.T) {
	d := preflight.CheckAgentPodCredFSGroup([]preflight.AgentPodSpec{
		{
			HostSharingPodSpec: preflight.HostSharingPodSpec{Workload: "lenny-agents/Pod/alice-0"},
			FSGroup:            i64(credReadersGID),
			SupplementalGroups: []int64{credReadersGID},
		},
	}, credReadersGID)
	if !d.Passed {
		t.Errorf("compliant agent pod was rejected: %s", d.Reason)
	}
}

// A fresh install has no agent pods; the audit must pass cleanly so it
// never blocks a first install. F-13.1.4.
func TestCheckAgentPodCredFSGroupPassesForEmptyInput(t *testing.T) {
	if d := preflight.CheckAgentPodCredFSGroup(nil, credReadersGID); !d.Passed {
		t.Errorf("an empty agent-pod set was rejected: %s", d.Reason)
	}
}

// spec: §13.1 line 25 — a pod with no pod-level fsGroup is rejected with
// POD_SPEC_CRED_FSGROUP_MISSING. F-13.1.4.
func TestCheckAgentPodCredFSGroupRejectsMissingFSGroup(t *testing.T) {
	d := preflight.CheckAgentPodCredFSGroup([]preflight.AgentPodSpec{
		{
			HostSharingPodSpec: preflight.HostSharingPodSpec{Workload: "lenny-agents/Pod/alice-0"},
			SupplementalGroups: []int64{credReadersGID},
		},
	}, credReadersGID)
	if d.Passed {
		t.Fatal("an agent pod with no fsGroup was accepted")
	}
	if !strings.Contains(d.Reason, "POD_SPEC_CRED_FSGROUP_MISSING") || !strings.Contains(d.Reason, "alice-0") {
		t.Errorf("reason %q does not carry the §13.1 code and offending pod", d.Reason)
	}
}

func TestCheckAgentPodCredFSGroupRejectsWrongFSGroup(t *testing.T) {
	d := preflight.CheckAgentPodCredFSGroup([]preflight.AgentPodSpec{
		{
			HostSharingPodSpec: preflight.HostSharingPodSpec{Workload: "lenny-agents/Pod/alice-0"},
			FSGroup:            i64(1000),
			SupplementalGroups: []int64{credReadersGID},
		},
	}, credReadersGID)
	if d.Passed || !strings.Contains(d.Reason, "POD_SPEC_CRED_FSGROUP_MISSING") {
		t.Errorf("a wrong fsGroup was not rejected: %+v", d)
	}
}

// spec: §13.1 line 25 — the explicit supplementalGroups membership is
// required, not merely the kubelet's implicit fsGroup propagation. F-13.1.4.
func TestCheckAgentPodCredFSGroupRejectsMissingSupplementalGroup(t *testing.T) {
	d := preflight.CheckAgentPodCredFSGroup([]preflight.AgentPodSpec{
		{
			HostSharingPodSpec: preflight.HostSharingPodSpec{Workload: "lenny-agents/Pod/alice-0"},
			FSGroup:            i64(credReadersGID),
			SupplementalGroups: []int64{1000},
		},
	}, credReadersGID)
	if d.Passed || !strings.Contains(d.Reason, "supplementalGroups") {
		t.Errorf("a missing supplementalGroups membership was not rejected: %+v", d)
	}
}

func TestCheckAgentPodCredFSGroupRejectsTheOffendingPodAmongMany(t *testing.T) {
	d := preflight.CheckAgentPodCredFSGroup([]preflight.AgentPodSpec{
		{HostSharingPodSpec: preflight.HostSharingPodSpec{Workload: "lenny-agents/Pod/alice-0"}, FSGroup: i64(credReadersGID), SupplementalGroups: []int64{credReadersGID}},
		{HostSharingPodSpec: preflight.HostSharingPodSpec{Workload: "lenny-agents/Pod/bob-0"}, FSGroup: nil},
		{HostSharingPodSpec: preflight.HostSharingPodSpec{Workload: "lenny-agents/Pod/carol-0"}, FSGroup: i64(credReadersGID), SupplementalGroups: []int64{credReadersGID}},
	}, credReadersGID)
	if d.Passed {
		t.Fatal("a set containing a non-compliant agent pod was accepted")
	}
	if !strings.Contains(d.Reason, "bob-0") {
		t.Errorf("reason %q does not name the offending pod", d.Reason)
	}
}
