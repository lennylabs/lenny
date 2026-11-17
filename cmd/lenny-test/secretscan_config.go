// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// The secret scan validates .gitleaks.toml before it runs gitleaks, so a
// configuration that would skip whole files or weaken a default rule fails
// the check rather than passing a scan that read less than it claims. It
// decodes the file with github.com/pelletier/go-toml/v2 at v2.2.3, the
// TOML parser gitleaks v8.30.1 reads its configuration with through viper,
// and folds every key to lower case, as viper does, so a key in any
// spelling counts. It implements the merge gate rather than a
// specification behavior, so it carries no spec annotation.

// The keys gitleaks v8.30.1 reads at each level of its configuration,
// lower-cased. Any other key fails, so a key gitleaks would read and the
// check would not cannot slip in.
var (
	secretScanTopKeys       = []string{"title", "description", "minversion", "extend", "allowlists", "rules"}
	secretScanExtendKeys    = []string{"usedefault"}
	secretScanAllowlistKeys = []string{"description", "condition", "commits", "paths", "regextarget", "regexes", "stopwords", "targetrules"}
)

// checkSecretScanConfig refuses a gitleaks configuration that would skip
// a whole file, allow more than a value at a path for a named rule, or
// change the default rules it extends:
//
//   - gitleaks v8.30.1 applies the paths of every global allowlist (the
//     `allowlist` table and each `allowlists` entry without targetRules)
//     before it reads a file, ignoring the entry's condition and regexes.
//     An entry with targetRules joins those rules' allowlists, which
//     gitleaks evaluates per finding with the entry's condition. Each
//     `allowlists` entry must therefore name targetRules and regexes and
//     set condition = "AND", and an `allowlist` key fails.
//   - `[extend]` must set useDefault = true and nothing else, because
//     disabledRules drops default rules and path or url replaces them.
//   - A `[[rules]]` entry whose id is a default rule's id fails, because
//     gitleaks merges it into that rule and could replace its regex or
//     path.
func checkSecretScanConfig(config []byte) error {
	var doc map[string]any
	if err := toml.Unmarshal(config, &doc); err != nil {
		return fmt.Errorf("%s: %w", secretScanConfig, err)
	}
	var problems []string
	top := foldKeys(doc, "top level", secretScanTopKeys, &problems)
	if _, ok := top["allowlist"]; ok {
		problems = append(problems, "top level: an `allowlist` key is a global allowlist, which skips whole files; use an [[allowlists]] entry with targetRules")
	}
	problems = append(problems, extendProblems(top["extend"])...)
	problems = append(problems, allowlistsProblems(top["allowlists"])...)
	problems = append(problems, rulesProblems(top["rules"])...)
	if len(problems) > 0 {
		return fmt.Errorf("%s: the configuration would skip whole files, allow more than an exact value, or change a default rule:\n  %s", secretScanConfig, strings.Join(problems, "\n  "))
	}
	return nil
}

// foldKeys returns table with its keys lower-cased, and records a key that
// two spellings share and a key outside allowed. A key named "allowlist"
// is left for the caller to report.
func foldKeys(table map[string]any, where string, allowed []string, problems *[]string) map[string]any {
	folded := map[string]any{}
	var keys []string
	for k := range table {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lk := strings.ToLower(k)
		if _, dup := folded[lk]; dup {
			*problems = append(*problems, fmt.Sprintf("%s: the key %q is spelled more than one way", where, lk))
		}
		folded[lk] = table[k]
		if !contains(allowed, lk) && lk != "allowlist" {
			*problems = append(*problems, fmt.Sprintf("%s: unknown key %q", where, k))
		}
	}
	return folded
}

// extendProblems checks the `[extend]` table.
func extendProblems(v any) []string {
	table, ok := v.(map[string]any)
	if !ok {
		return []string{"[extend]: missing or not a table; the configuration must extend the default rules with useDefault = true"}
	}
	var problems []string
	extend := foldKeys(table, "[extend]", secretScanExtendKeys, &problems)
	if use, ok := extend["usedefault"].(bool); !ok || !use {
		problems = append(problems, "[extend]: useDefault is not true")
	}
	return problems
}

// allowlistsProblems checks each `allowlists` entry, whether written as an
// array of tables or inline.
func allowlistsProblems(v any) []string {
	if v == nil {
		return nil
	}
	entries, ok := v.([]any)
	if !ok {
		return []string{"allowlists: not an array of tables"}
	}
	var problems []string
	for i, raw := range entries {
		table, ok := raw.(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("allowlists entry %d: not a table", i+1))
			continue
		}
		where := fmt.Sprintf("allowlists entry %d", i+1)
		entry := foldKeys(table, where, secretScanAllowlistKeys, &problems)
		if desc, ok := entry["description"].(string); ok {
			where = fmt.Sprintf("allowlists entry %d (%s)", i+1, desc)
		}
		if _, ok := entry["allowlist"]; ok {
			problems = append(problems, where+": unknown key \"allowlist\"")
		}
		if !nonEmptyStrings(entry["targetrules"]) {
			problems = append(problems, where+": no targetRules, so gitleaks skips every file its paths match")
		}
		if !nonEmptyStrings(entry["regexes"]) {
			problems = append(problems, where+": no regexes, so it allows every finding of its rules at its paths")
		}
		if cond, _ := entry["condition"].(string); !strings.EqualFold(cond, "AND") {
			problems = append(problems, where+": condition is not \"AND\"")
		}
	}
	return problems
}

// nonEmptyStrings reports whether v is a non-empty array of non-empty
// strings.
func nonEmptyStrings(v any) bool {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return false
	}
	for _, item := range list {
		if s, ok := item.(string); !ok || s == "" {
			return false
		}
	}
	return true
}

// rulesProblems refuses a `[[rules]]` entry whose id is a default rule's.
func rulesProblems(v any) []string {
	if v == nil {
		return nil
	}
	entries, ok := v.([]any)
	if !ok {
		return []string{"rules: not an array of tables"}
	}
	var problems []string
	for i, raw := range entries {
		table, ok := raw.(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("rules entry %d: not a table", i+1))
			continue
		}
		var id string
		for k, val := range table {
			if strings.EqualFold(k, "id") {
				id, _ = val.(string)
			}
		}
		for _, d := range gitleaksDefaultRuleIDs {
			if strings.EqualFold(id, d) {
				problems = append(problems, fmt.Sprintf("rules entry %d: id %q is a default rule, which this entry would change", i+1, id))
			}
		}
	}
	return problems
}
