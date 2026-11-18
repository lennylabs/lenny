// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// noRequirementPhrase is the phrase a test's doc comment carries when the
// test verifies no requirement, which the annotation rule asks such a
// test to say in a comment (.claude/rules/spec-annotations.md, "Tests").
// The check reads the doc comment with its lines joined, so the phrase
// may wrap.
const noRequirementPhrase = "verifies no requirement"

// validateTierTestsAnnotated checks that every test function in the tiers
// validate-diagnosis covers carries a `// spec:` annotation in its doc
// comment, or says in its doc comment that it verifies no requirement.
// It replaces the spec-map check that every test file of those tiers was
// named in tests/spec-map.json (change 0116, CS-9): the annotation is now
// the record of what a test verifies. Unlike validate-diagnosis, a skip
// helper in the body does not excuse a missing annotation, because the
// coverage view and selection read the annotation whatever the test does
// at run time.
func validateTierTestsAnnotated(root string) checkResult {
	const name = "test annotations"
	var missing []string
	funcs := 0
	for _, dir := range componentAndAboveTierDirs() {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
			rel, _ := filepath.Rel(root, path)
			n, miss := unannotatedTests(rel, splitLines(string(data)))
			funcs += n
			missing = append(missing, miss...)
			return nil
		})
		if err != nil {
			return newResult(name, false, err.Error())
		}
	}
	if len(missing) > 0 {
		return newResult(name, false, fmt.Sprintf(
			"%d test function(s) carry neither a // spec: annotation nor a doc comment saying the test %s: %s",
			len(missing), noRequirementPhrase, summarizeProblems(missing),
		))
	}
	return newResult(name, true, fmt.Sprintf(
		"%d test function(s) from the component tier on; each carries a // spec: annotation or says it %s",
		funcs, noRequirementPhrase,
	))
}

// unannotatedTests returns the number of top-level test functions in a
// file and the `path:line Name` of each one whose doc comment carries
// neither a `// spec:` annotation nor the no-requirement phrase.
func unannotatedTests(rel string, lines []string) (int, []string) {
	funcs := 0
	var missing []string
	for i, line := range lines {
		if !strings.HasPrefix(line, "func Test") || strings.HasPrefix(line, "func TestMain(") {
			continue
		}
		funcs++
		if hasAnnotationBefore(lines, i, "// spec:") || docMentionsNoRequirement(lines, i) {
			continue
		}
		name := strings.TrimPrefix(line, "func ")
		if j := strings.IndexByte(name, '('); j >= 0 {
			name = name[:j]
		}
		missing = append(missing, fmt.Sprintf("%s:%d %s", rel, i+1, name))
	}
	return funcs, missing
}

// docMentionsNoRequirement reports whether the doc comment above the
// declaration at idx says the test verifies no requirement. It reads the
// same comment block hasAnnotationBefore reads, with its lines joined and
// case folded.
func docMentionsNoRequirement(lines []string, idx int) bool {
	var parts []string
	for i := idx - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			if !precedingCommentExists(lines, i) {
				break
			}
			continue
		}
		if !strings.HasPrefix(trimmed, "//") {
			break
		}
		parts = append([]string{strings.TrimSpace(strings.TrimPrefix(trimmed, "//"))}, parts...)
	}
	return strings.Contains(strings.ToLower(strings.Join(parts, " ")), noRequirementPhrase)
}
