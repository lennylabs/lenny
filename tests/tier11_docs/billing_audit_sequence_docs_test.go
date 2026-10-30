// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for proposal 0025 (F-11.2.10): the reader-facing
// docs describe the real per-tenant Postgres billing and audit sequences under
// the §10.2 length-bounded safe-derived name, and the audit-chain-gap runbook
// recognizes the benign nextval-rollback branch as a distinct, no-action source
// of gap_suspected.
//
// Before this step the docs diverged from the applied spec in three ways:
//  1. docs/tutorials/multi-tenant-setup.md and docs/api/admin.md named only a
//     single billing sequence, interpolating the raw tenant_id into the object
//     name (billing_seq_tn_...), which overruns the 63-byte Postgres identifier
//     limit and omits the audit sequence the handler also provisions.
//  2. docs/runbooks/audit-chain-gap.md treated every gap_suspected as
//     outage-attributable, awaiting the §25.9 reconciliation pass and a
//     rechained_post_outage re-stamp. It did not recognize the benign nextval
//     rollback gap (no ops_postgres_outage_log window, intact prev_hash chain,
//     no reconciliation, no operator action) that Path A introduces.
//
// Each test below derives the expected substrings from the §10.2 derivation and
// the §25.9 nextval-rollback semantics, and asserts the doc carries them, so it
// fails against the pre-fix doc text and pins the corrected outcome.
//
// These tests are NOT under a build tag: they read the repository docs directly
// and need no external infrastructure.

package tier11_docs_test

import (
	"strings"
	"testing"
)

// TestTenantCreateDocsNameBothDerivedSequences pins the tenant-creation docs to
// name both the billing and audit per-tenant sequences under the §10.2 derived
// naming scheme (per-ledger prefix plus a 40-hex tenant digest), and asserts
// neither doc still interpolates a raw tenant_id into a sequence object name.
//
// diagnosis: a failure means docs/tutorials/multi-tenant-setup.md or
// docs/api/admin.md no longer describes the two per-tenant sequences the
// tenant-create handler provisions under the length-bounded safe-derived name,
// or has regressed to a raw-tenant_id sequence name (billing_seq_tn_...) that
// overruns the 63-byte Postgres identifier limit. Readers would then believe
// only a billing sequence is created, or that the raw tenant_id is safe to
// interpolate into a DDL identifier.
//
// spec: §10.2 (safe-derived sequence name), §15.1 (tenant-create provisioning)
func TestTenantCreateDocsNameBothDerivedSequences(t *testing.T) {
	root := repoRoot(t)

	type check struct {
		file      string
		parts     []string
		wantAll   []string // every substring must be present
		forbidden []string // no substring may be present
	}
	checks := []check{
		{
			file:  "multi-tenant-setup.md",
			parts: []string{"docs", "tutorials", "multi-tenant-setup.md"},
			wantAll: []string{
				"billing_seq_",
				"audit_seq_",
				"40-character hexadecimal digest",
			},
			// The raw tenant_id (tn_01J5ACME001) must no longer appear in a
			// sequence object name.
			forbidden: []string{"billing_seq_tn_"},
		},
		{
			file:  "admin.md",
			parts: []string{"docs", "api", "admin.md"},
			wantAll: []string{
				"billing_seq_",
				"audit_seq_",
			},
			forbidden: []string{"billing_seq_tn_", "billing_seq_{tenant_id}"},
		},
	}

	for _, c := range checks {
		body := readRepoFile(t, root, c.parts...)
		for _, want := range c.wantAll {
			if !strings.Contains(body, want) {
				t.Errorf("docs/%s does not mention %q; it must name both per-tenant sequences under the §10.2 derived name", c.file, want)
			}
		}
		for _, bad := range c.forbidden {
			if strings.Contains(body, bad) {
				t.Errorf("docs/%s still contains %q; the sequence name must be the derived digest, not a raw tenant_id", c.file, bad)
			}
		}
	}
}

// TestAuditChainGapRunbookDocumentsNextvalRollbackBranch pins the audit-chain-gap
// runbook to the benign nextval-rollback branch across its diagnosis,
// remediation, and verification sections. A gap_suspected with no
// ops_postgres_outage_log window and an intact prev_hash chain is a benign
// nextval rollback that requires no reconciliation and no operator action, and
// is never re-stamped rechained_post_outage. This is distinct from an outage
// gap.
//
// diagnosis: a failure means docs/runbooks/audit-chain-gap.md no longer
// distinguishes the benign nextval-rollback source of gap_suspected from an
// outage gap. An operator would then run the §25.9 reconciliation pass or
// escalate for a gap that has no deferred events to reconcile, or expect a
// rechained_post_outage re-stamp that never comes.
//
// spec: §25.9 (nextval-rollback gap_suspected), §11.7 (prev_hash tamper
// authority), §12.8 (tenant teardown), §18 (build-sequence deliverable)
func TestAuditChainGapRunbookDocumentsNextvalRollbackBranch(t *testing.T) {
	root := repoRoot(t)
	body := readRepoFile(t, root, "docs", "runbooks", "audit-chain-gap.md")

	diag := section(body, "Diagnosis")
	remed := section(body, "Remediation")
	verif := section(body, "Verification")
	if diag == "" || remed == "" || verif == "" {
		t.Fatalf("audit-chain-gap runbook is missing a required section (Diagnosis/Remediation/Verification)")
	}

	// The nextval_rollback reason string and its no-outage-window,
	// intact-prev_hash characterization must appear in each of the three
	// sections that this step reconciled.
	sections := []struct {
		name string
		body string
	}{
		{"Diagnosis", diag},
		{"Remediation", remed},
		{"Verification", verif},
	}
	for _, s := range sections {
		if !strings.Contains(s.body, "nextval") {
			t.Errorf("audit-chain-gap %s section does not mention the benign nextval rollback gap", s.name)
		}
	}

	// The remediation must explicitly state the benign case takes no
	// reconciliation and no escalation, distinct from the outage case.
	if !strings.Contains(remed, "nextval_rollback") {
		t.Errorf("audit-chain-gap Remediation does not carry the nextval_rollback reason the audit query API reports")
	}
	if !strings.Contains(remed, "No action is required") {
		t.Errorf("audit-chain-gap Remediation does not state the benign nextval rollback requires no action")
	}
	// The benign branch must be recognizable by the two signals that
	// distinguish it from an outage gap: no ops_postgres_outage_log window and
	// an intact prev_hash chain.
	for _, want := range []string{"ops_postgres_outage_log", "prev_hash"} {
		if !strings.Contains(remed, want) {
			t.Errorf("audit-chain-gap Remediation does not reference %q, the signal that distinguishes a benign rollback gap from an outage gap", want)
		}
	}
	// The benign branch is never re-stamped rechained_post_outage; the
	// verification section must say so.
	if !strings.Contains(verif, "not re-stamped") && !strings.Contains(verif, "not re-stamped `rechained_post_outage`") {
		t.Errorf("audit-chain-gap Verification does not state the benign rollback gap is not re-stamped rechained_post_outage")
	}
}
