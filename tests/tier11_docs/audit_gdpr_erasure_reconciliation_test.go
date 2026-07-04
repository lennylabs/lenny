// SPDX-License-Identifier: MIT

// Tier-11 documentation / spec-consistency checks for proposal 0028
// (F-12.8.4, F-12.2.5): the §12.8 audit-log GDPR-erasure sequence is
// reconciled to the shipped chain-safe implementation. The audit_log
// table has no user_id column, the step-14 dead-letter redaction is a
// receipt-authorized content-hash discontinuity rather than a physical
// chain re-seal, step-13 performs no user-scoped mid-chain DELETE, and
// DeleteByTenant retains the gdpr.* erasure-receipt remnant.
//
// Before this reconciliation the spec and its downstream renders
// diverged from the built system in several ways that these tests pin:
//   - §12.8 step-14 and the Article 20 export read/wrote a phantom
//     audit_log.user_id column that the schema never defines.
//   - §12.8 step-14, the RedactionReceipt.new_hash field, the §18 build
//     deliverable, the §16.5 AuditChainGap/AuditRedactionReceiptMissing
//     rows, the §25 redacted_gdpr enumeration, the metrics reference, the
//     single-source alert catalog (pkg/alerting/rules/rules.go) and its
//     three generated renders, and the external-verifier comment in
//     pkg/audit/integrity/redaction.go all described a "chain rewrite
//     boundary" / (original_hash, new_hash) re-seal the verifier never
//     performs. The verifier reconciles the discontinuity through the
//     receipt's original_hash against the redacted row's preserved
//     pre-redaction hash, plus a signature check.
//   - §12.8 step-13 and the EventStore(audit) scope-table row directed a
//     user-scoped mid-chain DELETE the append-only chain cannot admit, and
//     the gdpr.* exemption paragraph said DeleteByUser filters gdpr.% rows
//     out of a deletion.
//
// Each assertion derives the reconciled substring from the applied spec /
// catalog / comment text and fails against the pre-fix wording.
//
// These tests are NOT under a build tag: they read the repository state
// directly and need no external infrastructure.

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// storageSpecPath is spec/12_storage-architecture.md.
func storageSpecPath(root string) string {
	return filepath.Join(root, "spec", "12_storage-architecture.md")
}

// erasureStep returns the numbered §12.8 deletion-sequence step line whose
// text begins `<n>. ` and, for step 14, the multi-clause body on that line.
// The §12.8 steps are single-line numbered list items, so a line prefix
// match isolates one step.
func erasureStep(body, prefix string) string {
	for _, ln := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), prefix) {
			return ln
		}
	}
	return ""
}

// scopeTableRow returns the §12.8 erasure-scope table row whose first cell
// names the given store, or "" if none matches.
func scopeTableRow(body, store string) string {
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, "|") {
			continue
		}
		cells := strings.SplitN(t, "|", 3)
		if len(cells) < 3 {
			continue
		}
		if strings.Contains(cells[1], store) {
			return ln
		}
	}
	return ""
}

// exemptionParagraph returns the §12.8 `gdpr.*` audit-event exemption
// paragraph (a single line in the source), or "" if not found.
func exemptionParagraph(body string) string {
	for _, ln := range strings.Split(body, "\n") {
		if strings.Contains(ln, "`gdpr.*` audit event exemption from user-level deletion") {
			return ln
		}
	}
	return ""
}

// spec: 12.8 (audit erasure reconciliation), 11.7 (chain integrity, redacted_gdpr).
//
// diagnosis: a failure means the §12.8 step-14 redaction scan or the
// Article 20 portable-export subsection still reads or writes a phantom
// audit_log.user_id column. The audit_log schema defines no user_id
// column, so row scoping is by payload key and the export predicate is
// payload->>'user_id'; a page that still names a user_id column tells a
// reader to query or null a column that does not exist.
func TestErasureStep14AndArticle20DropPhantomUserIDColumn(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, storageSpecPath(root))

	step14 := erasureStep(body, "14.")
	if step14 == "" {
		t.Fatal("§12.8: could not find the step-14 OCSF dead-letter redaction step")
	}
	// The reconciled step-14 must state row scoping is by payload key and
	// that there is no user_id column, and must not set a user_id column to
	// NULL. The pre-fix text read "whose `user_id` column equals the target
	// user" and wrote "the `user_id` column is set to `NULL`".
	if !strings.Contains(step14, "the `audit_log` table has no `user_id` column, so row scoping is by payload key") {
		t.Errorf("§12.8 step-14 does not state row scoping is by payload key with no user_id column; " +
			"the phantom-column read side is not reconciled")
	}
	if strings.Contains(step14, "`user_id` column is set to `NULL`") ||
		strings.Contains(step14, "the `user_id` column equals the target user") {
		t.Errorf("§12.8 step-14 still reads/writes a phantom `audit_log.user_id` column")
	}
	if !strings.Contains(step14, "no `user_id` or other user-identifying column to null out separately") {
		t.Errorf("§12.8 step-14 does not state there is no user_id column to null out; write side not reconciled")
	}

	// Article 20 actor-scoped export: prose and template query must use the
	// payload key, not a user_id column.
	if !strings.Contains(body, "`audit_log` has no `user_id` column; the acting identity is carried in the payload's `user_id` key") {
		t.Errorf("§12.8 Article 20 export prose does not state audit_log has no user_id column (payload-key actor scope)")
	}
	if !strings.Contains(body, "`payload->>'user_id'` equals the target user") {
		t.Errorf("§12.8 Article 20 export prose does not use payload->>'user_id' for the actor-scoped predicate")
	}
	if !strings.Contains(body, "WHERE tenant_id = $2 AND payload->>'user_id' = $1") {
		t.Errorf("§12.8 Article 20 template query does not use payload->>'user_id'; the phantom-column predicate survives")
	}
	if strings.Contains(body, "WHERE tenant_id = $2 AND user_id = $1") {
		t.Errorf("§12.8 Article 20 template query still filters on a phantom user_id column")
	}
}

// spec: 12.8 (receipt-authorized discontinuity), 11.7 (chain integrity, redacted_gdpr).
//
// diagnosis: a failure means §12.8 step-14 or the RedactionReceipt.new_hash
// field still describes a physical chain re-seal — recomputing the row hash
// and writing it into the row's own position and the subsequent row's
// prev_hash. The shipped redaction rewrites only the two payload columns,
// leaves prev_hash immutable (grant-forbidden to rewrite), and authorizes
// the content-hash break with the receipt's original_hash. A page that
// describes a re-seal tells a reader the chain is physically rewritten,
// which migration 0165 forbids.
func TestErasureStep14DescribesReceiptDiscontinuityNotReseal(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, storageSpecPath(root))

	step14 := erasureStep(body, "14.")
	if step14 == "" {
		t.Fatal("§12.8: could not find the step-14 OCSF dead-letter redaction step")
	}
	// Reconciled: prev_hash and identity columns are untouched, each
	// successor's prev_hash remains the preserved pre-redaction hash, and no
	// hash is written into any subsequent row's prev_hash.
	for _, want := range []string{
		"each successor row's `prev_hash` remains the redacted row's preserved pre-redaction hash",
		"no hash is written into the redacted row or into any subsequent row's `prev_hash`",
		"`prev_hash` and identity columns remain immutable to every database role",
	} {
		if !strings.Contains(step14, want) {
			t.Errorf("§12.8 step-14 does not carry the reconciled receipt-discontinuity clause %q", want)
		}
	}
	// The pre-fix re-seal wording must be gone.
	if strings.Contains(step14, "must be re-sealed") ||
		strings.Contains(step14, "re-sealed across the rewrite") ||
		strings.Contains(step14, "immediately subsequent row's `prev_hash` input") {
		t.Errorf("§12.8 step-14 still describes a physical chain re-seal")
	}

	// RedactionReceipt.new_hash: recorded for provenance, never written into
	// any row's prev_hash, reconciled through original_hash.
	newHashField := ""
	for _, ln := range strings.Split(body, "\n") {
		if strings.Contains(ln, "`new_hash` (bytea, 32 bytes)") {
			newHashField = ln
			break
		}
	}
	if newHashField == "" {
		t.Fatal("§12.8: could not find the RedactionReceipt.new_hash field line")
	}
	if !strings.Contains(newHashField, "never written into the redacted row or into any subsequent row's `prev_hash`") ||
		!strings.Contains(newHashField, "the verifier reconciles the discontinuity through `original_hash`") {
		t.Errorf("§12.8 RedactionReceipt.new_hash does not describe the receipt-discontinuity model")
	}
	if strings.Contains(newHashField, "used to re-seal the chain") ||
		strings.Contains(newHashField, "written into the subsequent row's `prev_hash`") {
		t.Errorf("§12.8 RedactionReceipt.new_hash still says the post-redaction hash re-seals the chain / is written into prev_hash")
	}
}

// spec: 12.8 (receipt-authorized discontinuity), 16.5 (alert catalog),
// 18 (build deliverable), 25.9 (external-verifier check).
//
// diagnosis: a failure means one of the parallel re-seal surfaces outside
// §12.8 step-14 still describes a "chain rewrite boundary" or an
// (original_hash, new_hash) pair the verifier never computes. The §18 build
// deliverable, the §16.5 alert rows, the §25 enumeration, the metrics row,
// the single-source alert catalog and its three generated renders, and the
// external-verifier comment must all state the original_hash-versus-
// preserved-row-hash and signature check. A stale surface would keep the
// deployed PrometheusRule CRD and reader docs describing a removed boundary.
func TestReceiptCheckSurfacesDropChainRewriteBoundary(t *testing.T) {
	root := repoRoot(t)

	// No target surface may retain the removed re-seal vocabulary.
	staleSurfaces := []struct {
		name  string
		parts []string
	}{
		{"spec/16_observability.md", []string{"spec", "16_observability.md"}},
		{"spec/18_build-sequence.md", []string{"spec", "18_build-sequence.md"}},
		{"spec/25_agent-operability.md", []string{"spec", "25_agent-operability.md"}},
		{"docs/reference/metrics.md", []string{"docs", "reference", "metrics.md"}},
		{"pkg/alerting/rules/rules.go", []string{"pkg", "alerting", "rules", "rules.go"}},
		{"pkg/audit/integrity/redaction.go", []string{"pkg", "audit", "integrity", "redaction.go"}},
		{"charts/lenny/files/alerting-rules.yaml", []string{"charts", "lenny", "files", "alerting-rules.yaml"}},
		{"docs/alerting/rules.yaml", []string{"docs", "alerting", "rules.yaml"}},
		{"pkg/embedded/manifests/manifests.yaml", []string{"pkg", "embedded", "manifests", "manifests.yaml"}},
	}
	// "chain rewrite boundary" and the "(original_hash, new_hash)" pair are
	// the removed constructs. "re-seal" is deliberately NOT forbidden as a
	// bare substring: the reconciled §18 deliverable negates it ("no hash is
	// re-sealed into any row's `prev_hash`"), so a substring ban would flag
	// the corrected sentence.
	for _, s := range staleSurfaces {
		body := readRepoFile(t, root, s.parts...)
		if strings.Contains(body, "chain rewrite boundary") {
			t.Errorf("%s still describes a 'chain rewrite boundary' the verifier never computes", s.name)
		}
		if strings.Contains(body, "(original_hash, new_hash)") {
			t.Errorf("%s still describes the removed (original_hash, new_hash) pair", s.name)
		}
	}

	// §16.5 rows: both AuditChainGap and AuditRedactionReceiptMissing must
	// state the original_hash-versus-preserved-row-hash check.
	obs := readRepoFile(t, root, "spec", "16_observability.md")
	if !strings.Contains(obs, "whose preserved pre-redaction row hash does not match the receipt's `original_hash`") {
		t.Errorf("§16.5 AuditChainGap row does not state the preserved-pre-redaction-hash vs receipt original_hash check")
	}
	if !strings.Contains(obs, "its `original_hash` does not match the redacted row's preserved pre-redaction hash") {
		t.Errorf("§16.5 AuditRedactionReceiptMissing row does not state the original_hash vs preserved-pre-redaction-hash check")
	}

	// §18 build deliverable: receipt-authorized redacted_gdpr discontinuity.
	build := readRepoFile(t, root, "spec", "18_build-sequence.md")
	if !strings.Contains(build, "produces the receipt-authorized `redacted_gdpr` chain discontinuity") {
		t.Errorf("§18 build deliverable does not describe the receipt-authorized redacted_gdpr discontinuity")
	}
	if strings.Contains(build, "re-seals the chain.") {
		t.Errorf("§18 build deliverable still says the redaction path 're-seals the chain'")
	}

	// §25 external-verifier check: match original_hash to preserved hash.
	ops := readRepoFile(t, root, "spec", "25_agent-operability.md")
	if !strings.Contains(ops, "matching its `original_hash` to the redacted row's preserved pre-redaction hash") {
		t.Errorf("§25 redacted_gdpr enumeration does not state the original_hash-to-preserved-hash external-verifier check")
	}

	// docs/reference/metrics.md row.
	metrics := readRepoFile(t, root, "docs", "reference", "metrics.md")
	if !strings.Contains(metrics, "its `original_hash` does not match the redacted row's preserved pre-redaction hash") {
		t.Errorf("docs/reference/metrics.md redaction-receipt-missing row does not state the original_hash-vs-preserved-hash check")
	}

	// Single-source alert catalog and the external-verifier comment.
	rules := readRepoFile(t, root, "pkg", "alerting", "rules", "rules.go")
	if !strings.Contains(rules, "its original_hash does not match the redacted row's preserved pre-redaction hash") {
		t.Errorf("pkg/alerting/rules/rules.go AuditRedactionReceiptMissing Description not reconciled to the original_hash check")
	}
	redaction := readRepoFile(t, root, "pkg", "audit", "integrity", "redaction.go")
	if !strings.Contains(redaction, "match of the receipt's original_hash against the redacted row's") ||
		!strings.Contains(redaction, "preserved pre-redaction hash are performed by external verifiers") {
		t.Errorf("pkg/audit/integrity/redaction.go external-verifier comment not reconciled to the original_hash match")
	}

	// The three generated renders must carry the reconciled catalog string
	// (regenerated by `make generate`), not a stale hand-edit.
	for _, parts := range [][]string{
		{"charts", "lenny", "files", "alerting-rules.yaml"},
		{"docs", "alerting", "rules.yaml"},
		{"pkg", "embedded", "manifests", "manifests.yaml"},
	} {
		render := readRepoFile(t, root, parts...)
		if !strings.Contains(render, "its original_hash does not match the redacted row's preserved pre-redaction hash") {
			t.Errorf("%s does not carry the reconciled AuditRedactionReceiptMissing description (stale render?)",
				filepath.Join(parts...))
		}
	}
}

// spec: 12.8 (step-13 no user-scoped DELETE), 11.7 (append-only chain).
//
// diagnosis: a failure means §12.8 step-13, the EventStore(audit) scope-
// table row, or the gdpr.* exemption paragraph still directs a user-scoped
// mid-chain DELETE the append-only hash chain cannot admit, or still says
// DeleteByUser filters gdpr.% rows out of a deletion. The shipped
// DeleteByUser is a spec-sanctioned no-op; a page that describes a
// user-scoped delete tells a reader the ledger erases rows it never does.
func TestErasureStep13AndScopeRowAgreeNoUserScopedDelete(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, storageSpecPath(root))

	step13 := erasureStep(body, "13.")
	if step13 == "" {
		t.Fatal("§12.8: could not find the step-13 EventStore(audit) step")
	}
	if !strings.Contains(step13, "performs no user-scoped row deletion") {
		t.Errorf("§12.8 step-13 does not state the audit EventStore performs no user-scoped row deletion")
	}
	if !strings.Contains(step13, "`DeleteByUser` on this store is a spec-sanctioned no-op") {
		t.Errorf("§12.8 step-13 does not sanction DeleteByUser as a no-op")
	}
	// The pre-fix step-13 directed a delete of "the user's sessions".
	if strings.Contains(step13, "delete audit events for the user's sessions") {
		t.Errorf("§12.8 step-13 still directs a user-scoped mid-chain DELETE")
	}

	row := scopeTableRow(body, "`EventStore` (audit)")
	if row == "" {
		t.Fatal("§12.8: could not find the EventStore(audit) erasure-scope table row")
	}
	if !strings.Contains(row, "No user-scoped row deletion") ||
		!strings.Contains(row, "`DeleteByUser` deletes nothing (see step 13)") {
		t.Errorf("§12.8 EventStore(audit) scope-table row does not agree with step-13's no-user-scoped-delete model")
	}
	if strings.Contains(row, "Audit events for the user's sessions.") {
		t.Errorf("§12.8 EventStore(audit) scope-table row still describes deleting the user's session events")
	}

	// The exemption paragraph's DeleteByUser clause must state no user-scoped
	// deletion, not that it filters gdpr.% rows out of a deletion.
	exemption := exemptionParagraph(body)
	if exemption == "" {
		t.Fatal("§12.8: could not find the gdpr.* audit-event exemption paragraph")
	}
	if !strings.Contains(exemption, "`DeleteByUser` performs no user-scoped audit deletion (see step 13)") {
		t.Errorf("§12.8 exemption paragraph does not state DeleteByUser performs no user-scoped audit deletion")
	}
	if strings.Contains(exemption, "`DeleteByUser` MUST filter out rows where `event_type LIKE 'gdpr.%'`") {
		t.Errorf("§12.8 exemption paragraph still says DeleteByUser filters gdpr.%% rows out of a deletion")
	}
}

// spec: 12.8 (retention basis and post-teardown remnant), 11.7 (append-only chain).
//
// diagnosis: a failure means the §12.8 gdpr.* exemption paragraph no longer
// carries the ordinary-row retention basis or the post-teardown-remnant
// note. Ordinary rows are retained under the tamper-evidence and legal-
// obligation basis and surfaced in the Article 20 export; the retained
// gdpr.*-only remnant after Phase 4 is exempt from chain verification and
// the continuity verifier skips deleting/deleted tenants so no false
// AuditChainGap fires. A page missing these leaves the retained-row and
// retained-remnant behavior unexplained.
func TestExemptionParagraphCarriesRetentionBasisAndRemnantNote(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, storageSpecPath(root))

	exemption := exemptionParagraph(body)
	if exemption == "" {
		t.Fatal("§12.8: could not find the gdpr.* audit-event exemption paragraph")
	}
	// Ordinary-row retention basis (C3), including the Article 20 surfacing.
	for _, want := range []string{
		"Ordinary (non-`gdpr.*`, non-dead-lettered) audit rows are likewise retained through user-level erasure",
		"held under the ledger's tamper-evidence and legal-obligation basis and surfaced in the data subject's Article 20 export",
	} {
		if !strings.Contains(exemption, want) {
			t.Errorf("§12.8 exemption paragraph is missing the ordinary-row retention basis clause %q", want)
		}
	}
	// Post-teardown-remnant note (C4): standalone compliance-receipt set,
	// exempt from chain verification, verifier skips deleting/deleted tenants.
	for _, want := range []string{
		"This remnant is a standalone compliance-receipt set",
		"it is exempt from chain verification",
		"skips any tenant whose `state` is `deleting` or `deleted`",
		"raises no `AuditChainGap` alert",
	} {
		if !strings.Contains(exemption, want) {
			t.Errorf("§12.8 exemption paragraph is missing the post-teardown-remnant clause %q", want)
		}
	}
}

// spec: 12.8 (cross-references), 11.7, 16.4, 16.5.
//
// diagnosis: a failure means a §12.8 markdown cross-reference the
// reconciliation touches (§11.7, §16.4 at #164-logging, §16.5) points at a
// heading anchor that does not resolve. A broken anchor 404s for a reader
// following the link. The Article 20 export subsection is referenced as a
// bold-prose "(see ... above)" pointer rather than a markdown link, so it
// carries no anchor to resolve; this test checks the link anchors the
// reconciled paragraphs added or rely on.
func TestReconciledCrossReferencesResolve(t *testing.T) {
	root := repoRoot(t)
	body := readDoc(t, storageSpecPath(root))

	// The §11.7 audit-logging anchor and the §12.8 compliance-interfaces
	// anchor resolve within their own files.
	polSlugs, err := headingSlugs(filepath.Join(root, "spec", "11_policy-and-controls.md"))
	if err != nil {
		t.Fatalf("read §11 heading slugs: %v", err)
	}
	if !polSlugs["117-audit-logging"] {
		t.Errorf("§12.8 links to 11_policy-and-controls.md#117-audit-logging, but no heading produces that slug")
	}

	obsSlugs, err := headingSlugs(filepath.Join(root, "spec", "16_observability.md"))
	if err != nil {
		t.Fatalf("read §16 heading slugs: %v", err)
	}
	// step-13 links the §16.4 retention sweep at #164-logging (the pre-fix
	// draft used #164-log-aggregation-and-retention, which does not resolve).
	if !obsSlugs["164-logging"] {
		t.Errorf("§12.8 step-13 links to 16_observability.md#164-logging, but no heading produces that slug")
	}
	// The exemption-paragraph remnant note links §16.5.
	if !obsSlugs["165-alerting-rules-and-slos"] {
		t.Errorf("§12.8 links to 16_observability.md#165-alerting-rules-and-slos, but no heading produces that slug")
	}

	// The §12.8 compliance-interfaces anchor the alert rows point back to.
	storeSlugs, err := headingSlugs(storageSpecPath(root))
	if err != nil {
		t.Fatalf("read §12 heading slugs: %v", err)
	}
	if !storeSlugs["128-compliance-interfaces"] {
		t.Errorf("§16.5/§25 link to 12_storage-architecture.md#128-compliance-interfaces, but no heading produces that slug")
	}

	// The step-13 replacement introduced the #164-logging anchor; guard that
	// the removed non-resolving anchor did not survive.
	if strings.Contains(body, "16_observability.md#164-log-aggregation-and-retention") {
		t.Errorf("§12.8 step-13 still links the non-resolving #164-log-aggregation-and-retention anchor")
	}
}
