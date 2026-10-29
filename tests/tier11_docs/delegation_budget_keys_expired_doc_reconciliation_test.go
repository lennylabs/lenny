// SPDX-License-Identifier: MIT

// Tier-11 reconciliation of the delegation budget key docs with §8.3,
// §16.1, §16.5, and §4.7.3. Under §8.3 only a delegate_task reservation
// detects BUDGET_KEYS_EXPIRED (a budget return that finds the sentinel
// absent is not a detection), the reservation script sets the key TTL on
// every key it creates rather than once at tree creation, and the §16.5
// alert row no longer describes the cleanup. Under §4.7.3 FINAL_USAGE_REPORT
// is a per-session report carrying session_id that the adapter sends in the
// session's Shutdown, rather than the last message before the stream closes.
//
// The tests read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: §16.1, §16.5, §8.3 (Defense-in-depth TTL), §8.3 (BUDGET_KEYS_EXPIRED
// detection and tree cleanup), §4.7.3

package tier11_docs_test

import (
	"regexp"
	"testing"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
)

// retiredBudgetDocFragment is one retired wording and the pattern that
// matches it in any of its spellings.
type retiredBudgetDocFragment struct {
	name    string
	pattern *regexp.Regexp
}

// retiredBudgetDocFragments are the wordings the §8.3, §16.1, §16.5, and
// §4.7.3 edits retired. A match in any reconciled carrier is a regression.
var retiredBudgetDocFragments = []retiredBudgetDocFragment{
	{
		name:    "a Lua script returning BUDGET_KEYS_EXPIRED as the trigger",
		pattern: regexp.MustCompile("(?i)lua script`?\\s+return(s|ed)?\\s+`?BUDGET_KEYS_EXPIRED|BUDGET_KEYS_EXPIRED`?\\s+returned\\s+by\\s+(the\\s+)?(budget\\s+)?lua\\s+script"),
	},
	{
		name:    "the cleanup sentence in the alert description",
		pattern: regexp.MustCompile(`(?i)gateway initiates tree cleanup|cascade cancel \+ root`),
	},
	{
		name:    "a budget key TTL set at tree creation",
		pattern: regexp.MustCompile(`(?i)at tree creation`),
	},
	{
		name:    "FINAL_USAGE_REPORT sent before the stream closes",
		pattern: regexp.MustCompile(`(?i)(sent|message)\s+(just\s+)?before\s+the\s+stream\s+closes|final\s+lifecycle-stream\s+message`),
	},
}

// reconciledBudgetDocCarriers are the docs and renders that carry the
// alert, metric, runbook, configuration, and adapter-contract wording.
var reconciledBudgetDocCarriers = []string{
	"docs/alerting/rules.yaml",
	"charts/lenny/files/alerting-rules.yaml",
	"pkg/embedded/manifests/manifests.yaml",
	"docs/reference/metrics.md",
	"docs/runbooks/delegation-budget-recovery.md",
	"docs/reference/configuration.md",
	"docs/reference/adapter-contract.md",
}

// requireNoRetiredBudgetFragment fails for each retired fragment body
// still carries.
func requireNoRetiredBudgetFragment(t *testing.T, label, body string) {
	t.Helper()
	for _, f := range retiredBudgetDocFragments {
		if loc := f.pattern.FindStringIndex(body); loc != nil {
			t.Errorf("%s still carries %s: %q", label, f.name, body[loc[0]:loc[1]])
		}
	}
}

// spec: §16.5, §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)
// diagnosis: the DelegationBudgetKeysExpired catalog description still
// names a Lua script return as the trigger or still describes the cleanup.
// §16.5 counts a BUDGET_KEYS_EXPIRED detection, which only a delegate_task
// reservation makes, and dropped the cleanup sentence. A failure means the
// alert that pages an operator describes a trigger the gateway no longer
// fires on, for example a budget return on a settled tree.
func TestDelegationBudgetKeysExpiredAlertNamesReservationDetection(t *testing.T) {
	var found bool
	for _, r := range rules.Catalog() {
		if r.Name != "DelegationBudgetKeysExpired" {
			continue
		}
		found = true
		requireNoRetiredBudgetFragment(t, "DelegationBudgetKeysExpired Description", r.Description)
		requireAllContain(t, "DelegationBudgetKeysExpired Description", r.Description, []string{
			"BUDGET_KEYS_EXPIRED detection",
			"delegate_task reservation",
		})
	}
	if !found {
		t.Fatal("DelegationBudgetKeysExpired is absent from the alert catalog")
	}
}

// spec: §16.1, §16.5, §8.3 (Defense-in-depth TTL), §4.7.3
// diagnosis: a reconciled doc or render still carries a retired wording: a
// Lua script return as the BUDGET_KEYS_EXPIRED trigger, the alert's cleanup
// sentence, a key TTL set at tree creation, or FINAL_USAGE_REPORT as the
// message before the stream closes. A failure names the file and the
// matched text. A render failure means `make generate` was not rerun after
// the catalog edit; fix the source and regenerate rather than editing the
// render.
func TestDelegationBudgetDocsCarryNoRetiredFragment(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range reconciledBudgetDocCarriers {
		requireNoRetiredBudgetFragment(t, rel, readRepoFile(t, root, rel))
	}
}

// spec: §16.1, §8.3 (Defense-in-depth TTL), §8.3 (Usage quiescence before
// budget return), §4.7.3
// diagnosis: a reconciled doc dropped the wording that replaced a retired
// fragment, so the retired check above passes on a page that states
// nothing. The metrics row must count a detection, the configuration rows
// must state when the TTL is set and the gateway's startup refusal on the
// seal window, and the adapter-contract row must describe the per-session
// report sent in Shutdown.
func TestDelegationBudgetDocsStateReconciledWording(t *testing.T) {
	root := repoRoot(t)
	requireAllContain(t, "docs/reference/metrics.md", readRepoFile(t, root, "docs/reference/metrics.md"), []string{
		"Increments on each `BUDGET_KEYS_EXPIRED` detection",
		"A `delegate_task` reservation detects `BUDGET_KEYS_EXPIRED`",
	})
	requireAllContain(t, "docs/runbooks/delegation-budget-recovery.md", readRepoFile(t, root, "docs/runbooks/delegation-budget-recovery.md"), []string{
		"a `delegate_task` reservation detected `BUDGET_KEYS_EXPIRED`",
		"on every budget key it creates",
		"detected at the tree's next `delegate_task` reservation",
	})
	requireAllContain(t, "docs/reference/configuration.md", readRepoFile(t, root, "docs/reference/configuration.md"), []string{
		"The budget reservation script sets it on every budget key it creates",
		"the budget return script keeps the remaining TTL of every key it writes",
		"`--delegation-usage-quiescence-timeout-seconds` (default 5 s)",
		"Pool admission does not refuse a pool on this bound.",
	})
	requireAllContain(t, "docs/reference/adapter-contract.md", readRepoFile(t, root, "docs/reference/adapter-contract.md"), []string{
		"Pushed for one session in the session's `Shutdown`",
		"(fields: `session_id`)",
		"the pod or slot release does not wait for that return",
	})
}
