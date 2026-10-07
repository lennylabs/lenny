// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the mid-session failure outcome and the
// gateway's handling of a failed CH-ATTACH stream.
//
// A retryable failure of an active session follows the §7.3 resume flow: it
// moves to resume_pending while retries remain and to awaiting_client_action
// once they are exhausted. Only a non-retryable failure of an active session
// ends in failed. The gateway holds one Attach stream per session until it
// releases the session's binding, reports a stream that ends with
// DEADLINE_EXCEEDED or INTERNAL as runtime_crash through that classifier, and
// does not redial Attach against the same pod. A failed session retires its
// pod only on a pool with maxConcurrentSessions: 1; on a concurrent pool the
// failed slot counts toward the whole-pod replacement trigger.
//
// The retired readings sent an exhausted retry to failed, ended the session
// on a missed heartbeat ack, opened the stream after StartSession for the
// session's whole life with a gateway-side heartbeat and reconnection, and
// retired a concurrent pod on any failed session. Each phrase below matches
// only a site the reconciliation replaced, so a later edit to an unrelated
// sentence does not trip the sweep.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks, and
// compares with every whitespace run collapsed to one space.
//
// spec: 7.2 (Interactive Session Model), 7.3 (Retry and Resume), 4.6.3 (CRD
// Field Ownership and Write Boundaries), 6.1 (What a Pre-Warmed Pod Looks
// Like), 6.2 (Pod State Machine), 8.8 (TaskRecord and TaskResult Schema),
// 15.4.3 (Runtime Integration Levels), 28.5.1 (Gateway-to-pod), 28.5.3
// (Intra-pod), 5.2 (Pool Configuration and Execution Modes)

package tier11_docs_test

import (
	"strings"
	"testing"
)

// retiredFailureOutcomePhrases are the sentences the reconciliation replaced:
// an exhausted retry that ends in failed, a missed heartbeat ack that ends the
// session, a stream opened after StartSession with a gateway-side heartbeat
// and reconnection, and a failed session that retires a concurrent pod.
var retiredFailureOutcomePhrases = []string{
	"Unrecoverable error / retries exhausted",
	"Pod crash (retries exhausted) / BUDGET_KEYS_EXPIRED",
	"Unrecoverable error, retries exhausted, or",
	"crash, unrecoverable error, retries exhausted",
	"Pod crash, retries exhausted",
	"attached --> failed : Pod crash (retries exhausted)",
	"Crash, retries exhausted, or BUDGET_KEYS_EXPIRED",
	"On retry exhaustion the task transitions to `failed`",
	"or `failed` (on pod crash with retries exhausted)",
	"Unrecoverable error or retries exhausted",
	"input_required --> failed: Retries exhausted",
	"the runtime crashed and retries were exhausted",
	`"retriesExhausted": true`,
	"Pod failure while suspended (pod still held)",
	"suspended --> resume_pending : Pod failure or pod released",
	"suspended --> resume_pending: Pod failure while suspended",
	"running --> failed: Crash / unrecoverable error",
	"(pod released), or pod failure",
	"Pod crash (retryCount < maxRetries)",
	"gRPC error while awaiting input, retries exhausted",
	"and reports failure to gateway",
	"`failed` if retries exhausted",
	"If retries are exhausted or the failure is non-retryable",
	"unrecoverable error or pod-crash retries exhausted",
	"crashes always fail the session outright",
	"same behavior as terminal failure after retry exhaustion",
	"before marking the child as permanently failed",
	"missed acknowledgment ends the session,",
	"missed ack within 10 seconds ends the session",
	"a missed ack ends the session",
	"treats the process as hung and ends the session",
	"within 10 seconds, the session ends",
	"The session ends about 10 seconds after a `heartbeat`",
	"failure to ack within 10 seconds ends the session",
	"opens the Attach stream after `StartSession` succeeds",
	"The stream remains open for the duration of the session",
	"the gateway attempts reconnection",
	"may attempt to resume on the same pod",
	"pod failure results in session failure",
	"detects the stream break via heartbeat timeout",
	"The gateway sends periodic heartbeat pings on the Attach stream",
	"The adapter must respond with `HeartbeatAck`",
	"a failed session, an unschedulable host node",
	"or a failed or crashed session;",
	"A failed or crashed session, or a reached recycle limit",
}

// failureOutcomeEdgesStated maps each repository-relative file to the
// reconciled statements it must carry.
var failureOutcomeEdgesStated = []struct {
	path []string
	want []string
}{
	{
		path: []string{"docs", "reference", "state-machines.md"},
		want: []string{
			"running --> awaiting_client_action : Retryable failure (retries exhausted)",
			"input_required --> awaiting_client_action : Retryable failure (retries exhausted)",
			"suspended --> awaiting_client_action : Retryable failure, pod still held (retries exhausted)",
			"attached --> awaiting_client_action : Retryable failure (retries exhausted)",
			"| `running` | `awaiting_client_action` |",
			"| `input_required` | `awaiting_client_action` |",
			"| `suspended` | `awaiting_client_action` |",
			"when its session's stream to the pod fails",
			"a failed session on a pool with `maxConcurrentSessions: 1`",
		},
	},
	{
		path: []string{"docs", "getting-started", "concepts.md"},
		want: []string{"running --> awaiting_client_action: Retryable failure (retries exhausted)"},
	},
	{
		path: []string{"docs", "client-guide", "session-lifecycle.md"},
		want: []string{"running --> awaiting_client_action : retryable failure (retries exhausted)"},
	},
	{
		path: []string{"docs", "api", "internal.md"},
		want: []string{
			"holds that one stream until it releases the session's binding",
			"does not redial Attach against the same pod",
		},
	},
	{
		path: []string{"docs", "runtime-author-guide", "lifecycle.md"},
		want: []string{"a session ends in failure or a crash on a pool with `maxConcurrentSessions: 1`"},
	},
	{
		path: []string{"spec", "07_session-lifecycle.md"},
		want: []string{"running → awaiting_client_action (retryable failure, retries exhausted"},
	},
	{
		path: []string{"spec", "05_runtime-registry-and-pool-model.md"},
		want: []string{"On a pool with `maxConcurrentSessions: 1`, a session that ends in failure or a crash also retires the pod"},
	},
	{
		path: []string{"spec", "06_warm-pod-model.md"},
		want: []string{"A session that ends in failure or a crash retires its pod as the **Pod retirement policy (recycling pools).** paragraph that begins"},
	},
}

// collapseWhitespace replaces every whitespace run with one space, so a
// statement wrapped across lines is still one match.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// spec: 7.2 (Interactive Session Model), 7.3 (Retry and Resume), 28.5.1
// (Gateway-to-pod), 5.2 (Pool Configuration and Execution Modes)
// diagnosis: a specification or documentation page again states a retired
//
//	failure outcome: an exhausted retry that ends in failed, a missed
//	heartbeat ack that ends the session, an Attach stream opened after
//	StartSession with a gateway-side heartbeat and reconnection, or a failed
//	session that retires a concurrent pod. The §7.3 resume flow moves a
//	retryable failure with exhausted retries to awaiting_client_action, the
//	§28.5.1 CH-ATTACH card states the stream rules, and the §5.2 retirement
//	statement applies to a pool with maxConcurrentSessions: 1.
func TestRetiredFailureOutcomePhrasesAbsent(t *testing.T) {
	root := repoRoot(t)
	for path, body := range specAndDocFiles(t, root) {
		requireNoneContainFold(t, path, body, retiredFailureOutcomePhrases)
	}
}

// spec: 7.2 (Interactive Session Model), 7.3 (Retry and Resume), 6.2 (Pod
// State Machine), 28.5.1 (Gateway-to-pod), 5.2 (Pool Configuration and
// Execution Modes)
// diagnosis: a page lost a reconciled statement: the retryable-exhausted
//
//	edges to awaiting_client_action in the state diagrams and tables, the
//	held Attach stream and the no-redial rule in the internal API page, or
//	the maxConcurrentSessions: 1 scope of pod retirement after a failed
//	session in the specification and the runtime author guide.
func TestFailureOutcomeEdgesStated(t *testing.T) {
	root := repoRoot(t)
	for _, f := range failureOutcomeEdgesStated {
		label := strings.Join(f.path, "/")
		requireAllContain(t, label, collapseWhitespace(readRepoFile(t, root, f.path...)), f.want)
	}
}
