// SPDX-License-Identifier: MIT

package lint

import (
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The pod lifecycle kinds were renamed to AgentPod, PodSet, and PodClaim,
// and the template and warm pool kinds merged into PodSet. These checks
// keep the retired names out of the tree. Records that state what was true
// when they were written keep the names they were written with: the change
// records, the staged proposals, the designs, the decision records under
// docs/adr/ (which change 0125 removes), CHANGELOG.md, the root-level audit
// and planning records, the historical migrations, and every testdata
// fixture.

// retiredNameExemptPrefixes and retiredNameExemptFiles list the records
// above, relative to the repository root.
var retiredNameExemptPrefixes = []string{
	"changes/", "proposals/", "designs/", "docs/adr/", ".claude/",
}

var retiredNameExemptFiles = map[string]bool{
	"CHANGELOG.md":                         true,
	"BUILD-GAPS.md":                        true,
	"TEST-GAPS.md":                         true,
	"PROPOSAL-QUEUE.md":                    true,
	"CODE-REVIEW.md":                       true,
	"BUILD-PLAN.md":                        true,
	"BUILD-PROGRESS.md":                    true,
	"gateway-runtime-comms.md":             true,
	"gateway-runtime-comms-remediation.md": true,
	"tests/static/lint/pod_lifecycle_kind_names_test.go": true,
	// The component test that checks the API server refuses a retired kind
	// names that kind on purpose.
	"tests/component/controllers/pod_lifecycle_kinds_test.go": true,
	// The section 28 supersession register quotes the text a staged
	// proposal wrote, which named the retired kinds.
	"tests/docs/spec_28_index_rows_test.go": true,
}

// retiredNameExempt reports whether a tracked path is a record the checks
// leave as written.
func retiredNameExempt(rel string) bool {
	if retiredNameExemptFiles[rel] || strings.Contains(rel, "/testdata/") || strings.HasPrefix(rel, "testdata/") {
		return true
	}
	for _, p := range retiredNameExemptPrefixes {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	// A migration file is the historical schema chain; its name and SQL
	// keep the table and kind names of the version that wrote it.
	return strings.HasPrefix(rel, "migrations/") && strings.HasSuffix(rel, ".sql")
}

// retiredKindIdentifier matches a Go identifier that names a retired kind
// or the retired AgentSandbox type prefix at a camel-case word boundary,
// such as SandboxClaimSpec, getSandbox, or sandboxName. Identifiers that use
// the word in the isolation sense, such as ProfileSandboxed, do not match,
// because a lower-case letter follows the word.
var retiredKindIdentifier = regexp.MustCompile(`(?:^|[a-z0-9_])(?:Sandbox(?:es|WarmPools?|Templates?|Claims?)?|AgentSandbox)(?:[A-Z0-9_]|$)|^sandbox(?:es|WarmPools?|Templates?|Claims?)?(?:[A-Z0-9_]|$)`)

// retiredKindIdentifierAllowed lists identifiers that use "Sandbox" for
// the isolation sandbox rather than a retired kind.
var retiredKindIdentifierAllowed = map[string]bool{
	"assertScheduledOnSandboxGvisorNode": true, // a gVisor sandbox node
	"TestT4SandboxKMSFailClosedTestbed":  true, // the sandboxed isolation profile
	"requireSandboxIsolationProfile":     true, // the sandboxed isolation profile
}

// maskKeptNames removes from text the names that keep the word in another
// sense: the GKE Sandbox product, the pkg/sandboxclaim package path (the
// claim binding-state machine, whose path the rename keeps), and the
// isolation-sense identifiers above.
func maskKeptNames(text string) string {
	text = strings.ReplaceAll(text, "GKE Sandbox", "")
	text = strings.ReplaceAll(text, "pkg/sandboxclaim", "")
	for id := range retiredKindIdentifierAllowed {
		text = strings.ReplaceAll(text, id, "")
	}
	return text
}

// retiredNameText matches a retired name in text: a kind name as a word
// (Sandbox, SandboxTemplate, SandboxWarmPool, SandboxClaim, and their
// plurals), the resource and singular resource names other than
// "sandboxes", and the retired webhook, metric, alert, label, readiness
// gate, field manager, and runbook names. "GKE Sandbox" is the product and
// is masked before the match.
var retiredNameText = regexp.MustCompile(strings.Join([]string{
	`(?:^|[^A-Za-z])Sandbox(?:es|WarmPools?|Templates?|Claims?)?(?:[^a-z]|$)`,
	`\bsandbox(?:warmpools?|templates?|claims?)\b`,
	`sandboxclaim-guard`, `sandboxtemplate-deletion-guard`,
	`lenny_sandboxclaim_guard_rejections_total`, `lenny_sandbox_finalizer_terminating_seconds`,
	`lenny\.dev/sandbox-ready`, `lenny-sandbox-cond-`,
	`\bsandbox-claim-race\b`, `\bsandbox-finalizer-hang\b`,
	`(?:shortNames?:\s*|^\s*-\s+|kubectl get )sbx(?:c|t|wp)?\b`,
}, "|"))

// retiredResourceInCode matches the resource name "sandboxes" in a code
// context: a CRD name, a subresource, a quoted or backticked name, or a
// kubectl argument. As a bare prose word it can name isolation sandboxes.
var retiredResourceInCode = regexp.MustCompile("sandboxes\\.lenny\\.dev|sandboxes/(?:status|finalizers)|[`\"']sandboxes[`\"']|kubectl get sandbox(?:es)?\\b|lenny\\.dev_sandboxes|^\\s*-\\s+sandboxes\\s*$|/sandboxes(?:/|\\b)|--sandboxes\\b|\\bsandboxes\\.json\\b")

// codeSpan matches a Markdown code span.
var codeSpan = regexp.MustCompile("`[^`\n]+`")

// textExtensions are the non-Go files the text check reads.
var textExtensions = map[string]bool{
	".md": true, ".yaml": true, ".yml": true, ".json": true, ".sh": true, ".tpl": true,
	".tmpl": true, ".txt": true, ".svg": true, ".proto": true, ".sql": true, ".py": true,
}

// TestRetiredPodLifecycleKindNamesAbsent asserts that no Go identifier or
// string literal, Go file name, CRD, RBAC rule, webhook, metric, alert,
// label, readiness gate, field manager, runbook, chart template, or
// document names a retired pod lifecycle kind or its resource. Go comments
// are not read; the rename tooling carries the kind names there, and the
// check holds the identifiers and the strings a reader or an API server
// meets. In Markdown, a kind name counts only inside a code span, because
// prose uses "sandbox" for the isolation sandbox. A failure names the file
// and line; replace the retired name with AgentPod, PodSet, or PodClaim and
// the matching resource.
//
// spec: KAPI, KAPOD, KPSET, KPCLAIM
func TestRetiredPodLifecycleKindNamesAbsent(t *testing.T) {
	root := schematest.RepoRoot(t)
	for _, rel := range schematest.TrackedFiles(t) {
		if retiredNameExempt(rel) {
			continue
		}
		base := filepath.Base(rel)
		if retiredNameText.MatchString(strings.ReplaceAll(base, ".", " ")) || retiredResourceInCode.MatchString(base) || retiredFileName(base) {
			t.Errorf("%s: the file name carries a retired pod lifecycle name", rel)
		}
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue // a tracked file deleted in the working tree
		}
		switch ext := filepath.Ext(rel); {
		case ext == ".go":
			for _, hit := range retiredNamesInGo(rel, src) {
				t.Error(hit)
			}
		case textExtensions[ext] || base == "Makefile":
			for _, hit := range retiredNamesInText(rel, string(src), ext == ".md") {
				t.Error(hit)
			}
		}
	}
}

// retiredFileName reports whether a file name is one of the retired Go,
// CRD, chart template, or runbook names.
func retiredFileName(base string) bool {
	for _, p := range []string{"sandbox_types.go", "sandboxclaim", "sandboxtemplate", "sandboxwarmpool", "agentsandbox", "lenny.dev_sandboxes"} {
		if strings.Contains(base, p) {
			return true
		}
	}
	return false
}

// retiredNamesInGo scans Go tokens and reports each identifier and string
// literal that names a retired kind. Comments are skipped.
func retiredNamesInGo(rel string, src []byte) []string {
	var hits []string
	fset := token.NewFileSet()
	file := fset.AddFile(rel, -1, len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, 0)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		line := fset.Position(pos).Line
		switch tok {
		case token.IDENT:
			if retiredKindIdentifier.MatchString(lit) && !retiredKindIdentifierAllowed[lit] {
				hits = append(hits, rel+":"+strconv.Itoa(line)+": identifier "+lit+" names a retired pod lifecycle kind")
			}
		case token.STRING:
			text := maskKeptNames(lit)
			if retiredNameText.MatchString(text) || retiredResourceInCode.MatchString(text) {
				hits = append(hits, rel+":"+strconv.Itoa(line)+": string "+lit+" names a retired pod lifecycle kind or resource")
			}
		}
	}
	return hits
}

// retiredNamesInText scans a non-Go file line by line. In Markdown only
// code spans and fenced code blocks are read; elsewhere a line comment
// (after #) is skipped, so a YAML or shell comment that names GKE Sandbox
// across a line break is not read.
func retiredNamesInText(rel, src string, markdown bool) []string {
	var hits []string
	inFence := false
	for i, line := range strings.Split(src, "\n") {
		text := line
		if markdown {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inFence = !inFence
				continue
			}
			if !inFence {
				text = strings.Join(codeSpan.FindAllString(line, -1), " ")
			}
		} else if j := strings.Index(text, "#"); j >= 0 && strings.HasSuffix(rel, ".yaml") {
			text = text[:j]
		}
		text = maskKeptNames(text)
		if retiredNameText.MatchString(text) || retiredResourceInCode.MatchString(text) {
			hits = append(hits, rel+":"+strconv.Itoa(i+1)+": names a retired pod lifecycle kind or resource")
		}
	}
	return hits
}

// TestRetiredPodLifecycleNameMatchers pins the matchers: each retired name
// is reported, and the isolation-sense uses of the word are not.
//
// spec: KAPI
func TestRetiredPodLifecycleNameMatchers(t *testing.T) {
	idents := map[string]bool{
		"SandboxClaimSpec": true, "getSandbox": true, "sandboxName": true, "Sandbox": true,
		"sandboxes": true, "AgentSandboxPoolManager": true, "listSandboxes": true,
		"ProfileSandboxed": false, "sandboxed": false, "sandboxcond": false,
		"sandboxclaimstate": false, "AgentPod": false, "PodSet": false,
	}
	for id, want := range idents {
		if got := retiredKindIdentifier.MatchString(id); got != want {
			t.Errorf("identifier %q: match = %v, want %v", id, got, want)
		}
	}
	texts := map[string]bool{
		"kind: SandboxTemplate":          true,
		"resources: [\"sandboxclaims\"]": true,
		"lenny-sandboxclaim-guard":       true,
		"lenny.dev/sandbox-ready":        true,
		"shortName: sbxwp":               true,
		"the sandboxed profile":          false,
		"GKE Sandbox provides gvisor":    false,
		"Alibaba OpenSandbox":            false,
		"pkg/sandbox/isolation":          false,
		"kind: PodSet":                   false,
	}
	for text, want := range texts {
		if got := retiredNameText.MatchString(strings.ReplaceAll(text, "GKE Sandbox", "")); got != want {
			t.Errorf("text %q: match = %v, want %v", text, got, want)
		}
	}
	if !retiredResourceInCode.MatchString(`resources: ["sandboxes"]`) || retiredResourceInCode.MatchString("gVisor sandboxes isolate pods") {
		t.Error("the sandboxes resource matcher must match the resource in code and not the prose word")
	}
}

// retiredTableName matches the retired pool record table name, except as
// part of a historical migration file name such as
// 0033_sandbox_warm_pools.up.sql.
var retiredTableName = regexp.MustCompile(`(?:^|[^0-9_])sandbox_warm_pools\b`)

// TestRetiredPoolTableNameAbsent asserts that no Go code or SQL outside the
// historical migration chain names the retired pool record table
// sandbox_warm_pools. Migration 0198 renames the table to pools, and every
// query and every test that runs the full chain reads pools. The Go tests
// under migrations/ and tests/component/migrations/ test the historical
// migrations by number, rolling each back to the table it altered, and are
// exempt, as are Go comments, which may state what an earlier migration
// did. A failure names the file and line; replace the name with pools.
//
// spec: POOL
func TestRetiredPoolTableNameAbsent(t *testing.T) {
	root := schematest.RepoRoot(t)
	for _, rel := range schematest.TrackedFiles(t) {
		if retiredNameExempt(rel) || strings.HasPrefix(rel, "migrations/") || strings.HasPrefix(rel, "tests/component/migrations/") {
			continue
		}
		ext := filepath.Ext(rel)
		if ext != ".go" && ext != ".sql" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		if ext == ".sql" {
			for i, line := range strings.Split(string(src), "\n") {
				if retiredTableName.MatchString(line) {
					t.Errorf("%s:%d: names the retired table sandbox_warm_pools", rel, i+1)
				}
			}
			continue
		}
		fset := token.NewFileSet()
		file := fset.AddFile(rel, -1, len(src))
		var s scanner.Scanner
		s.Init(file, src, nil, 0)
		for {
			pos, tok, lit := s.Scan()
			if tok == token.EOF {
				break
			}
			if (tok == token.STRING || tok == token.IDENT) && retiredTableName.MatchString(strings.ReplaceAll(lit, ".up.sql", " ")) && !historicalMigrationName(lit) {
				t.Errorf("%s:%d: names the retired table sandbox_warm_pools", rel, fset.Position(pos).Line)
			}
		}
	}
}

// historicalMigrationName reports whether a string literal is the file
// name of a migration that predates the rename, such as
// "0033_sandbox_warm_pools.up.sql".
func historicalMigrationName(lit string) bool {
	return regexp.MustCompile(`^"0\d{3}_[a-z0-9_]+\.(?:up|down)\.sql"$`).MatchString(lit)
}

// templateReference matches the removed PodSet template reference as a Go
// identifier, a JSON or YAML field, or a JSONPath.
var templateReference = regexp.MustCompile(`(?i)\btemplateRef\b`)

// TestPodSetTemplateReferenceAbsent asserts that no Lenny component, chart
// file, test, or document reads or writes a template reference. The
// template and the warm pool merged into one PodSet that carries the pool
// settings and the warm targets, so nothing refers to a template. A
// failure names the file and line; read the field from the PodSet itself.
//
// spec: KPSET
func TestPodSetTemplateReferenceAbsent(t *testing.T) {
	root := schematest.RepoRoot(t)
	for _, rel := range schematest.TrackedFiles(t) {
		if retiredNameExempt(rel) {
			continue
		}
		ext := filepath.Ext(rel)
		if ext != ".go" && !textExtensions[ext] {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(src), "\n") {
			if templateReference.MatchString(line) {
				t.Errorf("%s:%d: names the removed PodSet template reference", rel, i+1)
			}
		}
	}
}
