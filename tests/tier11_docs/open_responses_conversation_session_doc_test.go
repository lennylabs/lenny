// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for the Open Responses API page's account of
// the conversation session.
//
// An Open Responses conversation is a session that keeps its pod between
// turns. A continuation of the newest response of a running session goes to
// that session warm, and every other continuation starts a new session seeded
// from the stored conversation. A response's status comes from its own turn,
// and a turn that fails after delivery ends the session. A page that still
// describes a session terminated at the end of every request, a status table
// keyed by session state, or a retried pod failure tells a client the wrong
// cost and failure model for a multi-turn conversation.
//
// spec: 15 (Open Responses conversation sessions)

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// openResponsesPage reads docs/api/open-responses.md.
func openResponsesPage(t *testing.T) string {
	t.Helper()
	return readDocPage(t, filepath.Join(repoRoot(t), "docs", "api", "open-responses.md"))
}

// rejectPhrases reports every phrase in banned that text still contains,
// naming where the text came from.
func rejectPhrases(t *testing.T, where, text string, banned []string) {
	t.Helper()
	for _, phrase := range banned {
		if strings.Contains(text, phrase) {
			t.Errorf("%s: still contains %q", where, phrase)
		}
	}
}

// spec: 15 (Open Responses conversation sessions)
// diagnosis: the Session mapping section of docs/api/open-responses.md again
//
//	ends every request with terminate_session(), or no longer says that the
//	session keeps its pod between turns under the ordinary idle, suspension,
//	and maximum-age limits, or drops the service-mode and non-multi-turn
//	cases in which the session ends after every response.
func TestOpenResponsesSessionMappingDescribesTheHeldConversationSession(t *testing.T) {
	body := section(openResponsesPage(t), "## Session mapping")
	requirePhrases(t, "open-responses.md Session mapping", body, []string{
		"Each Open Responses conversation maps to a Lenny session",
		"the session keeps its pod between turns",
		"(the session stays running and keeps its pod)",
		"the same idle, suspension, and maximum-age limits as any other Lenny session",
		"A pool that runs in service mode keeps no conversation context between requests",
		"the session ends after every response",
	})
	rejectPhrases(t, "open-responses.md Session mapping", body, []string{
		"terminate_session()",
		"Each Open Responses request maps to a Lenny session lifecycle",
	})
}

// spec: 15 (Open Responses conversation sessions)
// diagnosis: the Response status mapping section of docs/api/open-responses.md
//
//	again maps session states to response statuses, or no longer says that a
//	response's status comes from its own turn and that GET returns completed
//	responses only. A client then reads a later suspension or expiry of the
//	session as a change to a response it already received.
func TestOpenResponsesStatusComesFromTheResponsesOwnTurn(t *testing.T) {
	body := section(openResponsesPage(t), "### Response status mapping")
	requirePhrases(t, "open-responses.md Response status mapping", body, []string{
		"A response's status comes from its own turn",
		"| The turn is running | `in_progress` |",
		"| The turn's output was recorded | `completed` |",
		"`GET /v1/responses/{id}` returns completed responses",
		"returns `404`",
	})
	rejectPhrases(t, "open-responses.md Response status mapping", body, []string{
		"Lenny session state",
		"`resume_pending`",
	})
}

// spec: 15 (Open Responses conversation sessions)
// diagnosis: the Multi-turn section of docs/api/open-responses.md again says
//
//	that every continuation creates a new session, or no longer names the
//	warm case and the cold cases (fork, concurrent continuation, suspended,
//	restored, or ended session), or no longer says that response ids are
//	opaque. A client then parses the id or expects a pod claim per message.
func TestOpenResponsesMultiTurnNamesTheWarmAndColdContinuations(t *testing.T) {
	page := openResponsesPage(t)
	body := section(page, "### Multi-turn via `previous_response_id`")
	requirePhrases(t, "open-responses.md Multi-turn", body, []string{
		"names the newest response of a conversation session that is still running, the gateway sends the new input to the same session and pod",
		"In every other case the gateway starts a new session and seeds it with the stored conversation up to the named response",
		"a continuation of an earlier response (a fork)",
		"a second continuation sent while another continuation of the same response is in flight",
		"suspended, restored onto a new pod, or ended",
		"Response ids are opaque strings",
	})
	rejectPhrases(t, "open-responses.md Multi-turn", body, []string{
		"A new session is created with the combined context",
	})
	requirePhrases(t, "open-responses.md POST /v1/responses", section(page, "### POST /v1/responses"), []string{
		"A request without `previous_response_id` creates a conversation session",
		"either on the session that produced that response or on a new session",
	})
}

// spec: 15 (Open Responses conversation sessions)
// diagnosis: the checkpointing bullet of docs/api/open-responses.md again
//
//	promises that pod failures are retried transparently, or no longer says
//	that a turn failing after its input reached the runtime returns a server
//	error and ends the session.
func TestOpenResponsesCheckpointingBulletStatesTheTurnFailureRule(t *testing.T) {
	body := section(openResponsesPage(t), "## Task lifecycle integration")
	requirePhrases(t, "open-responses.md Task lifecycle integration", body, []string{
		"A turn that fails after its input reached the runtime returns a server error, and the gateway ends that session",
		"a continuation of an earlier completed response then starts a new session",
	})
	rejectPhrases(t, "open-responses.md Task lifecycle integration", body, []string{
		"Pod failures are retried transparently",
	})
}
