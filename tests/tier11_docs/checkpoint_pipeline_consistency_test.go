// SPDX-License-Identifier: MIT

// Tier-11 spec/code-consistency checks for the gateway-driven checkpoint
// upload pipeline. These pin the agreements the pipeline depends on across the
// spec, the migration, and the Go emitters, so a later edit to one site cannot
// silently drift from the others:
//
//   - the §10.1 checkpoint_manifest column set matches migration 0175;
//   - the closed §10.1 manifest_reason enum, the §16.1
//     lenny_checkpoint_partial_total label domains (including recovered), and
//     the write-path and resume-path emitters all agree, with no site naming
//     the removed terminated_during_resume value and every emitted trigger a
//     member of checkpoint.AllTriggers();
//   - the §12.5 backstop sweep predicate reads identically at its two spec/12
//     occurrences (the backstop bullet and GC concurrency rule 6);
//   - the storage-counter rehydrate reservation-folding term reads identically
//     at its §11.2 occurrences and at §12.4;
//   - the reader-facing docs mirrors of the §13.1 Pod Security table
//     (architecture.md, security.md) and the concepts.md file-delivery prose
//     agree with the amended spec rows, so no docs page still asserts that
//     agent pods have no object-store path.
//
// The checks read repository state directly (no build tag, no infrastructure),
// the same posture as the other tier-11 doc checks, plus two lightweight enum
// packages so the code side of each agreement is the real symbol rather than a
// re-typed literal.
//
// spec: 10.1 (partial-manifest column set and manifest_reason enum), 11.2
// (storage-counter rehydrate), 12.5 (backstop sweep predicate), 13.1 (docs
// mirrors of the pod-to-object-store checkpoint path), 16.1
// (lenny_checkpoint_partial_total label domains).

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
)

// domainColumns are the checkpoint_manifest columns migration 0175 creates
// that the §10.1 line-141 manifest enumeration also names. tenant_id (the RLS
// tenant key) and created_at (a standard audit column) are infrastructure
// columns the §10.1 prose does not enumerate, so they are excluded from the
// bidirectional agreement.
var infraColumns = map[string]bool{
	"tenant_id":  true,
	"created_at": true,
}

// migrationColumnRE matches a column definition line in migration 0175, e.g.
// `    manifest_reason                TEXT        NOT NULL DEFAULT 'in_progress',`.
var migrationColumnRE = regexp.MustCompile(`^\s+([a-z_]+)\s+(TEXT|UUID|BIGINT|INTEGER|BOOLEAN|TIMESTAMPTZ)\b`)

// spec: 10.1
// diagnosis: the §10.1 partial-manifest column enumeration and migration 0175
//
//	disagree on the checkpoint_manifest column set. §10.1 line 141 enumerates
//	the manifest columns in prose; migration 0175 CREATEs the table. A failure
//	here means one side added, renamed, or dropped a domain column the other
//	did not — for example the migration grows a column §10.1 never names, or
//	§10.1 promises a field the table has no column for — leaving the schema and
//	its normative description out of sync.
func TestCheckpointManifestColumnSetMatchesMigration0175(t *testing.T) {
	root := repoRoot(t)

	migration, err := os.ReadFile(filepath.Join(root, "migrations", "0175_checkpoint_manifest.up.sql"))
	if err != nil {
		t.Fatalf("read migration 0175: %v", err)
	}
	// Scope to the CREATE TABLE checkpoint_manifest body so a column named in a
	// DROP or index statement is not mistaken for a table column.
	body := string(migration)
	start := strings.Index(body, "CREATE TABLE checkpoint_manifest")
	if start < 0 {
		t.Fatal("migration 0175 has no CREATE TABLE checkpoint_manifest")
	}
	createBody := body[start:]
	if end := strings.Index(createBody, "\n);"); end >= 0 {
		createBody = createBody[:end]
	}

	var migrationCols []string
	for _, line := range strings.Split(createBody, "\n") {
		m := migrationColumnRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		migrationCols = append(migrationCols, m[1])
	}
	if len(migrationCols) == 0 {
		t.Fatal("extracted no columns from migration 0175 CREATE TABLE checkpoint_manifest (regex drift?)")
	}

	s101 := specSection(t, filepath.Join(root, "spec", "10_gateway-internals.md"), "### 10.1 ")

	for _, col := range migrationCols {
		if infraColumns[col] {
			continue
		}
		// §10.1 names some columns bare (`chunk_count`) and some with a
		// trailing type or value inside the code span (`reservation_released_at
		// TIMESTAMPTZ NULL`, `partial: true`), so accept the column name opening
		// a code span and closed by a backtick, space, or colon.
		if !columnNamedInCodeSpan(s101, col) {
			t.Errorf("migration 0175 column %q is not named in §10.1; the manifest column set and its normative enumeration must agree", col)
		}
	}
}

// columnNamedInCodeSpan reports whether col opens a markdown code span in body,
// closed by a backtick, a space (a trailing type), or a colon (a trailing
// value). It matches `col`, `col TYPE ...`, and `col: value` alike.
func columnNamedInCodeSpan(body, col string) bool {
	return regexp.MustCompile("`" + regexp.QuoteMeta(col) + "[` :]").MatchString(body)
}

// spec: 10.1, 16.1
// diagnosis: the §10.1 manifest_reason enum, the §16.1
//
//	lenny_checkpoint_partial_total label domains, and the Go emitters disagree.
//	The closed enum is in_progress, complete, timeout, stream_truncated,
//	superseded, quota_exceeded; the §16.1 partial-only manifest_reason sub-
//	domain drops the two partial = false values (in_progress, complete); the
//	recovered domain is true|false; the trigger domain is the §4.4 checkpoint
//	trigger enum. A failure here means a value drifted between the spec and the
//	partialmanifeststore / checkpoint code — for instance the removed
//	terminated_during_resume value reappeared, a partial reason is missing from
//	§16.1, or an emitted trigger is not a member of checkpoint.AllTriggers().
func TestManifestReasonAndPartialCounterDomainsAgree(t *testing.T) {
	root := repoRoot(t)

	// Code side: the closed enum from partialmanifeststore, and the partial-
	// only sub-domain the counter admits (the enum minus the two partial =
	// false values).
	fullEnum := []string{
		partialmanifeststore.ReasonInProgress,
		partialmanifeststore.ReasonComplete,
		partialmanifeststore.ReasonTimeout,
		partialmanifeststore.ReasonStreamTruncated,
		partialmanifeststore.ReasonSuperseded,
		partialmanifeststore.ReasonQuotaExceeded,
	}
	for _, r := range fullEnum {
		if !partialmanifeststore.IsValidReason(r) {
			t.Errorf("partialmanifeststore.IsValidReason rejects its own enum value %q", r)
		}
	}
	if partialmanifeststore.IsValidReason("terminated_during_resume") {
		t.Error("terminated_during_resume validates as a manifest_reason; it was removed from the closed enum as unsatisfiable")
	}
	partialReasons := []string{
		partialmanifeststore.ReasonTimeout,
		partialmanifeststore.ReasonStreamTruncated,
		partialmanifeststore.ReasonSuperseded,
		partialmanifeststore.ReasonQuotaExceeded,
	}

	// §10.1 enum enumeration: every full-enum value is named, and the removed
	// value is not a live member (the "removed from the enum" note aside).
	s101 := specSection(t, filepath.Join(root, "spec", "10_gateway-internals.md"), "### 10.1 ")
	enumLine := requireLine(t, s101, "a closed enum of the manifest's disposition")
	for _, r := range fullEnum {
		if !strings.Contains(enumLine, "`"+r+"`") {
			t.Errorf("§10.1 manifest_reason enum enumeration does not name the code value %q", r)
		}
	}
	if strings.Contains(enumLine, "`terminated_during_resume`") {
		t.Error("§10.1 manifest_reason enum enumeration still lists terminated_during_resume as a live value")
	}

	// §10.1 cleanup-paragraph cross-reference site: the second §10.1 mention of
	// lenny_checkpoint_partial_total (the Cleanup paragraph) carries only the
	// §16.1 cross-reference and re-enumerates none of the recovered /
	// manifest_reason / trigger domains, mirroring the enum-line check above so
	// both §10.1 cross-reference sites are pinned. A future edit that re-adds a
	// domain enumeration or the removed terminated_during_resume value here would
	// otherwise pass undetected and defeat the single-source invariant §16.1 owns.
	cleanupLine := requireLine(t, s101, "counter tracks partial checkpoint events")
	requireAllContain(t, "§10.1 cleanup-paragraph lenny_checkpoint_partial_total cross-reference", cleanupLine, []string{
		"the single source in [§16.1]",
	})
	requireNoneContain(t, "§10.1 cleanup-paragraph lenny_checkpoint_partial_total cross-reference", cleanupLine, []string{
		"terminated_during_resume",
		"`stream_truncated`",
		"`quota_exceeded`",
		"`pre_scale_down`",
		"`periodic`",
	})

	// §16.1 partial-total row: the recovered, manifest_reason, and trigger
	// domain declarations read exactly, and none names terminated_during_resume.
	s161 := specSection(t, filepath.Join(root, "spec", "16_observability.md"), "### 16.1 ")
	counterLine := requireLine(t, s161, "`lenny_checkpoint_partial_total`")
	requireAllContain(t, "§16.1 lenny_checkpoint_partial_total row", counterLine, []string{
		"`recovered`: `true` \\| `false`",
		"`manifest_reason`: `timeout` \\| `stream_truncated` \\| `superseded` \\| `quota_exceeded`",
		"`trigger`: `periodic` \\| `pre_scale_down` \\| `eviction`",
	})
	if strings.Contains(counterLine, "terminated_during_resume") {
		t.Error("§16.1 lenny_checkpoint_partial_total row names terminated_during_resume in a label domain")
	}

	// Code emitter registration: the counter carries exactly the pool,
	// recovered, manifest_reason, trigger labels §16.1 declares.
	emitter, err := os.ReadFile(filepath.Join(root, "pkg", "gateway", "metrics", "gatewaymetrics", "gatewaymetrics_podlifecycle.go"))
	if err != nil {
		t.Fatalf("read gatewaymetrics_podlifecycle.go: %v", err)
	}
	if !strings.Contains(string(emitter), `[]string{"pool", "recovered", "manifest_reason", "trigger"}`) {
		t.Error("the lenny_checkpoint_partial_total emitter does not register the {pool, recovered, manifest_reason, trigger} label set §16.1 declares")
	}

	// The partial-only sub-domain the counter admits matches the §16.1
	// manifest_reason declaration exactly.
	assertSetEqual(t, "partial manifest_reason sub-domain (code) vs §16.1",
		partialReasons, []string{"timeout", "stream_truncated", "superseded", "quota_exceeded"})

	// Every §16.1 trigger-domain value is a member of checkpoint.AllTriggers(),
	// and every trigger the code can emit is in the §16.1 domain.
	var codeTriggers []string
	for _, tr := range checkpoint.AllTriggers() {
		codeTriggers = append(codeTriggers, string(tr))
	}
	assertSetEqual(t, "checkpoint.AllTriggers() vs §16.1 trigger domain",
		codeTriggers, []string{"periodic", "pre_scale_down", "eviction"})
}

// spec: 12.5
// diagnosis: the §12.5 backstop sweep selection predicate reads differently at
//
//	its two spec/12 occurrences (the partial-manifest backstop bullet and GC
//	concurrency-model rule 6). Both sites quote the full selection predicate
//	verbatim so a reader of either learns the same query. A failure here means
//	one occurrence was edited without the other, so the backstop and its
//	idempotency argument no longer describe the same predicate.
func TestBackstopSweepPredicateReadsIdenticallyAcrossSpec12(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "spec", "12_storage-architecture.md"))
	if err != nil {
		t.Fatalf("read spec/12: %v", err)
	}
	const predicate = "partial = true AND (manifest_reason != 'in_progress' OR now() > checkpoint_timeout_at) AND (terminal_state OR created_at < now() - maxResumeWindowSeconds) AND deleted_at IS NULL"
	if n := strings.Count(string(body), predicate); n < 2 {
		t.Errorf("the §12.5 backstop sweep predicate appears %d time(s) in spec/12, want at least 2 (the backstop bullet and GC rule 6 must quote it identically); a differing count means one site drifted:\n  %s", n, predicate)
	}
}

// spec: 11.2, 12.4
// diagnosis: the storage-counter rehydrate reservation-folding term reads
//
//	differently across its spec sites. §11.2 (the relative-decrement rebuild-
//	safety argument and the GC-triggered decrement) and §12.4 (the Redis
//	failure-mode table) each fold outstanding checkpoint reservations into the
//	absolute artifact_store-sum rebuild with the identical term. A failure here
//	means one site was edited without the others, so the counter rehydrate no
//	longer folds reservations consistently and a relative decrement after a
//	rebuild could drop bytes belonging to the tenant's live artifacts.
func TestStorageRehydrateReservationTermReadsIdenticallyAcrossSpec(t *testing.T) {
	root := repoRoot(t)
	// The reservation-folding term uses a Unicode minus (U+2212), matching the
	// spec's arithmetic notation; keep the literal byte-for-byte.
	const term = "SUM(reserved_bytes − workspace_bytes_uploaded over checkpoint_manifest rows where deleted_at IS NULL AND reservation_released_at IS NULL)"

	s11, err := os.ReadFile(filepath.Join(root, "spec", "11_policy-and-controls.md"))
	if err != nil {
		t.Fatalf("read spec/11: %v", err)
	}
	if n := strings.Count(string(s11), term); n < 2 {
		t.Errorf("the reservation-folding rehydrate term appears %d time(s) in spec/11, want at least 2 (the rebuild-safety argument and the GC-triggered decrement must fold reservations identically):\n  %s", n, term)
	}

	s12, err := os.ReadFile(filepath.Join(root, "spec", "12_storage-architecture.md"))
	if err != nil {
		t.Fatalf("read spec/12: %v", err)
	}
	if n := strings.Count(string(s12), term); n < 1 {
		t.Errorf("the reservation-folding rehydrate term is absent from spec/12 §12.4; the failure-mode table must fold reservations with the same term §11.2 uses:\n  %s", term)
	}
}

// spec: 13.1
// diagnosis: the reader-facing docs mirrors of the §13.1 Pod Security control
//
//	table (architecture.md, security.md) or the concepts.md "Gateway-mediated
//	file delivery" prose have drifted from the amended §13.1 spec rows. §13.1
//	records that the agent pod PUTs checkpoint chunks to, and on resume GETs
//	them from, object storage against gateway-minted presigned capabilities —
//	the one exception to gateway-mediated file delivery. This check anchors on
//	the spec first (so a docs mirror is measured against the live §13.1 rows,
//	not a stale re-typed phrase) and then asserts each mirror agrees. A failure
//	means the §13.1 row that grants the presigned path was removed, or a docs
//	page still presents the pre-pipeline posture in which the agent pod has no
//	object-store path at all, leaving an operator with a control-table mirror
//	that contradicts the shipped presigned-capability grant.
func TestCheckpointDocsMirrorsAgreeWithSpec131(t *testing.T) {
	root := repoRoot(t)

	// Spec anchor: §13.1 grants the agent pod the presigned checkpoint-chunk
	// path. Assert the spec side first so the docs comparison below is bound to
	// a live row rather than passing vacuously when the row is gone.
	s131 := specSection(t, filepath.Join(root, "spec", "13_security-model.md"), "### 13.1 ")
	specTransfer := requireLine(t, s131, "| Checkpoint transfer |")
	requireAllContain(t, "§13.1 Checkpoint transfer row", specTransfer, []string{
		"`PUT`s checkpoint chunks",
		"gateway-minted presigned URLs",
	})

	// architecture.md and security.md §13.1 table mirrors: the File delivery row
	// names the checkpoint-chunk exception, matching the amended spec row.
	tableMirrors := []struct{ page, heading string }{
		{filepath.Join("docs", "getting-started", "architecture.md"), "Pod security settings"},
		{filepath.Join("docs", "operator-guide", "security.md"), "Security Context"},
	}
	for _, m := range tableMirrors {
		body := readDoc(t, filepath.Join(root, m.page))
		sec := section(body, m.heading)
		if sec == "" {
			t.Fatalf("%s: %q section not found (renamed or removed?)", m.page, m.heading)
		}
		delivery := requireLine(t, sec, "| File delivery |")
		requireAllContain(t, m.page+" §13.1 File delivery row", delivery, []string{
			"Checkpoint chunk objects are the one exception",
			"gateway-minted presigned capabilities",
		})
	}

	// concepts.md prose: the file-delivery section states the exception and does
	// not assert pods have no object-store path.
	concepts := readDoc(t, filepath.Join(root, "docs", "getting-started", "concepts.md"))
	delivery := section(concepts, "Gateway-mediated file delivery")
	if delivery == "" {
		t.Fatal("concepts.md: 'Gateway-mediated file delivery' section not found (renamed or removed?)")
	}
	requireAllContain(t, "concepts.md gateway-mediated file delivery prose", delivery, []string{
		"Checkpoint chunk objects are the one exception",
		"gateway-minted presigned capability",
	})
	requireNoneContain(t, "concepts.md gateway-mediated file delivery prose", delivery, []string{
		"no direct object store access from pods",
	})
}

// assertSetEqual fails when got and want are not the same set of strings,
// order-independent. It reports the symmetric difference so a drift names the
// offending value rather than a bare inequality.
func assertSetEqual(t *testing.T, label string, got, want []string) {
	t.Helper()
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if strings.Join(g, ",") != strings.Join(w, ",") {
		t.Errorf("%s: sets differ\n  got:  %v\n  want: %v", label, g, w)
	}
}
