// SPDX-License-Identifier: MIT

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// namingLawSection is the specification file carrying the naming law the
// tooling's cases cite, and namingLawHeading is the heading its rules sit
// under.
const (
	namingLawSection = "../../spec/28_communication-channels.md"
	namingLawHeading = "### 28.1 "
)

// namingLawCitation matches the opening line of a naming-law annotation
// and captures whatever the parenthetical names first.
var namingLawCitation = regexp.MustCompile(`// spec: §28\.1 \(([^,)]*)`)

// namingLawRule matches a rule identifier as the specification states it.
var namingLawRule = regexp.MustCompile(`\*\*(N\d+)\.\*\*`)

// namingLawRuleName matches a bare rule identifier.
var namingLawRuleName = regexp.MustCompile(`^N\d+$`)

// TestEveryNamingLawAnnotationNamesARuleTheSectionStates pins the
// annotation convention the tooling's cases follow: a case annotated with
// the naming law names the rule of that section it derives from, and the
// section states that rule. An annotation whose parenthetical states a
// rule of its own attributes to the specification something the section
// does not say, so a reader who follows the citation finds nothing that
// backs the case. A behavior the specification does not state is a
// migration-tooling convention and is described in prose in the case's
// doc comment with no spec-section annotation, the way the package's
// other tooling cases are written.
//
// spec: §28.1 (N8, the citation rule: a citation names a heading, and the
// heading it names is the one that carries the rule)
func TestEveryNamingLawAnnotationNamesARuleTheSectionStates(t *testing.T) {
	t.Parallel()

	stated, err := namingLawRules(namingLawSection)
	if err != nil {
		t.Fatalf("read the naming law from %s: %v", namingLawSection, err)
	}
	if len(stated) == 0 {
		t.Fatalf("%s states no naming-law rule, so the walk read no section", namingLawSection)
	}

	cited, err := namingLawAnnotations(".")
	if err != nil {
		t.Fatalf("read the naming-law annotations of the migration tooling: %v", err)
	}
	if len(cited) == 0 {
		t.Fatalf("the tooling carries no naming-law annotation, so the walk read no tree")
	}

	for _, annotation := range cited {
		named := strings.TrimSpace(annotation.names)
		if !namingLawRuleName.MatchString(named) {
			t.Errorf("%s:%d annotates the naming law with %q, which names no rule of the section; cite the rule the case derives from, or drop the annotation and state the behavior in prose",
				annotation.file, annotation.line, named)
			continue
		}
		if !stated[named] {
			t.Errorf("%s:%d cites naming-law rule %s, which %s does not state",
				annotation.file, annotation.line, named, namingLawSection)
		}
	}
}

// namingLawAnnotation is one naming-law citation in the tooling's sources.
type namingLawAnnotation struct {
	file  string
	line  int
	names string
}

// namingLawRules returns the rule identifiers the naming-law section
// states.
func namingLawRules(path string) (map[string]bool, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rules := map[string]bool{}
	inSection := false
	for _, current := range strings.Split(string(source), "\n") {
		if strings.HasPrefix(current, namingLawHeading) {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(current, "### ") {
			break
		}
		if !inSection {
			continue
		}
		if match := namingLawRule.FindStringSubmatch(current); match != nil {
			rules[match[1]] = true
		}
	}
	return rules, nil
}

// namingLawAnnotations returns every naming-law citation in the Go
// sources under root, test files included, since the tooling's cases
// carry most of them.
func namingLawAnnotations(root string) ([]namingLawAnnotation, error) {
	var found []namingLawAnnotation
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		source, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		for number, text := range strings.Split(string(source), "\n") {
			match := namingLawCitation.FindStringSubmatch(text)
			if match == nil {
				continue
			}
			found = append(found, namingLawAnnotation{
				file:  filepath.ToSlash(current),
				line:  number + 1,
				names: match[1],
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].line < found[j].line
	})
	return found, nil
}
