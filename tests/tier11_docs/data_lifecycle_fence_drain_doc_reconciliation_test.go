// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the reader pages that document the
// lifecycle fence and drain, the durable user-erasure job, and the tenant
// suspend response.
//
// The spec makes tenant suspension, tenant-deletion Phase 2, and user erasure
// share one drain that force-terminates the scope's live sessions, keeps a
// failed Phase 2 drain in phase for the next controller pass, and fails an
// erasure job whose drain or legal-hold re-check does not pass. It catalogues
// `TENANT_NOT_ACTIVE`, `USER_INVALIDATED`, and `ERASURE_JOB_NOT_FAILED`, and
// it defines the suspend response's `drainedSessions` and `failedSessions`.
// Before these pages were reconciled, the phase table described a
// graceful-shutdown wait with a five-minute timeout, the error tables omitted
// the three codes, the admin page had no suspend entry, and the erasure
// runbook told the operator that clearing the restriction retries the job.
//
// The tests read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 12.8 (Compliance Interfaces), 15.1 (REST API), 16.5 (Alerting Rules
// and SLOs)

package tier11_docs_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// lifecycleCreationCodes are the session-creation refusal codes the creation
// fence answers, in addition to ERASURE_IN_PROGRESS.
var lifecycleCreationCodes = []string{"USER_INVALIDATED", "TENANT_SUSPENDED", "TENANT_NOT_ACTIVE"}

// lifecycleDocPages are the reader pages this reconciliation edits. None may
// cite a spec section number in prose.
var lifecycleDocPages = []string{
	filepath.Join("docs", "operator-guide", "multi-tenancy.md"),
	filepath.Join("docs", "operator-guide", "security.md"),
	filepath.Join("docs", "reference", "error-catalog.md"),
	filepath.Join("docs", "api", "index.md"),
	filepath.Join("docs", "api", "mcp.md"),
	filepath.Join("docs", "api", "rest.md"),
	filepath.Join("docs", "api", "admin.md"),
	filepath.Join("docs", "runbooks", "erasure-job-failed.md"),
	filepath.Join("docs", "runbooks", "tenant-deletion-overdue.md"),
}

// specCatalogCells returns the category and HTTP status cells of the §15.1
// error-code catalog row for code, failing when the catalog has no such row.
func specCatalogCells(t *testing.T, spec, code string) (category, status string) {
	t.Helper()
	re := regexp.MustCompile("(?m)^\\|\\s*`" + regexp.QuoteMeta(code) + "`\\s*\\|\\s*`([A-Z_]+)`\\s*\\|\\s*(\\d{3})\\s*\\|")
	m := re.FindStringSubmatch(spec)
	if m == nil {
		t.Fatalf("spec/15: no catalog row for %s; re-derive this check", code)
	}
	return m[1], m[2]
}

// spec: 12.8 (Compliance Interfaces, Tenant deletion lifecycle; Lifecycle
// fence and drain)
// diagnosis: the multi-tenancy phase table still describes the retired Phase
// 2 graceful-shutdown wait, or omits that a drain that does not complete
// keeps the deletion in Phase 2 for the next controller pass. An operator
// waits for a timeout that never fires.
func TestTenantDeletionPhaseTwoRowDescribesDrainAndRetry(t *testing.T) {
	root := repoRoot(t)
	spec := readDocPage(t, filepath.Join(root, "spec", "12_storage-architecture.md"))
	if !strings.Contains(spec, "When the drain does not succeed, the job stays in this phase and the controller runs the drain again on its next pass.") {
		t.Fatalf("spec/12: the Phase 2 row no longer keeps a failed drain in phase; re-derive this check")
	}
	page := filepath.Join("docs", "operator-guide", "multi-tenancy.md")
	row := ""
	for _, l := range strings.Split(readDocPage(t, filepath.Join(root, page)), "\n") {
		if strings.HasPrefix(l, "| 2. Drain active sessions |") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("%s: no Phase 2 row in the tenant deletion phase table", page)
	}
	for _, want := range []string{
		"Force-terminate every active session in the tenant.",
		"The phase completes when no active session remains.",
		"the phase stays incomplete and the controller retries it on its next pass",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("%s: Phase 2 row lacks %q", page, want)
		}
	}
	for _, retired := range []string{"graceful-shutdown", "5 min"} {
		if strings.Contains(row, retired) {
			t.Errorf("%s: Phase 2 row still names the retired %q wait", page, retired)
		}
	}
}

// spec: 15.1 (REST API, Error code catalog), 15.2.1 (REST/MCP consistency
// contract, rule 5)
// diagnosis: a reader error table omits a creation-fence refusal code or
// gives it a category or status other than the spec catalog's. A client
// cannot recognize the refusal or treats it as retryable.
func TestLifecycleRefusalCodesListedOnErrorPages(t *testing.T) {
	root := repoRoot(t)
	spec := readDocPage(t, filepath.Join(root, "spec", "15_external-api-surface.md"))

	catalogPage := filepath.Join("docs", "reference", "error-catalog.md")
	catalog := readDocPage(t, filepath.Join(root, catalogPage))
	indexPage := filepath.Join("docs", "api", "index.md")
	index := readDocPage(t, filepath.Join(root, indexPage))

	for _, code := range append(append([]string{}, lifecycleCreationCodes...), "ERASURE_JOB_NOT_FAILED") {
		category, status := specCatalogCells(t, spec, code)
		if row := tableRow(t, catalogPage, catalog, code); !strings.Contains(row, "| "+status+" |") {
			t.Errorf("%s: %s row does not carry status %s", catalogPage, code, status)
		}
		want := "| `" + code + "` | `" + category + "` | " + status + " | No |"
		if row := tableRow(t, indexPage, index, code); !strings.HasPrefix(row, want) {
			t.Errorf("%s: %s row = %q, want prefix %q", indexPage, code, row, want)
		}
	}

	notActive := tableRow(t, catalogPage, catalog, "TENANT_NOT_ACTIVE")
	for _, want := range []string{"`details.tenantId`", "`details.state`"} {
		if !strings.Contains(notActive, want) {
			t.Errorf("%s: TENANT_NOT_ACTIVE row lacks %s", catalogPage, want)
		}
	}
	if !strings.Contains(tableRow(t, catalogPage, catalog, "USER_INVALIDATED"), "Not retryable") {
		t.Errorf("%s: USER_INVALIDATED row does not state that it is not retryable", catalogPage)
	}
	if !strings.Contains(tableRow(t, catalogPage, catalog, "ERASURE_BLOCKED_BY_LEGAL_HOLD"), "`POST /v1/admin/erasure-jobs/{jobId}/retry`") {
		t.Errorf("%s: ERASURE_BLOCKED_BY_LEGAL_HOLD row does not name the retry endpoint", catalogPage)
	}
	erasing := tableRow(t, catalogPage, catalog, "ERASURE_IN_PROGRESS")
	if !strings.Contains(erasing, "source session of a derive or replay") {
		t.Errorf("%s: ERASURE_IN_PROGRESS row does not name the derive or replay source owner", catalogPage)
	}

	mcpPage := filepath.Join("docs", "api", "mcp.md")
	mcp := readDocPage(t, filepath.Join(root, mcpPage))
	restPage := filepath.Join("docs", "api", "rest.md")
	rest := readDocPage(t, filepath.Join(root, restPage))
	for _, code := range lifecycleCreationCodes {
		tableRow(t, mcpPage, mcp, code)
		if !strings.Contains(rest, "`"+code+"` (403)") {
			t.Errorf("%s: the session-creation key error codes omit %s", restPage, code)
		}
	}
}

// spec: 15.1 (REST API, POST /v1/admin/tenants/{id}/suspend)
// diagnosis: the admin page omits the suspend entry or a field of its
// response. An operator cannot tell an incomplete drain from a complete one or
// does not know that repeating the request retries the drain.
func TestAdminSuspendEntryDocumentsDrainResponse(t *testing.T) {
	root := repoRoot(t)
	page := filepath.Join("docs", "api", "admin.md")
	body := readDocPage(t, filepath.Join(root, page))
	sec := docHeadingSection(t, page, body, "### POST /v1/admin/tenants/{id}/suspend")
	for _, want := range []string{
		"`drainedSessions`",
		"`failedSessions`",
		"runs the session drain again",
		"A non-empty array means the drain is incomplete and the request can be repeated.",
		"`500 INTERNAL_ERROR`",
		"`403 TENANT_SUSPENDED`",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("%s: suspend entry lacks %q", page, want)
		}
	}
}

// spec: 12.8 (Compliance Interfaces, User-level erasure invocation), 15.1
// (REST API)
// diagnosis: the admin page omits a 409 the erase, retry, or
// clear-processing-restriction endpoint returns. An operator retries a job
// that is still running or clears a restriction another job set.
func TestAdminErasureEntriesDocumentJobConflicts(t *testing.T) {
	root := repoRoot(t)
	page := filepath.Join("docs", "api", "admin.md")
	body := readDocPage(t, filepath.Join(root, page))

	erase := docHeadingSection(t, page, body, "### POST /v1/admin/users/{userId}/erase")
	for _, want := range []string{"returns that job's ID and starts no second job", "`acknowledgeHoldOverride`", "`409 INVALID_STATE_TRANSITION`"} {
		if !strings.Contains(erase, want) {
			t.Errorf("%s: erase entry lacks %q", page, want)
		}
	}
	retry := tableRowByPrefix(t, page, body, "| `POST /v1/admin/erasure-jobs/{jobId}/retry` |")
	for _, want := range []string{"`409 ERASURE_JOB_NOT_FAILED`", "`409 INVALID_STATE_TRANSITION`", "`409 ERASURE_BLOCKED_BY_LEGAL_HOLD`"} {
		if !strings.Contains(retry, want) {
			t.Errorf("%s: retry row lacks %q", page, want)
		}
	}
	clear := tableRowByPrefix(t, page, body, "| `POST /v1/admin/erasure-jobs/{jobId}/clear-processing-restriction` |")
	for _, want := range []string{"`409 ERASURE_JOB_NOT_FAILED`", "`409 INVALID_STATE_TRANSITION`", "does not retry the job"} {
		if !strings.Contains(clear, want) {
			t.Errorf("%s: clear-processing-restriction row lacks %q", page, want)
		}
	}
}

// spec: 12.8 (Compliance Interfaces, Processing restriction during erasure
// (GDPR Article 18)), 16.5 (Alerting Rules and SLOs, ErasureJobFailed)
// diagnosis: the security page or the erasure runbook omits the drain step or
// still says that clearing the restriction retries the job. An operator
// clears the restriction expecting a retry, and the job stays failed while
// the user is reopened to new sessions.
func TestErasureDrainAndRecoveryDocumented(t *testing.T) {
	root := repoRoot(t)
	secPage := filepath.Join("docs", "operator-guide", "security.md")
	sec := docHeadingSection(t, secPage, readDocPage(t, filepath.Join(root, secPage)), "### Erasure Process")
	if !strings.Contains(sec, "force-terminates the user's running sessions") || !strings.Contains(sec, "the job fails and the processing restriction stays set") {
		t.Errorf("%s: Erasure Process does not state the drain and its failure outcome", secPage)
	}

	rbPage := filepath.Join("docs", "runbooks", "erasure-job-failed.md")
	rb := readDocPage(t, filepath.Join(root, rbPage))
	if strings.Contains(rb, "so the controller can retry") {
		t.Errorf("%s: still states that clearing the restriction lets the controller retry", rbPage)
	}
	for _, want := range []string{
		"**Session drain incomplete**",
		"**Legal hold found by the job's re-check**",
		"Clearing the processing restriction retries nothing.",
		`"acknowledgeHoldOverride": true`,
		"A later retry of the job sets the restriction again.",
	} {
		if !strings.Contains(rb, want) {
			t.Errorf("%s: lacks %q", rbPage, want)
		}
	}
}

// spec: 12.8 (Compliance Interfaces, Tenant deletion lifecycle), 16.5
// (Alerting Rules and SLOs, TenantDeletionOverdue)
// diagnosis: the tenant-deletion runbook omits the Phase 2 drain stall. An
// operator investigating an overdue deletion whose tenant stays `disabling`
// has no cause to match it against.
func TestTenantDeletionRunbookNamesPhaseTwoDrainStall(t *testing.T) {
	root := repoRoot(t)
	page := filepath.Join("docs", "runbooks", "tenant-deletion-overdue.md")
	body := readDocPage(t, filepath.Join(root, page))
	for _, want := range []string{
		"**Phase 2 session drain incomplete.**",
		"stays `disabling`",
		"logs a warning naming the tenant",
		"runs the drain again on its next pass",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s: lacks %q", page, want)
		}
	}
}

// spec: 12.8 (Compliance Interfaces), 15.1 (REST API)
// diagnosis: a reconciled reader page cites a spec section number in prose.
// Section numbers are internal to the spec and shift, so the citation stops
// resolving for a reader who does not have the spec.
func TestLifecycleDocPagesCiteNoSpecSectionNumbers(t *testing.T) {
	root := repoRoot(t)
	cite := regexp.MustCompile(`§\d`)
	for _, page := range lifecycleDocPages {
		inFence := false
		for i, l := range strings.Split(readDocPage(t, filepath.Join(root, page)), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "```") {
				inFence = !inFence
				continue
			}
			if inFence || !cite.MatchString(l) {
				continue
			}
			if containsLifecycleTerm(l) {
				t.Errorf("%s:%d cites a spec section number: %s", page, i+1, l)
			}
		}
	}
}

// containsLifecycleTerm reports whether a line belongs to the content this
// reconciliation added, so the section-number check does not fail on
// citations elsewhere on a page that predate it.
func containsLifecycleTerm(l string) bool {
	for _, term := range []string{
		"TENANT_NOT_ACTIVE", "USER_INVALIDATED", "ERASURE_JOB_NOT_FAILED", "TENANT_SUSPENDED",
		"drainedSessions", "failedSessions", "Drain active sessions", "Session drain",
		"drain incomplete", "force-terminates",
	} {
		if strings.Contains(l, term) {
			return true
		}
	}
	return false
}

// tableRowByPrefix returns the first line that starts with prefix, failing the
// test when the page has none.
func tableRowByPrefix(t *testing.T, page, body, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("%s: no row starting %q", page, prefix)
	return ""
}
