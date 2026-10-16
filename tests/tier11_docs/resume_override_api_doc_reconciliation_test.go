// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the client resume surface on the
// API, error-catalog, and client SDK-example pages.
//
// The gateway restores a recovering session itself: every route into recovery
// writes `resume_pending`, and the coordinating replica's resume driver
// performs the restore. `POST /v1/sessions/{id}/resume` and the
// `resume_session` tool are the client override. They admit
// `awaiting_client_action` and `suspended`, return the session in its new
// state, and leave the restore's outcome to the event stream, so no resume
// call returns a restore failure and the retired restore-failure error code
// has no reader-facing entry. A `suspended` session leaves the
// concurrent-session count, so its resume, whether a resume call or a message
// triggers it, passes the creation-time admission check and is refused with
// `QUOTA_EXCEEDED` when a limit is full.
//
// The tests read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 15.1 (REST API), 15.2 (MCP API), 7.2 (Interactive Session Model),
// 7.3 (Retry and Resume), 11.1 (Admission and Fairness), 28.5.3 (Intra-pod)

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// retiredResumeErrorCode is the error code the resume call returned when a
// synchronous restore failed. The restore now runs in the resume driver and
// reports its outcome on the event stream, so the code names nothing.
const retiredResumeErrorCode = "RESUME_FAILED"

// resumeSurfacePages are the reader pages that document the client resume
// surface. None of them may name the retired restore-failure code.
var resumeSurfacePages = []string{
	filepath.Join("docs", "api", "rest.md"),
	filepath.Join("docs", "api", "mcp.md"),
	filepath.Join("docs", "api", "internal.md"),
	filepath.Join("docs", "reference", "error-catalog.md"),
	filepath.Join("docs", "client-guide", "sdk-examples", "curl.md"),
	filepath.Join("docs", "client-guide", "sdk-examples", "go.md"),
	filepath.Join("docs", "client-guide", "sdk-examples", "python.md"),
	filepath.Join("docs", "client-guide", "sdk-examples", "typescript.md"),
}

// docHeadingSection returns the text of the section that opens with the exact
// heading line, up to the next heading at the same depth or shallower. It
// fails the test when the heading is absent, because a renamed heading would
// otherwise turn every check on the section into a silent pass.
func docHeadingSection(t *testing.T, page, body, heading string) string {
	t.Helper()
	depth := len(heading) - len(strings.TrimLeft(heading, "#"))
	lines := strings.Split(body, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimRight(l, " ") == heading {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s: no heading %q", page, heading)
	}
	for i := start + 1; i < len(lines); i++ {
		l := lines[i]
		if !strings.HasPrefix(l, "#") {
			continue
		}
		d := len(l) - len(strings.TrimLeft(l, "#"))
		if d <= depth && strings.HasPrefix(l[d:], " ") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	return strings.Join(lines[start:], "\n")
}

// tableRow returns the markdown table row whose first cell is the backticked
// code, or fails the test when the page has no such row.
func tableRow(t *testing.T, page, body, code string) string {
	t.Helper()
	prefix := "| `" + code + "` |"
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("%s: no table row for %s", page, code)
	return ""
}

// spec: 15.1 (REST API, error code catalog), 7.3 (Retry and Resume, Resume
// driver)
// diagnosis: a reader page still names the restore-failure error code that the
// resume call no longer returns. A client that branches on it handles a
// response that never arrives and misses the `status_change` event that
// carries the restore's outcome.
func TestResumeSurfacePagesNameNoRetiredRestoreFailureCode(t *testing.T) {
	root := repoRoot(t)
	for _, page := range resumeSurfacePages {
		body := readDocPage(t, filepath.Join(root, page))
		if strings.Contains(body, retiredResumeErrorCode) {
			t.Errorf("%s names %s, which no resume call returns", page, retiredResumeErrorCode)
		}
	}
	catalog := filepath.Join("docs", "reference", "error-catalog.md")
	setup := tableRow(t, catalog, readDocPage(t, filepath.Join(root, catalog)), "SETUP_COMMAND_FAILED")
	if strings.Contains(setup, "/resume") {
		t.Errorf("%s: the SETUP_COMMAND_FAILED row names the resume endpoint, which returns no setup failure", catalog)
	}
}

// spec: 15.1 (REST API, State-mutating endpoint preconditions), 11.1
// (Admission and Fairness, Resume admission)
// diagnosis: the REST resume or message entry disagrees with the spec's
// preconditions table or omits the concurrent-session refusal. A client reads
// that a `suspended` session cannot be resumed through the call, or meets a
// 429 the page does not list.
func TestRESTResumeEntryAdmitsSuspendedAndListsQuotaRefusal(t *testing.T) {
	root := repoRoot(t)
	spec := readDocPage(t, filepath.Join(root, "spec", "15_external-api-surface.md"))
	const specRow = "| `POST /v1/sessions/{id}/resume`    | `awaiting_client_action`, `suspended`"
	if !strings.Contains(spec, specRow) {
		t.Fatalf("spec/15: the resume preconditions row no longer admits `awaiting_client_action`, `suspended`; re-derive this check")
	}

	page := filepath.Join("docs", "api", "rest.md")
	body := readDocPage(t, filepath.Join(root, page))
	if !strings.Contains(body, "`/v1/sessions/{id}/resume`    | Resume a session in `awaiting_client_action` or `suspended` |") {
		t.Errorf("%s: the endpoint table row for resume does not name both precondition states", page)
	}
	resume := docHeadingSection(t, page, body, "### POST /v1/sessions/{id}/resume")
	for _, want := range []string{
		"**Valid precondition states:** `awaiting_client_action`, `suspended`.",
		"`suspended` to `running` when the pod is still held",
		"`status_change` event",
		"`QUOTA_EXCEEDED` (429)",
		"new key",
	} {
		if !strings.Contains(resume, want) {
			t.Errorf("%s: resume entry lacks %q", page, want)
		}
	}
	if strings.Contains(resume, `delivery: "immediate"`) {
		t.Errorf("%s: resume entry ties waking a suspended session to an immediate delivery, while any message resumes it", page)
	}
	if strings.Index(resume, "automatically") > strings.Index(resume, "client override") {
		t.Errorf("%s: resume entry presents the client override before automatic recovery", page)
	}

	messages := docHeadingSection(t, page, body, "### POST /v1/sessions/{id}/messages")
	if !strings.Contains(messages, "`QUOTA_EXCEEDED` (429)") {
		t.Errorf("%s: messages entry does not list the concurrent-session refusal of a suspended session's resume", page)
	}
}

// spec: 15.2 (MCP API), 11.1 (Admission and Fairness, Resume admission)
// diagnosis: the MCP tool entries omit the concurrent-session refusal or still
// describe `resume_session` from `suspended` as resuming the runtime in place.
// A client that resumes a released session expects `running` and receives
// `resume_pending`.
func TestMCPResumeAndSendMessageListQuotaRefusal(t *testing.T) {
	root := repoRoot(t)
	page := filepath.Join("docs", "api", "mcp.md")
	body := readDocPage(t, filepath.Join(root, page))

	resume := docHeadingSection(t, page, body, "### `resume_session`")
	for _, want := range []string{
		"`suspended`, with its pod still held",
		"`suspended`, with its pod released",
		"| `QUOTA_EXCEEDED` |",
	} {
		if !strings.Contains(resume, want) {
			t.Errorf("%s: resume_session entry lacks %q", page, want)
		}
	}
	if strings.Contains(resume, "Resumes the agent runtime.") {
		t.Errorf("%s: resume_session entry keeps the pre-driver suspended bullet", page)
	}

	send := docHeadingSection(t, page, body, "### `send_message`")
	if !strings.Contains(send, "| `QUOTA_EXCEEDED` |") {
		t.Errorf("%s: send_message entry does not list the concurrent-session refusal", page)
	}
}

// spec: 15.1 (REST API, error code catalog), 11.1 (Admission and Fairness,
// Resume admission)
// diagnosis: the error catalog describes `QUOTA_EXCEEDED` as a creation-time
// refusal only. An operator who sees it on a resume or a message to a
// suspended session finds no entry explaining it or naming the remedy.
func TestErrorCatalogQuotaRowNamesRefusedResume(t *testing.T) {
	root := repoRoot(t)
	page := filepath.Join("docs", "reference", "error-catalog.md")
	row := tableRow(t, page, readDocPage(t, filepath.Join(root, page)), "QUOTA_EXCEEDED")
	for _, want := range []string{"resume of a `suspended` session", "end another session"} {
		if !strings.Contains(row, want) {
			t.Errorf("%s: QUOTA_EXCEEDED row lacks %q", page, want)
		}
	}
}

// spec: 7.3 (Retry and Resume, Resume driver), 28.5.3 (Intra-pod, CH-MSGSOCK
// Timing and Inbound: heartbeat)
// diagnosis: the internal API page still states that a recovering session is
// re-attached only when a pod is allocated inside the window, or that a missed
// heartbeat acknowledgment acts only on an open stream. A reader expects a
// session with no open stream to escape the hung-runtime refusal.
func TestInternalAPIStreamAndHeartbeatBulletsMatchResumeDriver(t *testing.T) {
	root := repoRoot(t)
	page := filepath.Join("docs", "api", "internal.md")
	body := collapseWhitespace(readDocPage(t, filepath.Join(root, page)))
	for _, want := range []string{
		"restored onto a replacement pod by the gateway's resume driver",
		"by rebuilding its workspace from the session's workspace plan",
		"whether or not an Attach stream for the session is open",
		"refuses each later Attach for the session with `DEADLINE_EXCEEDED`",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s: lacks %q", page, want)
		}
	}
	if strings.Contains(body, "when a pod is allocated within `maxResumeWindowSeconds`") {
		t.Errorf("%s: keys the resume window's end on pod allocation", page)
	}
}

// spec: 15.1 (REST API, State-mutating endpoint preconditions)
// diagnosis: an SDK-example page describes the resume call as recovering only
// a session after retry exhaustion, or as always returning `running`. A
// client author never uses it on a suspended session, or waits on a state the
// call does not return.
func TestSDKExampleResumeRowsNameBothPreconditionStates(t *testing.T) {
	root := repoRoot(t)
	curl := filepath.Join("docs", "client-guide", "sdk-examples", "curl.md")
	if body := readDocPage(t, filepath.Join(root, curl)); !strings.Contains(body, "\n### Resume\n") ||
		strings.Contains(body, "Retry Exhaustion") {
		t.Errorf("%s: the resume heading still scopes the call to retry exhaustion", curl)
	}
	const row = "Moves a session in `awaiting_client_action`, or a `suspended` session whose pod was released, to `resume_pending`, and a `suspended` session whose pod is held to `running`."
	for _, name := range []string{"go.md", "python.md", "typescript.md"} {
		page := filepath.Join("docs", "client-guide", "sdk-examples", name)
		body := readDocPage(t, filepath.Join(root, page))
		if !strings.Contains(body, row) {
			t.Errorf("%s: the resume row does not state both precondition states and their transitions", page)
		}
		if strings.Contains(body, "A session awaiting client action back to `running`.") {
			t.Errorf("%s: keeps the retry-exhaustion-only resume row", page)
		}
	}
}
