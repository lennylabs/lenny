// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the credentials namespace. Credential
// Secrets live in the namespace `credentials.namespace` names, a `secretRef`
// names a Secret there, and the Token Service reads no other namespace. A page
// that still sends an operator to the release namespace produces a Secret the
// Token Service never finds, or a check that passes against the wrong
// namespace, so these checks fail on the retired spellings.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.9 (Credentials namespace, Secret references), 16.1 (Token Service
// secret reloads), 17.7 (runbooks)

package tier11_docs_test

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// secretReloadsMetric is the metric whose reference row mirrors §16.1.
const secretReloadsMetric = "lenny_token_service_secret_reloads_total"

// backtickedToken matches one inline-code span.
var backtickedToken = regexp.MustCompile("`([^`]+)`")

// retiredSecretRefValue matches a `secretRef` value in the release namespace,
// in YAML and JSON spellings. It is case-sensitive, so `clientSecretRef`, whose
// connector client secrets stay outside the credentials namespace, never
// matches.
var retiredSecretRefValue = regexp.MustCompile(`secretRef"?:\s*["']?lenny-system/`)

// secretCommandWord matches the Kubernetes Secret resource on a command line.
var secretCommandWord = regexp.MustCompile(`(?i)\bsecrets?\b`)

// releaseNamespaceFlag matches a kubectl namespace flag naming the release
// namespace.
var releaseNamespaceFlag = regexp.MustCompile(`(?:^|\s)(?:-n|--namespace)[ =]lenny-system(?:\s|$)`)

// secretReloadOutcomesFromSpec returns the outcomes the §16.1 row lists.
func secretReloadOutcomesFromSpec(t *testing.T, root string) []string {
	t.Helper()
	for line := range strings.Lines(readRepoFile(t, root, "spec", "16_observability.md")) {
		if !strings.Contains(line, secretReloadsMetric) {
			continue
		}
		_, after, ok := strings.Cut(line, "by outcome:")
		if !ok {
			continue
		}
		list, _, _ := strings.Cut(after, ")")
		return backtickedValues(list)
	}
	t.Fatalf("spec/16_observability.md: no %s row listing outcomes", secretReloadsMetric)
	return nil
}

// backtickedValues returns the inline-code spans of s, sorted.
func backtickedValues(s string) []string {
	var values []string
	for _, m := range backtickedToken.FindAllStringSubmatch(s, -1) {
		values = append(values, m[1])
	}
	slices.Sort(values)
	return values
}

// metricReferenceCells returns the cells of the metric's row on the reference
// page.
func metricReferenceCells(t *testing.T, root string) []string {
	t.Helper()
	page := readRepoFile(t, root, "docs", "reference", "metrics.md")
	for line := range strings.Lines(page) {
		if strings.HasPrefix(line, "| `"+secretReloadsMetric+"` |") {
			return strings.Split(line, "|")
		}
	}
	t.Fatalf("docs/reference/metrics.md: no %s row", secretReloadsMetric)
	return nil
}

// spec: 16.1, 4.9
// diagnosis: the metrics reference row for the Token Service Secret read
//
//	counter is behind §16.1. A row that names `secret_name` or a retired
//	outcome sends an operator to a label or a value the counter never carries.
func TestSecretReloadsMetricRowMatchesTheSpecRow(t *testing.T) {
	root := repoRoot(t)
	cells := metricReferenceCells(t, root)
	if len(cells) < 5 {
		t.Fatalf("metric row has %d cells: %q", len(cells), cells)
	}
	if got := backtickedValues(cells[3]); !slices.Equal(got, []string{"outcome"}) {
		t.Errorf("labels = %q, want only outcome", got)
	}
	want := secretReloadOutcomesFromSpec(t, root)
	if len(want) == 0 {
		t.Fatal("the §16.1 row lists no outcomes")
	}
	if got := backtickedValues(cells[4]); !slices.Equal(got, want) {
		t.Errorf("outcomes = %q, want the §16.1 outcomes %q", got, want)
	}
}

// spec: 4.9
// diagnosis: a page under docs/ registers a credential in the release
//
//	namespace. The gateway refuses that reference with 400 VALIDATION_ERROR,
//	because a `secretRef` names a Secret in the credentials namespace.
func TestNoDocsPageCarriesAReleaseNamespaceSecretRef(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "docs")
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".md" {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		page := readRepoFile(t, root, rel)
		for n, line := range strings.Split(page, "\n") {
			if retiredSecretRefValue.MatchString(line) {
				t.Errorf("%s:%d: secretRef in the release namespace: %s", rel, n+1, line)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs: %v", err)
	}
}

// spec: 4.9, 17.7
// diagnosis: an operator runbook runs a Secret command against the release
//
//	namespace. The rotation writes a Secret the Token Service never reads, or
//	the RBAC check reports on a namespace the Token Service has no grant in.
func TestCredentialRunbooksRunNoSecretCommandInTheReleaseNamespace(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"credential-revocation.md", "token-service-outage.md"} {
		t.Run(name, func(t *testing.T) {
			page := readRepoFile(t, root, "docs", "runbooks", name)
			joined := strings.ReplaceAll(page, "\\\n", " ")
			for line := range strings.Lines(joined) {
				if strings.Contains(line, "kubectl") && secretCommandWord.MatchString(line) &&
					releaseNamespaceFlag.MatchString(line) {
					t.Errorf("Secret command in the release namespace: %s", strings.TrimSpace(line))
				}
			}
			if !strings.Contains(joined, "-n <credentials.namespace>") {
				t.Error("no Secret command names <credentials.namespace>")
			}
		})
	}
}
