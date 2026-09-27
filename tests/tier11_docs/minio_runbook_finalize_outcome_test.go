// SPDX-License-Identifier: MIT

package tier11_docs_test

import (
	"strings"
	"testing"
)

// minioRunbookSection returns the body of the `### ` heading in the MinIO
// failure runbook whose text starts with prefix, up to the next `### ` or
// `## ` heading. It returns "" when no such heading exists.
func minioRunbookSection(body, prefix string) string {
	start := -1
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if start < 0 {
			if strings.HasPrefix(line, "### "+prefix) {
				start = i + 1
			}
			continue
		}
		if strings.HasPrefix(line, "### ") || strings.HasPrefix(line, "## ") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start < 0 {
		return ""
	}
	return strings.Join(lines[start:], "\n")
}

// diagnosis: the MinIO failure runbook's full-outage tenant notice names an
// error code for a failed finalize again. §17.7 states that a sustained MinIO
// outage fails the upload call first (INTERNAL_ERROR, then 503 once the Upload
// Handler circuit breaker opens) and that a finalize whose workspace cannot be
// materialized answers the retryable SESSION_CREATION_FAILED, so a notice that
// ties INTERNAL_ERROR to finalize contradicts the spec and the gateway.
//
// spec: §17.7 (MinIO failure runbook), §16.5 (MinIOUnavailable alert)
func TestMinIORunbookFullOutageNoticeNamesNoFinalizeInternalError_spec_17_7(t *testing.T) {
	body := readRepoFile(t, repoRoot(t), "docs", "runbooks", "minio-failure.md")
	section := minioRunbookSection(body, "Step 2 — Full outage")
	if section == "" {
		t.Fatalf("docs/runbooks/minio-failure.md: no `### Step 2 — Full outage` section found")
	}
	for _, line := range strings.Split(section, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "finalize") && strings.Contains(line, "INTERNAL_ERROR") {
			t.Errorf("full-outage section ties INTERNAL_ERROR to finalize, which §17.7 answers with SESSION_CREATION_FAILED: %q", strings.TrimSpace(line))
		}
	}
	if !strings.Contains(section, "Inform affected tenants: new session creation is degraded.") {
		t.Errorf("full-outage section lost the tenant notice that new session creation is degraded (§17.7 remediation)")
	}
}
