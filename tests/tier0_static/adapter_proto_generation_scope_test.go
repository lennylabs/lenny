// SPDX-License-Identifier: MIT

package tier0_static

import (
	"sort"
	"strings"
	"testing"
)

// The gateway ↔ adapter gRPC contract is a shipped wire artifact a runtime
// author reads directly, so the doc comments that state the coordination
// generation's rules are the runtime author's copy of those rules. The pod
// holds the fenced generation per bound session: a fence records the
// generation against the session the fence names and changes nothing for a
// co-tenant session, the barrier gate compares against the value the pod
// holds for the session the request names, and a bound session for which the
// pod holds no fenced generation has no recorded value to match (§10.1.2
// step 3, §10.1.8 step 1, §28.5.1 CH-FENCE and CH-BARRIER).
//
// This gate reads the comment text, because that is the surface the runtime
// author reads and a descriptor carries no comments. It pins each carrier of
// the record-and-reject rule, the fence's own acceptance predicate, and the
// barrier gate against the pod-wide wording those comments carried before the
// generation was scoped to the session.

// generationScopeSite is one doc comment on the shipped contract that states
// a coordination-generation rule. anchor is a substring of a single line
// inside the comment, want are phrases the normalized comment must carry, and
// reject are the pod-wide phrases it must not.
type generationScopeSite struct {
	name   string
	anchor string
	want   []string
	reject []string
}

// generationScopeSites are the fence and barrier carriers. The twelve
// operational-RPC field comments are checked in one pass below rather than
// listed here, because they carry one shared sentence.
var generationScopeSites = []generationScopeSite{
	{
		name:   "CoordinatorFence RPC",
		anchor: "CoordinatorFence announces a new",
		want: []string{
			"records the generation against the session the fence names",
			"rejects any RPC carrying a generation older than the one it holds for that session",
			"A fence for one session does not change the generation the pod holds for another",
			"relative to that session's last acknowledged fence",
			"The first fence for that session within its current binding on this pod is never treated as a gap",
		},
		reject: []string{
			"records the new generation and from this point rejects any RPC carrying an older one",
			"relative to the last acknowledged fence",
			"The first call on a pod's lifetime",
		},
	},
	{
		name:   "CoordinatorFenceRequest message",
		anchor: "CoordinatorFenceRequest announces a new coordination generation",
		want: []string{
			"records the generation against the session the fence names",
			"rejects every RPC carrying a generation older than the one it holds for that session",
			// The rejection's status, its detail string, and the metric it
			// feeds have no other carrier, so the re-scoping keeps them.
			"FailedPrecondition + a `coordinator_handoff_stale` detail string",
			"lenny_coordinator_handoff_stale_total",
		},
		reject: []string{"carrying a strictly older generation"},
	},
	{
		name:   "CoordinatorFenceRequest.coordination_generation field",
		anchor: "coordinator wrote to Postgres in step 1",
		// This carrier already stated per-session monotonicity and keeps its
		// wording; the change is what the adapter does, not what it says.
		want:   []string{"Strictly monotonic on the pod side per session."},
		reject: nil,
	},
	{
		name:   "CoordinatorFenceResponse message",
		anchor: "CoordinatorFenceResponse acknowledges",
		want: []string{
			"not greater than the generation the pod holds for the session the fence names",
			"skips one or more values relative to the generation the pod holds for that session",
			"reset the transient tool-call state that session accumulated",
		},
		reject: []string{
			"not greater than the last fenced generation",
			"relative to the last fenced generation",
		},
	},
	{
		name:   "CheckpointBarrier RPC",
		anchor: "CheckpointBarrier dispatches a barrier signal",
		want: []string{
			"validates the request's `coordination_generation` against the generation the pod holds for the session the request names",
			// The unset arm is the ordinary state of a session that has
			// neither resumed nor been taken over, so the RPC comment states
			// it alongside the match rule; a runtime author reading this
			// carrier alone would otherwise fail closed on the drain barrier
			// §10.1.2 step 3 accepts.
			"a barrier naming a bound session for which the pod holds no fenced generation is accepted and records no value",
		},
		reject: []string{"against the last fenced generation"},
	},
	{
		name:   "CheckpointBarrierRequest message",
		anchor: "CheckpointBarrierRequest dispatches a barrier",
		want: []string{
			"must match the generation the pod holds for the session the request names",
			"a barrier naming a bound session for which the pod holds no fenced generation is accepted and records no value",
			"the adapter rejects with FailedPrecondition",
		},
		reject: []string{"must match the last fenced generation"},
	},
	{
		name:   "CheckpointBarrierRequest.coordination_generation field",
		anchor: "coordination generation. The adapter rejects",
		want: []string{
			"rejects with FailedPrecondition when the pod holds a generation for the session the request names and this is not equal to it",
		},
		reject: []string{"not strictly equal to the last fenced generation"},
	},
}

// operationalFenceSentence is the sentence every operational-RPC
// `coordination_generation` field comment carries. §10.1.2 step 3 owns the
// unset case, so these comments state the match rule and leave the unset arm
// to the fence and barrier carriers above.
const operationalFenceSentence = "A pod validates the generation on every gateway-to-pod RPC against the value it holds " +
	"for the session the RPC names, and rejects a request whose generation does not match it (§10.1)."

// operationalFenceSites is how many request messages carry that sentence:
// SendMessage, Attach, RotateCredentials, ExtendCredentialLease,
// RevokeCredentials, Interrupt, Checkpoint, SignalDeadline, Resume,
// ExportPaths, ReportUsage, and Shutdown.
const operationalFenceSites = 12

// podWideConsequences are the unconditional consequence clauses those twelve
// comments carried. Each is false for a bound session the pod holds no fenced
// generation for, which is the ordinary state of a session that has neither
// resumed nor been taken over.
var podWideConsequences = []string{
	"so a replica that has lost coordination cannot drive the pod",
	"so a replica that has lost coordination cannot tear the session down",
}

// normalizeProtoComment collapses a proto comment block into one line of
// prose: the `//` markers and the line breaks come off and runs of
// whitespace become single spaces, so a phrase can be matched without
// depending on where the comment happens to wrap.
func normalizeProtoComment(block string) string {
	var b strings.Builder
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "//")
		b.WriteString(" ")
		b.WriteString(strings.TrimSpace(line))
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// generationScopeViolations reports every way the proto source fails the
// gate: a carrier comment that is missing, one that omits a phrase the
// session unit requires, and one that still carries the pod-wide wording. The
// result is sorted so a caller can compare it deterministically.
func generationScopeViolations(src string) []string {
	var out []string
	for _, site := range generationScopeSites {
		block, ok := protoCommentBlock(src, site.anchor)
		if !ok {
			out = append(out, site.name+": comment not found")
			continue
		}
		text := normalizeProtoComment(block)
		for _, want := range site.want {
			if !strings.Contains(text, want) {
				out = append(out, site.name+": missing "+want)
			}
		}
		for _, reject := range site.reject {
			if strings.Contains(text, reject) {
				out = append(out, site.name+": carries "+reject)
			}
		}
	}
	normalized := normalizeProtoComment(src)
	if got := strings.Count(normalized, operationalFenceSentence); got != operationalFenceSites {
		out = append(out, "operational-RPC field comments: "+itoa(got)+" carry the session-scoped validation sentence, want "+itoa(operationalFenceSites))
	}
	for _, clause := range podWideConsequences {
		if strings.Contains(normalized, clause) {
			out = append(out, "operational-RPC field comments: carries "+clause)
		}
	}
	sort.Strings(out)
	return out
}

// itoa keeps the violation strings free of a fmt dependency for one integer.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// spec: 10.1.2 (coordinator handoff protocol, step 3), 10.1.8 (rolling
//
//	updates and the CheckpointBarrier protocol, step 1), 28.5.1
//	(gateway-to-pod channels, CH-FENCE and CH-BARRIER), 4.7 (runtime
//	adapter)
//
// diagnosis: a doc comment on the shipped gateway ↔ adapter gRPC contract
//
//	states the coordination generation as one value per pod. A runtime
//	author reading it implements a fence that fences every session on
//	the pod and a barrier gate that refuses a co-tenant session's
//	legitimate generation.
func TestAdapterProtoGenerationCommentsAreSessionScoped(t *testing.T) {
	t.Parallel()

	src := readAdapterProto(t)
	if got := generationScopeViolations(src); len(got) > 0 {
		t.Errorf("%s coordination-generation comments: %v", adapterProtoRel, got)
	}
}

// spec: 10.1.2 (coordinator handoff protocol, step 3), 28.5.1
//
//	(gateway-to-pod channels)
//
// diagnosis: the gate no longer detects pod-wide generation wording, so the
//
//	shipped contract can regress to one value per pod with the tier
//	green.
func TestGenerationScopeGateDetectsPodWideWording(t *testing.T) {
	t.Parallel()

	const podWide = `
// CoordinatorFenceRequest announces a new coordination generation to the
// pod. The pod records the new generation and from this point rejects
// every RPC carrying a strictly older generation.
message CoordinatorFenceRequest {}
`
	got := generationScopeViolations(podWide)
	if len(got) == 0 {
		t.Fatal("gate reported no violation on pod-wide wording")
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		"CoordinatorFenceRequest message: carries carrying a strictly older generation",
		"CoordinatorFence RPC: comment not found",
		"operational-RPC field comments: 0 carry the session-scoped validation sentence, want 12",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("gate did not report %q, got:\n%s", want, joined)
		}
	}
}
