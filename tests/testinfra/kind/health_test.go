// SPDX-License-Identifier: MIT

package kind

import (
	"strings"
	"testing"
	"time"
)

// spec: 13.2 (network isolation — minimum CNI requirement)
//
// TestClassifyPodsSeparatesRestartLoopFromSettling pins the distinction
// the tier-5 bootstrap gate draws. Before it existed, a control plane or
// CNI that was OOMKilling itself in a restart loop was reported through
// the same "not Ready" path as a component that had simply not finished
// starting, so every dependent suite skipped with a generic install hint
// and the cluster defect stayed invisible across days of runs.
func TestClassifyPodsSeparatesRestartLoopFromSettling(t *testing.T) {
	cases := []struct {
		name string
		pods []podStatus
		want fleetVerdict
	}{
		{
			name: "no pods is absent",
			pods: nil,
			want: fleetAbsent,
		},
		{
			name: "all running and ready is healthy",
			pods: []podStatus{
				{Name: "a", Phase: "Running", Ready: true},
				{Name: "b", Phase: "Running", Ready: true},
			},
			want: fleetHealthy,
		},
		{
			// A long-lived cluster restarts its gateway many times over
			// days of runs. While it is Running and Ready it is serving,
			// and a high historical restart count must not be read as a
			// loop.
			name: "high restart count while ready is healthy",
			pods: []podStatus{
				{Name: "gateway", Phase: "Running", Ready: true, Restarts: 137},
			},
			want: fleetHealthy,
		},
		{
			name: "pending with no restarts is settling",
			pods: []podStatus{
				{Name: "a", Phase: "Running", Ready: true},
				{Name: "b", Phase: "Pending", Ready: false},
			},
			want: fleetSettling,
		},
		{
			// The condition this gate exists for: the controller acquires
			// its lease, starts its informers, and is killed for exceeding
			// its memory limit ~20s later.
			name: "oomkilled and not ready is a restart loop",
			pods: []podStatus{
				{
					Name: "controller", Phase: "Running", Ready: false, Restarts: 1109,
					WaitingReasons: []string{"CrashLoopBackOff"}, TerminatedReason: []string{"OOMKilled"},
				},
			},
			want: fleetRestartLoop,
		},
		{
			// CrashLoopBackOff alone is enough; the last termination
			// reason may be a plain non-zero exit rather than OOMKilled.
			name: "crashloopbackoff alone is a restart loop",
			pods: []podStatus{
				{
					Name: "cni", Phase: "Running", Ready: false, Restarts: 2,
					WaitingReasons: []string{"CrashLoopBackOff"},
				},
			},
			want: fleetRestartLoop,
		},
		{
			// A pod past the restart threshold that still is not Ready is
			// not converging even without a backoff reason recorded at the
			// instant of the read.
			name: "restarts past threshold while not ready is a restart loop",
			pods: []podStatus{
				{Name: "cni", Phase: "Running", Ready: false, Restarts: crashLoopRestartThreshold + 1},
			},
			want: fleetRestartLoop,
		},
		{
			// A restart loop outranks settling: one cycling pod among
			// healthy ones still means the component is broken.
			name: "one looping pod among healthy ones is a restart loop",
			pods: []podStatus{
				{Name: "a", Phase: "Running", Ready: true},
				{Name: "b", Phase: "Pending", Ready: false},
				{Name: "c", Phase: "Running", Ready: false, TerminatedReason: []string{"OOMKilled"}},
			},
			want: fleetRestartLoop,
		},
		{
			// ImagePullBackOff is not a restart loop: the container has
			// never started, so the pod is waiting on an image rather than
			// cycling. install.sh documents leftover pools in this state
			// on a standing cluster, and they must not fail every suite.
			name: "imagepullbackoff is settling",
			pods: []podStatus{
				{
					Name: "leftover", Phase: "Pending", Ready: false,
					WaitingReasons: []string{"ImagePullBackOff"},
				},
			},
			want: fleetSettling,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyPods(tc.pods); got != tc.want {
				t.Errorf("classifyPods() = %v, want %v", got, tc.want)
			}
		})
	}
}

// spec: 13.2 (network isolation — minimum CNI requirement)
//
// TestParsePodStatusesReadsRestartAndTerminationReason pins the parse of
// the kubectl jsonpath the gate reads. The restart count and the
// last-termination reason are what separate a crash loop from a slow
// start, so a parse that drops them turns a hard failure into a skip.
func TestParsePodStatusesReadsRestartAndTerminationReason(t *testing.T) {
	// The layout kubectl emits for the podHealthJSONPath template:
	// name, phase, Ready condition, per-container restart counts,
	// per-container waiting reasons, per-container termination reasons.
	out := strings.Join([]string{
		"kindnet-4jlbs\tRunning\tFalse\t1014\tCrashLoopBackOff\tOOMKilled",
		"lenny-gateway-1\tRunning\tTrue\t13\t\t",
		"lenny-agent-1\tRunning\tFalse\t2 7\t\tError OOMKilled",
		"", // trailing newline from the jsonpath range
	}, "\n")

	pods := parsePodStatuses(out)
	if len(pods) != 3 {
		t.Fatalf("parsePodStatuses returned %d pods, want 3: %+v", len(pods), pods)
	}

	cni := pods[0]
	if cni.Name != "kindnet-4jlbs" || cni.Ready || cni.Restarts != 1014 {
		t.Errorf("cni pod parsed as %+v", cni)
	}
	if !cni.inRestartLoop() {
		t.Errorf("an OOMKilled, CrashLoopBackOff pod with 1014 restarts is a restart loop; got %+v", cni)
	}

	gateway := pods[1]
	if !gateway.Ready || gateway.Restarts != 13 {
		t.Errorf("gateway pod parsed as %+v", gateway)
	}
	if gateway.inRestartLoop() {
		t.Errorf("a Running and Ready gateway is not in a restart loop; got %+v", gateway)
	}
	if len(gateway.WaitingReasons) != 0 || len(gateway.TerminatedReason) != 0 {
		t.Errorf("empty reason fields must parse to empty slices; got %+v", gateway)
	}

	// A multi-container pod reports one restart count and one reason per
	// container. The highest restart count and any OOMKilled reason must
	// both survive the reduction.
	multi := pods[2]
	if multi.Restarts != 7 {
		t.Errorf("multi-container restart count = %d, want the maximum 7", multi.Restarts)
	}
	if !containsString(multi.TerminatedReason, "OOMKilled") {
		t.Errorf("multi-container termination reasons = %v, want OOMKilled among them",
			multi.TerminatedReason)
	}
}

// spec: 13.2 (network isolation — minimum CNI requirement)
//
// TestParsePodStatusesSkipsMalformedLines confirms the parse fails
// toward "absent", which callers treat as a skip, rather than inventing
// a healthy pod out of output it does not recognize.
func TestParsePodStatusesSkipsMalformedLines(t *testing.T) {
	for _, out := range []string{
		"",
		"\n\n",
		"only-a-name\n",
		"too\tfew\tfields\n",
	} {
		if pods := parsePodStatuses(out); len(pods) != 0 {
			t.Errorf("parsePodStatuses(%q) = %+v, want no pods", out, pods)
		}
		if got := classifyPods(parsePodStatuses(out)); got != fleetAbsent {
			t.Errorf("classifyPods(parsePodStatuses(%q)) = %v, want fleetAbsent", out, got)
		}
	}
}

// spec: 6.2 (pod state machine — terminal Sandbox phases)
//
// TestParseSandboxRecordsSelectsAgedTerminalDebris pins which Sandbox
// objects the per-run prune removes. Terminal-phase Sandboxes are the
// objects that accumulate without bound on a standing cluster and grow
// the controller's informer cache until it is OOMKilled; live and recent
// ones must survive the prune.
func TestParseSandboxRecordsSelectsAgedTerminalDebris(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour).Format(time.RFC3339)
	recent := now.Add(-time.Minute).Format(time.RFC3339)

	out := strings.Join([]string{
		"aged-failed\tfailed\t" + old + "\t",
		"aged-terminated\tterminated\t" + old + "\t",
		"aged-claimed\tclaimed\t" + old + "\t",
		"aged-idle\tidle\t" + old + "\t",
		"aged-no-phase\t\t" + old + "\t",
		"recent-failed\tfailed\t" + recent + "\t",
		"already-deleting\tfailed\t" + old + "\t" + old,
		"",
	}, "\n")

	records := parseSandboxRecords(out)
	if len(records) != 7 {
		t.Fatalf("parseSandboxRecords returned %d records, want 7: %+v", len(records), records)
	}

	var selected []string
	for _, r := range records {
		if terminalSandboxPhases[r.Phase] && now.Sub(r.Created) >= sandboxDebrisMinAge {
			selected = append(selected, r.Name)
		}
	}
	want := []string{"aged-failed", "aged-terminated", "already-deleting"}
	if len(selected) != len(want) {
		t.Fatalf("prune selected %v, want %v", selected, want)
	}
	for i, name := range want {
		if selected[i] != name {
			t.Errorf("prune selection[%d] = %q, want %q (full selection %v)", i, selected[i], name, selected)
		}
	}

	// The deletionTimestamp field drives the finalizer-clearing pass, so
	// it must survive the parse.
	for _, r := range records {
		if r.Name == "already-deleting" && !r.Deleting {
			t.Errorf("a Sandbox with a deletionTimestamp must parse as Deleting: %+v", r)
		}
		if r.Name == "aged-failed" && r.Deleting {
			t.Errorf("a Sandbox with no deletionTimestamp must not parse as Deleting: %+v", r)
		}
	}
}

// spec: 6.2 (pod state machine — terminal Sandbox phases)
//
// TestTallyPhasesCountsUnsetPhase confirms the debris breakdown the
// failure diagnostic prints counts every object, including one whose
// status has no phase written yet. An undercount would understate the
// informer-cache pressure the operator is being asked to act on.
func TestTallyPhasesCountsUnsetPhase(t *testing.T) {
	counts, total := tallyPhases("a\tfailed\nb\tfailed\nc\t\nd\tterminated\n")
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
	if counts["failed"] != 2 || counts["terminated"] != 1 || counts["(none)"] != 1 {
		t.Errorf("counts = %v, want failed=2 terminated=1 (none)=1", counts)
	}
	if _, empty := tallyPhases(""); empty != 0 {
		t.Errorf("empty output must tally to zero objects")
	}
}
