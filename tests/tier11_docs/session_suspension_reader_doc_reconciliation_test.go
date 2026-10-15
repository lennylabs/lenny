// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the reader pages that describe the
// session lifecycle around suspension and recovery: the client lifecycle page,
// the state-machine and configuration references, the concepts page, the
// adapter contract, the runtime-author pages, the runbooks, and the operator
// pages.
//
// A `running` session with no activity for `maxClientIdleSeconds` (900 s by
// default) is idle-suspended: the gateway seals it on the adapter, checkpoints
// the workspace, and releases the pod or slot as it suspends the session. Any
// message or resume call resumes a `suspended` session, whatever the message's
// `delivery` value. A `suspended` session expires after the suspended-session
// lifetime `gateway.maxSuspendedSessionSeconds`, it holds no concurrent-session
// slot, and session age counts active time. A runtime reports `busy: true` on
// the `heartbeat_ack` that names a session while it does work for that
// session, and the adapter counts a fixed list of traffic as activity without
// a report. Each check below pins one of those statements on the pages that
// make it, against the spec where the spec states a value.
//
// The tests read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 6.2 (Pod State Machine), 7.2 (Interactive Session Model), 7.3 (Retry
// and Resume), 11.1 (Admission and Fairness), 11.3 (Timeouts and
// Cancellation), 12.8 (Compliance Interfaces), 15.7 (Runtime Author SDKs),
// 16.5 (Alerting Rules and SLOs), 27.6 (Session lifecycle and cleanup), 28.5.3
// (Intra-pod)

package tier11_docs_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// immediateDeliveryResumeSpellings are the spellings under which a reader page
// conditioned the resume of a `suspended` session on `delivery: immediate`.
// Any message resumes such a session now, so none of them may stand on a page
// that describes the session state machine.
var immediateDeliveryResumeSpellings = []string{
	"delivery:immediate",
	`with ` + "`delivery: \"immediate\"`" + `, atomically resumes`,
	`send a message with ` + "`delivery: \"immediate\"`",
}

// lifecycleStatePages are the reader pages that diagram or tabulate the
// session state machine.
var lifecycleStatePages = []string{
	filepath.Join("docs", "client-guide", "session-lifecycle.md"),
	filepath.Join("docs", "reference", "state-machines.md"),
	filepath.Join("docs", "getting-started", "concepts.md"),
	filepath.Join("docs", "runtime-author-guide", "lifecycle.md"),
}

// spec: 6.2 (Pod State Machine, suspended state), 7.2 (Interactive Session
// Model, delivery path 6)
// diagnosis: a page that diagrams or tabulates the session state machine still
// resumes a `suspended` session only for a `delivery: immediate` message, or
// omits the idle suspension edge or the suspended-session lifetime. A reader
// sends a resume call the message already made, or never learns that an idle
// session gives up its pod and expires after the suspended-session lifetime.
func TestLifecyclePagesStateIdleSuspensionAndResumeOnAnyMessage(t *testing.T) {
	root := repoRoot(t)
	for _, page := range lifecycleStatePages {
		body := readDocPage(t, filepath.Join(root, page))
		for _, s := range immediateDeliveryResumeSpellings {
			if strings.Contains(body, s) {
				t.Errorf("%s conditions the resume of a suspended session on delivery: immediate (%q)", page, s)
			}
		}
		if !strings.Contains(strings.ToLower(body), "idle suspension") {
			t.Errorf("%s does not name idle suspension as a way into `suspended`", page)
		}
		if !strings.Contains(body, "maxSuspendedSessionSeconds") && !strings.Contains(body, "suspended-session lifetime") {
			t.Errorf("%s does not state the suspended-session lifetime", page)
		}
	}

	lifecycle := filepath.Join("docs", "client-guide", "session-lifecycle.md")
	body := readDocPage(t, filepath.Join(root, lifecycle))
	for _, edge := range []string{
		"running --> suspended : idle suspension",
		"suspended --> running : message or resume (pod still held)",
		"suspended --> resume_pending : message or resume (pod released)",
		"suspended --> expired : suspended-session lifetime",
	} {
		if !strings.Contains(body, edge) {
			t.Errorf("%s: the state diagram has no edge %q", lifecycle, edge)
		}
	}
	resume := tableRow(t, lifecycle, body, "POST /v1/sessions/{id}/resume")
	if !strings.Contains(resume, "`awaiting_client_action`, `suspended`") {
		t.Errorf("%s: the resume precondition row does not admit `suspended`: %s", lifecycle, resume)
	}
	machines := filepath.Join("docs", "reference", "state-machines.md")
	mbody := readDocPage(t, filepath.Join(root, machines))
	for _, edge := range []string{
		"running --> suspended : Idle suspension",
		"resuming --> resume_pending : Retryable failure or resuming watchdog (retries remain)",
		"resume_pending --> awaiting_client_action : maxResumeWindowSeconds elapsed",
	} {
		if !strings.Contains(mbody, edge) {
			t.Errorf("%s: no diagram edge %q", machines, edge)
		}
	}
	if strings.Contains(mbody, "(no pod available)") {
		t.Errorf("%s: the resume_pending escalation edge still names pod availability; the resume window caps the state whatever the cause", machines)
	}
}

// spec: 7.3 (Retry and Resume, Retry accounting, Resume driver), 11.1
// (Admission and Fairness, Resume admission)
// diagnosis: the client lifecycle page no longer presents automatic recovery
// first, omits the retry accounting, or omits the concurrent-session refusal
// of a resume. A client polls `POST /resume` for recovery the gateway already
// drives, misreads when `awaiting_client_action` is reached, or treats a
// `QUOTA_EXCEEDED` refusal as a failed restore.
func TestClientLifecyclePageStatesRecoveryRetryAccountingAndResumeAdmission(t *testing.T) {
	root := repoRoot(t)
	page := filepath.Join("docs", "client-guide", "session-lifecycle.md")
	body := readDocPage(t, filepath.Join(root, page))
	resume := docHeadingSection(t, page, body, "### g. Resume")
	auto := strings.Index(resume, "resume driver")
	override := strings.Index(resume, "is the client override")
	if auto < 0 || override < 0 || auto > override {
		t.Errorf("%s: the resume section does not describe the resume driver's automatic recovery before the client override", page)
	}
	for _, want := range []string{
		"`retryCount` advances by one",
		"A successful restore does not advance it",
		"`429 QUOTA_EXCEEDED`",
		"retries with a new key",
		"`status_change` event",
	} {
		if !strings.Contains(resume, want) {
			t.Errorf("%s: the resume section does not state %q", page, want)
		}
	}
	window := tableRow(t, page, body, "maxResumeWindow")
	if !strings.Contains(window, "Maximum time a session stays in `resume_pending` before it moves to `awaiting_client_action`") {
		t.Errorf("%s: the maxResumeWindow row does not state the resume_pending cap: %s", page, window)
	}
	age := tableRow(t, page, body, "maxSessionAge")
	if strings.Contains(strings.ToLower(age), "wall-clock") {
		t.Errorf("%s: the maxSessionAge row describes wall-clock time; session age counts active time: %s", page, age)
	}
}

// specTimeoutDefault returns the default cell of the §11.3 timeout-table row
// whose setting cell names the backticked key, or fails the test.
func specTimeoutDefault(t *testing.T, spec, key string) string {
	t.Helper()
	for _, l := range strings.Split(spec, "\n") {
		if !strings.HasPrefix(l, "|") || !strings.Contains(l, "`"+key+"`") {
			continue
		}
		cells := strings.Split(l, "|")
		if len(cells) > 3 {
			return strings.TrimSuffix(strings.TrimSpace(cells[2]), "s")
		}
	}
	t.Fatalf("spec/11_policy-and-controls.md: no timeout row names %s", key)
	return ""
}

// configRowDefault returns the Default cell of the configuration reference row
// for key, or fails the test.
func configRowDefault(t *testing.T, page, body, key string) (row, def string) {
	t.Helper()
	row = tableRow(t, page, body, key)
	cells := strings.Split(row, "|")
	if len(cells) < 5 {
		t.Fatalf("%s: the %s row has too few cells: %s", page, key, row)
	}
	return row, strings.TrimSpace(cells[3])
}

// spec: 11.3 (Timeouts and Cancellation), 6.2 (Pod State Machine), 8.3
// (Delegation Policy and Lease, Defense-in-depth TTL), 27.6 (Session lifecycle
// and cleanup)
// diagnosis: the configuration reference disagrees with the spec's timeout
// table on a suspension default, still lists the deleted per-pool retry
// budget, or describes the idle bound as a pure expiry. An operator sizes the
// delegation budget TTL below the suspended-session lifetime, sets a field the
// pool admission no longer reads, or expects idle sessions to end rather than
// be suspended.
func TestConfigurationReferenceMatchesSuspensionDefaults(t *testing.T) {
	root := repoRoot(t)
	spec := readDocPage(t, filepath.Join(root, "spec", "11_policy-and-controls.md"))
	page := filepath.Join("docs", "reference", "configuration.md")
	body := readDocPage(t, filepath.Join(root, page))

	for _, c := range []struct{ docKey, specKey string }{
		{"gateway.maxSuspendedSessionSeconds", "gateway.maxSuspendedSessionSeconds"},
		{"gateway.suspendedBacklogRecheckSeconds", "gateway.suspendedBacklogRecheckSeconds"},
		{"delegation.budgetKeyTTLSeconds", "delegation.budgetKeyTTLSeconds"},
		{"gateway.maxSuspendedPodHoldSeconds", "gateway.maxSuspendedPodHoldSeconds"},
		{"sessionPolicy.maxClientIdleSeconds", "sessionPolicy.maxClientIdleSeconds"},
		{"session.maxClientIdleSeconds", "sessionPolicy.maxClientIdleSeconds"},
	} {
		want := specTimeoutDefault(t, spec, c.specKey)
		_, got := configRowDefault(t, page, body, c.docKey)
		if got != want {
			t.Errorf("%s: %s defaults to %q, the spec timeout table states %q", page, c.docKey, got, want)
		}
	}
	ttl, _ := configRowDefault(t, page, body, "delegation.budgetKeyTTLSeconds")
	if !strings.Contains(ttl, "Must exceed `gateway.maxSuspendedSessionSeconds`") {
		t.Errorf("%s: the budget key TTL row does not state that it must exceed the suspended-session lifetime: %s", page, ttl)
	}
	recheck, _ := configRowDefault(t, page, body, "gateway.suspendedBacklogRecheckSeconds")
	if !strings.Contains(recheck, ">= 30") {
		t.Errorf("%s: the backlog re-check row does not state the 30s minimum: %s", page, recheck)
	}
	window, _ := configRowDefault(t, page, body, "session.maxResumeWindowSeconds")
	if !strings.Contains(window, "Maximum time a session stays in `resume_pending` before it moves to `awaiting_client_action`.") {
		t.Errorf("%s: the maxResumeWindowSeconds row does not state the resume_pending cap: %s", page, window)
	}
	idle, _ := configRowDefault(t, page, body, "session.maxClientIdleSeconds")
	if !strings.Contains(idle, "idle-suspends") || !strings.Contains(idle, "`max_idle_time`") {
		t.Errorf("%s: the maxClientIdleSeconds row does not state idle suspension and the expiry of a session that cannot be idle-suspended: %s", page, idle)
	}
	playground, _ := configRowDefault(t, page, body, "playground.maxSessionMinutes")
	if !strings.Contains(playground, "active time") || !strings.Contains(playground, "`suspended`") {
		t.Errorf("%s: the playground duration cap row does not state that it counts active time and pauses while suspended: %s", page, playground)
	}

	for _, p := range []string{
		page,
		filepath.Join("docs", "operator-guide", "configuration.md"),
		filepath.Join("docs", "reference", "execution-modes.md"),
		filepath.Join("docs", "runtime-author-guide", "runtime-configuration.md"),
	} {
		b := readDocPage(t, filepath.Join(root, p))
		if strings.Contains(b, "maxSessionRetries") {
			t.Errorf("%s still names maxSessionRetries; retryPolicy.maxRetries is the one retry budget", p)
		}
		if strings.Contains(b, "maxClientIdleSeconds: 7200") {
			t.Errorf("%s still shows the maxClientIdleSeconds example at the session-age value; its default is 900", p)
		}
	}
}

// quotedValue matches one quoted lowercase enum value in a table row.
var quotedValue = regexp.MustCompile(`"([a-z_]+)"`)

// deadlineTriggers returns the quoted `trigger` values of a
// `deadline_approaching` frame row, from the spec or the reference page.
func deadlineTriggers(t *testing.T, where, row string) []string {
	t.Helper()
	i := strings.Index(row, "trigger")
	if i < 0 {
		t.Fatalf("%s: the deadline_approaching row names no trigger: %s", where, row)
	}
	var out []string
	for _, m := range quotedValue.FindAllStringSubmatch(row[i:], -1) {
		out = append(out, m[1])
	}
	return out
}

// spec: 28.5.3 (Intra-pod, CH-RUNTIMEOPS frame table), 4.7.1 (Role and Gateway
// RPC Contract, Session seal), 10.1.2 (Coordinator Handoff Protocol)
// diagnosis: the adapter-contract reference disagrees with the spec on the
// `deadline_approaching` trigger values, omits the seal RPCs, or still states
// the coordinator fence as a precondition for every RPC. A runtime waits for an
// idle warning the adapter never sends, or a reader cannot explain the
// `session_sealed` refusal of a message.
func TestAdapterContractStatesSealRPCsAndCurrentDeadlineTriggers(t *testing.T) {
	root := repoRoot(t)
	spec := readDocPage(t, filepath.Join(root, "spec", "28_communication-channels.md"))
	page := filepath.Join("docs", "reference", "adapter-contract.md")
	body := readDocPage(t, filepath.Join(root, page))

	var specRow string
	for _, l := range strings.Split(spec, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "| `deadline_approaching`") {
			specRow = l
		}
	}
	if specRow == "" {
		t.Fatal("spec/28_communication-channels.md: no deadline_approaching frame row")
	}
	want := deadlineTriggers(t, "spec", specRow)
	got := deadlineTriggers(t, page, tableRow(t, page, body, "deadline_approaching"))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s: deadline_approaching triggers %v, spec states %v", page, got, want)
	}

	for _, rpc := range []string{"SealSession", "UnsealSession"} {
		row := tableRow(t, page, body, rpc)
		if !strings.Contains(row, "Deadline: 5s.") {
			t.Errorf("%s: the %s row states no 5s deadline: %s", page, rpc, row)
		}
	}
	fence := tableRow(t, page, body, "CoordinatorFence")
	if !strings.Contains(fence, "precondition for operational RPCs to the pod it fences") {
		t.Errorf("%s: the CoordinatorFence row does not scope the precondition to the fenced pod: %s", page, fence)
	}
	for _, want := range []string{
		"**Session seal.**",
		"`session_sealed`",
		"`FAILED_PRECONDITION`",
		"other than `heartbeat_ack`, whose `sessionId` the `heartbeat_ack` section states.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s does not state %q", page, want)
		}
	}
}

// busyReportingPages are the runtime-author pages that tell a runtime how to
// answer a `heartbeat`. Each states the `busy` rule and the activity the
// adapter counts without a report.
var busyReportingPages = []string{
	filepath.Join("docs", "runtime-author-guide", "integration-levels.md"),
	filepath.Join("docs", "runtime-author-guide", "lifecycle.md"),
	filepath.Join("docs", "runtime-author-guide", "local-development.md"),
	filepath.Join("docs", "runtime-author-guide", "echo-runtime.md"),
	filepath.Join("docs", "runtime-author-guide", "runtime-sdk.md"),
	filepath.Join("docs", "runtime-author-guide", "testing.md"),
	filepath.Join("docs", "runtime-author-guide", "sdk-examples", "index.md"),
	filepath.Join("docs", "runtime-author-guide", "sdk-examples", "go.md"),
	filepath.Join("docs", "runtime-author-guide", "sdk-examples", "python.md"),
	filepath.Join("docs", "runtime-author-guide", "sdk-examples", "typescript.md"),
	filepath.Join("docs", "getting-started", "concepts.md"),
	filepath.Join("docs", "tutorials", "build-a-runtime.md"),
}

// sealActivityMarkers name each entry of the adapter's activity list: the
// writes to the runtime, the session-scoped reads, the completion frame, and
// the platform and connector tool calls in flight.
var sealActivityMarkers = []string{
	"other than `heartbeat` and `session_end`",
	"`set_tracing_context`",
	"`llm_request_completed`",
	"connector MCP tool call",
}

// retiredMissedAckOutcome is the statement of a missed acknowledgment from
// before the adapter heartbeated each session: it ended a stream and nothing
// more, with no refusal of a later stream.
const retiredMissedAckOutcome = "A missed acknowledgment ends that session's stream"

// spec: 28.5.3 (Intra-pod, Outbound heartbeat_ack, Timing), 4.7.1 (Role and
// Gateway RPC Contract, Session seal), 15.7 (Runtime Author SDKs, Busy
// reporting), 6.2 (Pod State Machine, Idle suspension)
// diagnosis: a runtime-author page omits the `busy` report on `heartbeat_ack`,
// the activity the adapter counts without one, or the current outcome of a
// missed acknowledgment. A runtime built from it does background or
// tool-call work without reporting it, and the idle suspension releases its
// pod mid-work.
func TestRuntimeAuthorPagesStateBusyReportingAndAdapterActivity(t *testing.T) {
	root := repoRoot(t)
	for _, page := range busyReportingPages {
		body := readDocPage(t, filepath.Join(root, page))
		if !strings.Contains(body, "busy: true") && !strings.Contains(body, `"busy":true`) {
			t.Errorf("%s does not state the `busy: true` report on heartbeat_ack", page)
		}
		for _, m := range sealActivityMarkers {
			if !strings.Contains(body, m) {
				t.Errorf("%s: the adapter activity list lacks %q", page, m)
			}
		}
		if strings.Contains(body, retiredMissedAckOutcome) {
			t.Errorf("%s states the retired missed-acknowledgment outcome %q", page, retiredMissedAckOutcome)
		}
	}
	testsPage := filepath.Join("docs", "runtime-author-guide", "testing.md")
	row := tableRow0(t, testsPage, readDocPage(t, filepath.Join(root, testsPage)), "heartbeat busy")
	if !strings.Contains(row, "`busy: true` and the session's `sessionId`") {
		t.Errorf("%s: the heartbeat busy conformance row does not state the assertion: %s", testsPage, row)
	}
}

// tableRow0 returns the markdown table row whose first cell is the plain text
// label, or fails the test.
func tableRow0(t *testing.T, page, body, label string) string {
	t.Helper()
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "| "+label+" |") {
			return l
		}
	}
	t.Fatalf("%s: no table row %q", page, label)
	return ""
}

// spec: 11.1 (Admission and Fairness, Concurrent-session count), 16.5
// (Alerting Rules and SLOs), 16.1 (Metrics), 8.3 (Delegation Policy and
// Lease, Defense-in-depth TTL), 4.9 (Credential Leasing Service)
// diagnosis: an operator page or runbook still counts a `suspended` session
// toward the concurrent-session limits or the availability SLO, misstates the
// resume counter, or omits the restore's wait on an exhausted credential pool.
// An operator sizes quotas against sessions that hold no slot, or reads a
// session parked in `resume_pending` as a gateway fault.
func TestOperatorPagesStateSuspendedAccountingAndRestoreWaits(t *testing.T) {
	root := repoRoot(t)
	checks := []struct {
		page  string
		wants []string
	}{
		{filepath.Join("docs", "operator-guide", "multi-tenancy.md"), []string{
			"except while it is `suspended`",
			"is refused with `QUOTA_EXCEEDED` and the session stays `suspended`",
		}},
		{filepath.Join("docs", "operator-guide", "observability.md"), []string{
			"A `suspended` session counts as neither available nor in recovery and is not counted",
		}},
		{filepath.Join("docs", "runbooks", "pod-kill-during-session.md"), []string{
			"advances at most once for each restore that the gateway starts on a claimed replacement pod",
		}},
		{filepath.Join("docs", "runbooks", "credential-pool-exhaustion.md"), []string{
			"waits in `resume_pending`",
			"suspended or recovering sessions wait in resume_pending",
		}},
		{filepath.Join("docs", "runbooks", "delegation-budget-recovery.md"), []string{
			"must exceed the suspended-session lifetime `gateway.maxSuspendedSessionSeconds`",
		}},
		{filepath.Join("docs", "reference", "metrics.md"), []string{
			"`suspended` sessions that reached the suspended-session lifetime `gateway.maxSuspendedSessionSeconds`",
			"a suspended session's pod hold ends and the pod or slot is successfully released",
		}},
		{filepath.Join("docs", "client-guide", "webhooks.md"), []string{
			"cannot be idle-suspended",
		}},
		{filepath.Join("docs", "reference", "cloudevents-catalog.md"), []string{
			"cannot be idle-suspended",
		}},
		{filepath.Join("docs", "operator-guide", "web-playground.md"), []string{
			"The cap counts active time",
			"idle-suspends the session",
		}},
		{filepath.Join("docs", "runtime-author-guide", "platform-tools.md"), []string{
			"the pending elicitation expires and its answer is never delivered",
		}},
		{filepath.Join("docs", "client-guide", "sdk-examples", "mcp-sdk.md"), []string{
			"| `resume_session` | Resume a `suspended` session or a session in `awaiting_client_action` |",
		}},
	}
	for _, c := range checks {
		body := readDocPage(t, filepath.Join(root, c.page))
		for _, w := range c.wants {
			if !strings.Contains(body, w) {
				t.Errorf("%s does not state %q", c.page, w)
			}
		}
	}
	mcp := filepath.Join("docs", "client-guide", "sdk-examples", "mcp-sdk.md")
	body := readDocPage(t, filepath.Join(root, mcp))
	for _, walkthrough := range []string{
		"### Step-by-Step Walkthrough with Interrupt/Resume",
		"### Interrupt/Resume Example (Python)",
	} {
		section := docHeadingSection(t, mcp, body, walkthrough)
		if strings.Contains(section, `"resume_session"`) {
			t.Errorf("%s: %q still calls resume_session after the message that already resumes the session", mcp, walkthrough)
		}
	}
}

// idleQualifyingEventMarkers name each qualifying event of the idle clock on
// the configuration reference row, in the order the spec lists them.
var idleQualifyingEventMarkers = []string{
	"a message the runtime adapter writes to the runtime",
	"a tool approval or denial",
	"an elicitation response",
	"an attach",
	"`agent_output` or `tool_use` event",
	"`lenny/await_children` call and each partial result",
	"proxy delivery mode",
	"non-zero token delta (direct delivery mode)",
}

// retiredIdleQualifyingEvents are events an earlier row listed that the idle
// clock does not count.
var retiredIdleQualifyingEvents = []string{"tool result", "elicitation request"}

// spec: 6.2 (Pod State Machine, maxClientIdleSeconds clock behavior across
// states), 7.2 (Interactive Session Model, delivery path 6 Backlog resume,
// Synchronous delivery, Held messages), 11.1 (Admission and Fairness, Resume
// admission)
// diagnosis: the configuration reference lists idle-clock events the spec does
// not define or omits the ones it does, or overstates which podless suspended
// sessions the leader resumes; or the client lifecycle page extends the
// `delivered` receipt beyond a REST message on the coordinating replica. An
// operator expects a parent in `await_children` to be idle-suspended, expects a
// message held before an interrupt to resume the session, or a client expects
// `delivered` for an MCP message that receives `queued`.
func TestReaderPagesStateIdleEventsBacklogResumeAndSuspendedDeliveryReceipts(t *testing.T) {
	root := repoRoot(t)
	page := filepath.Join("docs", "reference", "configuration.md")
	body := readDocPage(t, filepath.Join(root, page))

	idle, _ := configRowDefault(t, page, body, "session.maxClientIdleSeconds")
	for _, m := range idleQualifyingEventMarkers {
		if !strings.Contains(idle, m) {
			t.Errorf("%s: the maxClientIdleSeconds qualifying events lack %q: %s", page, m, idle)
		}
	}
	for _, r := range retiredIdleQualifyingEvents {
		if strings.Contains(idle, r) {
			t.Errorf("%s: the maxClientIdleSeconds row lists %q, which the idle clock does not count", page, r)
		}
	}

	recheck, _ := configRowDefault(t, page, body, "gateway.suspendedBacklogRecheckSeconds")
	for _, want := range []string{
		"idle-suspended and its dead-letter queue or inbox holds a message",
		"accepted after the session entered `suspended`",
		"concurrent-session admission check",
		"again at the next interval",
	} {
		if !strings.Contains(recheck, want) {
			t.Errorf("%s: the backlog re-check row does not state %q: %s", page, want, recheck)
		}
	}

	lifecycle := filepath.Join("docs", "client-guide", "session-lifecycle.md")
	lbody := readDocPage(t, filepath.Join(root, lifecycle))
	// The page has two `suspended` rows; the message-delivery one names the
	// admission refusal.
	var row string
	for _, l := range strings.Split(lbody, "\n") {
		if strings.HasPrefix(l, "| `suspended` |") && strings.Contains(l, "QUOTA_EXCEEDED") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("%s: no `suspended` message-delivery row", lifecycle)
	}
	for _, want := range []string{
		"`POST /v1/sessions/{id}/messages`",
		"receipt `delivered`",
		"receipt `queued`",
		"`lenny/send_message`",
		"`QUOTA_EXCEEDED`",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("%s: the suspended message-delivery row does not state %q: %s", lifecycle, want, row)
		}
	}
}

// spec: 4.7.1 (Role and Gateway RPC Contract, Session seal), 6.2 (Pod State
// Machine, Idle suspension)
// diagnosis: the adapter-contract reference or the runtime-author lifecycle
// page widens the unseal condition, omits the outcome of a seal nobody lifts,
// or implies that a `busy` report after the seal protects work from the
// release. A runtime author relies on a late `busy` report and loses the work,
// or an operator cannot explain a `running` session refusing every message.
func TestSessionSealPagesStateUnsealFenceAndAfterSealRelease(t *testing.T) {
	root := repoRoot(t)
	page := filepath.Join("docs", "reference", "adapter-contract.md")
	body := readDocPage(t, filepath.Join(root, page))
	start := strings.Index(body, "**Session seal.**")
	end := strings.Index(body, "**Adapter-to-Gateway events**")
	if start < 0 || end < start {
		t.Fatalf("%s: no Session seal paragraph before the adapter-to-gateway events", page)
	}
	seal := body[start:end]
	for _, want := range []string{
		"still at the attempt's `coordination_generation` and names the attempt's pod",
		"No other caller lifts a seal.",
		"refuses every `message` for the session",
		"`maxSessionAge` bounds a sealed `running` session",
		"also ends work your runtime begins after the seal, whether or not your runtime reports it `busy`",
	} {
		if !strings.Contains(seal, want) {
			t.Errorf("%s: the Session seal paragraph does not state %q", page, want)
		}
	}
	if strings.Contains(body, "its state write does not take effect") {
		t.Errorf("%s: the Session seal paragraph unseals on any missed state write, without the generation and pod fence", page)
	}

	lifecycle := filepath.Join("docs", "runtime-author-guide", "lifecycle.md")
	lbody := readDocPage(t, filepath.Join(root, lifecycle))
	if strings.Contains(lbody, "that it has not reported.") {
		t.Errorf("%s: the idle suspension paragraph implies reported work always survives the release", lifecycle)
	}
	if !strings.Contains(lbody, "any work your runtime begins after the seal, even when it reports that work `busy`") {
		t.Errorf("%s: the idle suspension paragraph does not state that the release ends work begun after the seal", lifecycle)
	}
}

// sessionlessFrameActivityRetired is the statement of the sessionId-less
// activity rule that scoped it to a pod holding one session. Leaked and failed
// slots count against pod occupancy, so a pod can hold one live session and
// more than one slot, and the adapter counts such a frame for no session there.
const sessionlessFrameActivityRetired = "carries no `sessionId` on a pod holding one session"

// sessionlessFrameActivityCurrent is the current statement of the rule, scoped
// to a pod holding at most one slot.
const sessionlessFrameActivityCurrent = "carries no `sessionId` on a pod holding at most one [slot]("

// spec: 4.7.1 (Role and Gateway RPC Contract, Session seal), 6.2 (Pod State
// Machine, Idle suspension), 7.3 (Retry and Resume, Retry accounting, Resume
// driver), 9.2 (Elicitation Chain, Elicitation Timeout Semantics), 27.6
// (Session lifecycle and cleanup, Idle-timeout override)
// diagnosis: a reader page states one of these windows wider or narrower than
// the spec: an idle suspension expiring an elicitation the runtime still waits
// on, a restore failure before the resume driver's `resume_pending` to
// `resuming` write consuming a retry, a sessionId-less frame counting as
// activity on a pod holding one session and a leaked slot, or a playground
// suspension attempt whose checkpoint or suspend write fails releasing the pod.
// A reader plans around an outcome the gateway or the adapter never produces.
func TestReaderPagesStateSealWindowsAndRetryWindowAsSpecified(t *testing.T) {
	root := repoRoot(t)

	tools := filepath.Join("docs", "runtime-author-guide", "platform-tools.md")
	tbody := readDocPage(t, filepath.Join(root, tools))
	if strings.Contains(tbody, "(including an idle suspension)") {
		t.Errorf("%s names idle suspension as a way a pending elicitation expires; the pending call keeps the session from idle suspension", tools)
	}
	if !strings.Contains(tbody, "the wait keeps the session from idle suspension") {
		t.Errorf("%s does not state that a pending elicitation keeps the session from idle suspension", tools)
	}

	lifecycle := filepath.Join("docs", "client-guide", "session-lifecycle.md")
	lbody := readDocPage(t, filepath.Join(root, lifecycle))
	if strings.Contains(lbody, "after the driver has claimed the replacement pod") {
		t.Errorf("%s counts restore failures from the pod claim rather than from the resuming write", lifecycle)
	}
	if !strings.Contains(lbody, "the resume driver's `resume_pending` to `resuming` write") {
		t.Errorf("%s does not anchor the retry accounting on the resume driver's `resume_pending` to `resuming` write", lifecycle)
	}

	for _, page := range busyReportingPages {
		body := readDocPage(t, filepath.Join(root, page))
		if strings.Contains(body, sessionlessFrameActivityRetired) {
			t.Errorf("%s scopes sessionId-less activity to a pod holding one session rather than at most one slot", page)
		}
		if !strings.Contains(body, sessionlessFrameActivityCurrent) {
			t.Errorf("%s does not scope sessionId-less activity to a pod holding at most one slot", page)
		}
	}

	playground := filepath.Join("docs", "operator-guide", "web-playground.md")
	pbody := readDocPage(t, filepath.Join(root, playground))
	if !strings.Contains(pbody, "or whose attempt fails because the workspace checkpoint fails or the session changed before the suspend write, stays `running`") {
		t.Errorf("%s omits the failed suspension attempt that leaves the session `running`", playground)
	}
}
