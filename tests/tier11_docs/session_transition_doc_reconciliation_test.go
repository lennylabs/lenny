// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the session transitions and the
// client contract of a failed start. §7.2 routes POST /v1/sessions/{id}/start
// through `starting` and gives every start failure the `starting → failed`
// edge, and lists the terminate edge `input_required → completed`. §15.1
// makes STARTING_FAILED a session that is `failed`, so a retry creates a new
// session. §15.2 and §15.6 make each client SDK's Start send its request once.
// §8.8 maps the persisted `input_required` sub-state to the MCP Tasks
// `input_required` state, and get_session_status returns it verbatim.
//
// These tests read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: §7.2, §15.1, §15.2, §15.6, §8.8

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// docLineWith returns the single line of doc that starts with prefix, failing
// the test when no line or more than one line matches.
func docLineWith(t *testing.T, doc, prefix string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, prefix) {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one line starting with %q, found %d", prefix, len(found))
	}
	return found[0]
}

// docParagraphWith returns the blank-line-delimited paragraph of doc that
// starts with prefix, failing the test when none does.
func docParagraphWith(t *testing.T, doc, prefix string) string {
	t.Helper()
	for _, para := range strings.Split(doc, "\n\n") {
		if strings.HasPrefix(strings.TrimSpace(para), prefix) {
			return strings.Join(strings.Fields(para), " ")
		}
	}
	t.Fatalf("no paragraph starts with %q", prefix)
	return ""
}

// TestStartingFailedCatalogRowCreatesANewSession_spec_15_1 pins the
// STARTING_FAILED catalog row to the §15.1 contract: the session is failed and
// a retry is a new session rather than a same-session rebind.
//
// spec: §15.1, §7.2
func TestStartingFailedCatalogRowCreatesANewSession_spec_15_1(t *testing.T) {
	doc := readDocPage(t, filepath.Join(repoRoot(t), "docs", "reference", "error-catalog.md"))
	row := docLineWith(t, doc, "| `STARTING_FAILED` |")
	for _, want := range []string{
		"The session is `failed`, so a retry creates a new session, which binds a fresh pod.",
		"| Create a new session after the `Retry-After` window. |",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("STARTING_FAILED row lacks %q:\n%s", want, row)
		}
	}
	if strings.Contains(row, "a retry rebinds a fresh pod") {
		t.Errorf("STARTING_FAILED row still describes a same-session rebind:\n%s", row)
	}
}

// TestSDKRetriesParagraphSaysStartSendsOnce_spec_15_6 pins each client SDK
// page's Retries paragraph to the single-attempt Start contract.
//
// spec: §15.2, §15.6, §7.2
func TestSDKRetriesParagraphSaysStartSendsOnce_spec_15_6(t *testing.T) {
	root := repoRoot(t)
	for page, method := range map[string]string{
		"go.md":         "`Start`",
		"python.md":     "`start`",
		"typescript.md": "`start`",
	} {
		t.Run(page, func(t *testing.T) {
			doc := readDocPage(t, filepath.Join(root, "docs", "client-guide", "sdk-examples", page))
			para := docParagraphWith(t, doc, "The client retries a retryable error on its own")
			for _, want := range []string{
				method + " sends its request once and is not retried",
				"creates a new session when it is `failed`",
				"(../../reference/state-machines.md)",
			} {
				if !strings.Contains(para, want) {
					t.Errorf("Retries paragraph lacks %q:\n%s", want, para)
				}
			}
			if strings.Contains(para, "§") {
				t.Errorf("Retries paragraph cites a spec section number:\n%s", para)
			}
		})
	}
}

// TestSessionDocsCarryStartFailureAndInputRequiredTerminate_spec_7_2 pins the
// §7.2 `starting → failed` and `input_required → completed` edges in the
// state-machine reference, the concepts page, and the session-lifecycle guide.
//
// spec: §7.2
func TestSessionDocsCarryStartFailureAndInputRequiredTerminate_spec_7_2(t *testing.T) {
	root := repoRoot(t)
	sm := readDocPage(t, filepath.Join(root, "docs", "reference", "state-machines.md"))
	for _, want := range []string{
		"| `starting` | `failed` | Start fails or times out | `POST /v1/sessions/{id}/start`, or internal timer |",
		"| `input_required` | `completed` | Terminate | `POST /v1/sessions/{id}/terminate` |",
		"    starting --> failed : Start fails or times out\n",
	} {
		if !strings.Contains(sm, want) {
			t.Errorf("state-machines.md lacks %q", want)
		}
	}
	// The session diagram and the canonical task diagram each carry the
	// terminate edge out of input_required.
	if n := strings.Count(sm, "    input_required --> completed : terminate\n"); n != 2 {
		t.Errorf("state-machines.md carries %d `input_required --> completed` diagram lines, want 2", n)
	}

	concepts := readDocPage(t, filepath.Join(root, "docs", "getting-started", "concepts.md"))
	if !strings.Contains(concepts, "    input_required --> completed: Terminate\n") {
		t.Errorf("concepts.md session diagram lacks the input_required terminate edge")
	}

	lifecycle := readDocPage(t, filepath.Join(root, "docs", "client-guide", "session-lifecycle.md"))
	if !strings.Contains(lifecycle, "    starting --> failed : start fails or times out\n") {
		t.Errorf("session-lifecycle.md session diagram lacks the starting failure edge")
	}
	para := docParagraphWith(t, lifecycle, "While in `input_required`")
	if !strings.Contains(para, "`completed` (on `POST /v1/sessions/{id}/terminate`)") {
		t.Errorf("session-lifecycle.md input_required transitions omit terminate:\n%s", para)
	}
}

// TestMCPDocsListInputRequiredStatus_spec_8_8 pins the get_session_status
// status values and the Task lifecycle mapping to the persisted
// `input_required` sub-state.
//
// spec: §8.8, §15.2, §7.2
func TestMCPDocsListInputRequiredStatus_spec_8_8(t *testing.T) {
	doc := readDocPage(t, filepath.Join(repoRoot(t), "docs", "api", "mcp.md"))
	statuses := docLineWith(t, doc, "Possible `status` values: `created`")
	if !strings.Contains(statuses, "`running`, `input_required`, `suspended`") {
		t.Errorf("get_session_status status values omit input_required:\n%s", statuses)
	}
	if row := docLineWith(t, doc, "| `input_required` |"); row != "| `input_required` | Task requires input |" {
		t.Errorf("Task lifecycle mapping input_required row = %q", row)
	}
}
