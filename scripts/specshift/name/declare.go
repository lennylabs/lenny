// SPDX-License-Identifier: MIT

package name

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// specPrefix is the directory the identifier space is declared under.
const specPrefix = "spec/"

// identifierExpr matches a canonical identifier, which the naming law
// writes uppercase and hyphenated under one of the class prefixes: a
// link, a channel, or a register.
var identifierExpr = regexp.MustCompile(`^(?:LNK|CH|REG)-[A-Z0-9]+(?:-[A-Z0-9]+)*$`)

// headingExpr matches a markdown heading, whose words carry the
// identifier a per-channel contract card is named for.
var headingExpr = regexp.MustCompile(`^#{1,6}[ \t]+(.*)$`)

// rowExpr matches a table row, whose cells carry the identifier a
// register row is keyed by.
var rowExpr = regexp.MustCompile(`^[ \t]*\|`)

// declaredIdentifiers indexes the identifier space the specification
// declares, which is every identifier written as a word of a heading or
// as a whole cell of a table under spec/.
//
// The index is read out of the specification rather than restated here,
// because the specification is where the identifier space is normative
// and a second list would drift from it. The two positions are where the
// space is declared: a contract card is a heading named for its
// identifier, and a register row is keyed by one.
//
// It fails on a tree with no specification file and on a specification
// that declares no identifier, rather than reporting an empty space. An
// empty space fails every entry of the register, which reads as a
// register of misspellings rather than as a tree the pass cannot run
// against yet.
func declaredIdentifiers(ctx context.Context, list scope.Lister, read scope.FileReader) (map[string]bool, error) {
	if list == nil || read == nil {
		return nil, fmt.Errorf("index the declared identifiers: a lister and a reader are required")
	}
	paths, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("index the declared identifiers: %w", err)
	}
	declared := map[string]bool{}
	files := 0
	for _, path := range paths {
		if !strings.HasPrefix(path, specPrefix) || !strings.HasSuffix(path, ".md") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("index the declared identifiers: %w", err)
		}
		data, err := read(path)
		if err != nil {
			return nil, fmt.Errorf("read specification file %s: %w", path, err)
		}
		files++
		for _, id := range declarationsIn(string(data)) {
			declared[id] = true
		}
	}
	if files == 0 {
		return nil, fmt.Errorf("index the declared identifiers: no file under %s in the tree", specPrefix)
	}
	if len(declared) == 0 {
		return nil, fmt.Errorf("index the declared identifiers: %d file(s) under %s declare none", files, specPrefix)
	}
	return declared, nil
}

// declarationsIn returns every identifier one specification file
// declares.
func declarationsIn(content string) []string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		if m := headingExpr.FindStringSubmatch(line); m != nil {
			out = append(out, identifiersAmong(strings.Fields(m[1]))...)
			continue
		}
		if rowExpr.MatchString(line) {
			out = append(out, identifiersAmong(strings.Split(line, "|"))...)
		}
	}
	return out
}

// identifiersAmong returns the candidates that are identifiers, with the
// punctuation and the code delimiters a heading or a cell writes around
// one removed.
func identifiersAmong(candidates []string) []string {
	var out []string
	for _, candidate := range candidates {
		trimmed := strings.Trim(strings.TrimSpace(candidate), "`*_,.;:()[]")
		if identifierExpr.MatchString(trimmed) {
			out = append(out, trimmed)
		}
	}
	return out
}
