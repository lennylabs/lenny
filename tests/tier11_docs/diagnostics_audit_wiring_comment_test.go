// SPDX-License-Identifier: MIT

// Tier-11 source-comment consistency check for the §25.6/§25.9
// diagnostics-audit wiring. The wiring in cmd/lenny-ops/deps.go and
// pkg/ops/opsserver/diagnostics_audit.go durably commits each coalesced
// diagnostic audit event to the §11.7 hash chain through
// opsaudit.Recorder (proven by the tier2 component test
// tests/tier2_component/opsaudit/diagnostics_audit_durable_test.go), so
// the comments describing that path must not describe it as an unwired
// stub.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stalePhrases are the wording the pre-fix comments used to describe the
// diagnostics-audit Emit destination as not yet durably wired.
var stalePhrases = []string{
	"audit-append stub",
	"a documented stub until the audit-store client lands",
}

// spec: §11.7 line 360 ("Hash chaining: Each audit log entry includes a
// prev_hash column containing the SHA-256 hash of the previous entry's
// ... tuple.") — the durable hash-chained audit-append path the
// diagnostics-audit Emit destination commits to.
// diagnosis: cmd/lenny-ops/deps.go's buildDiagnosticsAudit comment and
// pkg/ops/opsserver/diagnostics_audit.go's DiagnosticsAuditConfig.Emit
// field comment described the wired, durably-committed §11.7 hash-chain
// append path as "the audit-append stub" / "a documented stub until the
// audit-store client lands." The tier2 component test
// diagnostics_audit_durable_test.go proves the recorder appends these
// events durably through opsaudit.Recorder over auditstore.Store as
// cmd/lenny-ops wires it, so a comment still describing the path as an
// unwired stub misleads a reader about behavior the code does not have:
// the path is wired and durable, not deferred pending a future client.
func TestDiagnosticsAuditWiringCommentsNotStale(t *testing.T) {
	root := repoRoot(t)

	sites := []string{
		filepath.Join(root, "cmd", "lenny-ops", "deps.go"),
		filepath.Join(root, "pkg", "ops", "opsserver", "diagnostics_audit.go"),
	}

	for _, path := range sites {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		// Go line comments wrap across multiple "// " lines; collapse each
		// comment block onto one line so a stale phrase split across a
		// wrap is still matched as a contiguous phrase.
		src := normalizeComments(string(body))
		for _, phrase := range stalePhrases {
			if strings.Contains(src, phrase) {
				t.Errorf("%s: comment still describes the diagnostics-audit append path as an unwired stub (found %q); the path durably commits to the §11.7 hash chain via opsaudit.Recorder", filepath.Base(path), phrase)
			}
		}
	}
}

// normalizeComments strips the leading "// " (and any indentation) from
// each line and joins the result with single spaces, so a phrase that Go
// wraps across two comment lines still reads as one contiguous string for
// substring matching.
func normalizeComments(src string) string {
	lines := strings.Split(src, "\n")
	var b strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimPrefix(trimmed, "//")
		b.WriteString(strings.TrimSpace(trimmed))
		b.WriteString(" ")
	}
	return b.String()
}
