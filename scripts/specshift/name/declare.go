// SPDX-License-Identifier: MIT

package name

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// channelSectionPrefix is the specification file the identifier space is
// declared in. The registers of links, channels, and register entries
// are that file's tables, and nothing outside it declares an identifier:
// a section elsewhere in the specification cites one in a table or a
// heading, and reading a citation as a declaration would pass an entry
// whose spelling no register carries.
const channelSectionPrefix = "spec/28"

// identifierExpr matches a canonical identifier, which the naming law
// writes uppercase and hyphenated under one of the class prefixes: a
// link, a channel, or a register.
var identifierExpr = regexp.MustCompile(`^(?:LNK|CH|REG)-[A-Z0-9]+(?:-[A-Z0-9]+)*$`)

// rowExpr matches a table row, whose cells carry the identifier a
// register row is keyed by.
var rowExpr = regexp.MustCompile(`^[ \t]*\|`)

// declaredIdentifiers indexes the identifier space the specification
// declares, which is every identifier written as a whole cell of a
// register table in the communication-channels section.
//
// The index is read out of the specification rather than restated here,
// because the specification is where the identifier space is normative
// and a second list would drift from it. The registers are the
// declaration position: a contract card elsewhere in that section is
// headed by the participant edge it groups rather than by an identifier,
// and a table anywhere else in the specification cites an identifier
// rather than declaring one.
//
// It fails on a tree with no communication-channels section and on a
// section that declares no identifier, rather than reporting an empty
// space. An empty space fails every entry of the register, which reads
// as a register of misspellings rather than as a tree the pass cannot
// run against yet.
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
		if !strings.HasPrefix(path, channelSectionPrefix) || !strings.HasSuffix(path, ".md") {
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
		return nil, fmt.Errorf("index the declared identifiers: the tree carries no %s* specification file", channelSectionPrefix)
	}
	if len(declared) == 0 {
		return nil, fmt.Errorf("index the declared identifiers: %d file(s) under %s* declare none", files, channelSectionPrefix)
	}
	return declared, nil
}

// declarationsIn returns every identifier one specification file
// declares.
func declarationsIn(content string) []string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
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
