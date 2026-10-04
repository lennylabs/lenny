// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for the two conditions the specification
// attaches to the `session_started` acknowledgement.
//
// The adapter bounds its wait for `session_started`, and the wait ends by the
// deadline of the request that starts the session only when that request
// carries one. A start request without a deadline is bounded by the adapter's
// own acknowledgement timeout alone, so a page that states the request
// deadline as an unconditional bound tells a reader every start carries one.
//
// The adapter writes a session's CH-RUNTIMEOPS frames only after it reads that
// session's `session_started`, except for a session whose start did not wait
// for the acknowledgement because the runtime's CH-RUNTIMEOPS capability
// handshake had not completed. A page that states the ordering without that
// exception tells a Full-level author that no session-scoped frame can arrive
// before the acknowledgement.
//
// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started), 28.5.3 (CH-RUNTIMEOPS
// Messages)

package tier11_docs_test

import (
	"bufio"
	"path/filepath"
	"strings"
	"testing"
)

// orderingExceptionPhrases are the spellings the docs use for the exception to
// the CH-RUNTIMEOPS ordering against `session_started`.
var orderingExceptionPhrases = []string{
	"except for a session whose start did not wait",
	"The exception is a session whose start did not wait",
}

// spec: 28.5.3 (CH-MSGSOCK Outbound: session_started)
// diagnosis: docs/reference/adapter-contract.md states the request deadline as
//
//	an unconditional bound on the adapter's wait for `session_started`. The
//	specification bounds the wait by that deadline only when the start request
//	carries one; otherwise only the adapter's own acknowledgement timeout
//	applies.
func TestSessionStartedWaitBoundIsConditionalOnARequestDeadline(t *testing.T) {
	contract := adapterContractDoc(t, repoRoot(t))

	const unconditional = "it ends no later than the deadline of the request that starts the session"
	if strings.Contains(contract, unconditional) {
		t.Errorf("adapter-contract.md states the request deadline as an unconditional bound on the session_started wait: %q", unconditional)
	}
	const conditional = "when the request that starts the session carries a deadline, the wait ends no later than that deadline"
	if !strings.Contains(contract, conditional) {
		t.Errorf("adapter-contract.md no longer states the conditional session_started wait bound: want %q", conditional)
	}
}

// spec: 28.5.3 (CH-RUNTIMEOPS Messages), 28.5.3 (CH-MSGSOCK Outbound:
// session_started)
// diagnosis: a docs/ page states that a session's CH-RUNTIMEOPS frames follow
//
//	its `session_started` acknowledgement without the exception for a session
//	whose start did not wait for it. A Full-level author who reads the absolute
//	statement does not expect session-scoped frames before the acknowledgement.
func TestRuntimeOpsOrderingAgainstSessionStartedStatesItsException(t *testing.T) {
	root := repoRoot(t)
	pages := []string{
		filepath.Join("docs", "reference", "adapter-contract.md"),
		filepath.Join("docs", "runtime-author-guide", "integration-levels.md"),
		filepath.Join("docs", "getting-started", "concepts.md"),
		filepath.Join("docs", "api", "internal.md"),
	}
	for _, rel := range pages {
		body := readDocPage(t, filepath.Join(root, rel))
		found := false
		for i, line := range orderingStatementLines(body) {
			found = true
			if !containsAnyTerm(line, orderingExceptionPhrases) {
				t.Errorf("%s: ordering statement %d omits the exception for a start that did not wait: %q", rel, i+1, line)
			}
		}
		if !found {
			t.Errorf("%s: no longer states the CH-RUNTIMEOPS ordering against session_started", rel)
		}
	}
}

// orderingStatementLines returns the lines of body that state a write
// happening "only after" the `session_started` acknowledgement.
func orderingStatementLines(body string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, "only after") {
			continue
		}
		if strings.Contains(line, "session_started") || strings.Contains(line, "acknowledgement") {
			out = append(out, line)
		}
	}
	return out
}
