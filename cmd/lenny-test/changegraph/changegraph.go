// SPDX-License-Identifier: MIT

// Package changegraph holds the one statement of the change graph's
// glob-key reading and of the tracked source domain the completeness
// check governs.
//
// The values live in their own importable package because
// `cmd/lenny-test` is a main package. The completeness check that fails
// a tracked source path no glob key covers, the `--changed` selector
// that resolves a changed path against the same keys, and the tier-0
// residual scan that reports the source paths the check's narrowing
// never reaches all read this domain. A reader that restated the tree
// list, the extension list, or the key reading would drift from the
// others: a narrowing here would leave the residual scan subtracting
// paths the check no longer inspects, so a path governed by neither
// gate would go unreported, and a widening would make both gates report
// the same path.
//
// Nothing in this package carries a spec annotation. It states the
// harness's own test-selection domain, which the test harness governs rather
// than the platform specification.
package changegraph

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
)

// RelPath is the repo-relative path of the change graph.
const RelPath = "tests/change-graph.json"

// sourceTrees and sourceExts define the tracked source domain of the
// completeness check: the top-level trees it walks and the file
// extensions it reads inside them. A test file is outside the domain
// whatever its tree, because the graph selects tests rather than being
// keyed by them.
//
// The trees are the ones the graph already keys entries under, so a
// harness or script change propagates to a test selection the same way
// a package change does. Every other tree is outside the check and is
// keyed in the graph without being gated by it, which is the population
// the residual scan reports.
var (
	sourceTrees = []string{"cmd", "pkg", "scripts", "tests"}
	sourceExts  = []string{".go", ".sh"}
)

// SourceTrees returns the top-level trees the completeness check walks.
func SourceTrees() []string { return append([]string(nil), sourceTrees...) }

// SourceExts returns the file extensions the completeness check reads.
func SourceExts() []string { return append([]string(nil), sourceExts...) }

// IsSourceFile reports whether a file name is in the tracked source
// domain: a source extension, and not a test file.
func IsSourceFile(name string) bool {
	if strings.HasSuffix(name, "_test.go") {
		return false
	}
	for _, ext := range sourceExts {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// InCheckDomain reports whether the completeness check governs the
// repo-relative path, which is a source file under one of the source
// trees. The residual scan subtracts this predicate as its enumeration,
// so the two range over one definition of the boundary between them.
func InCheckDomain(p string) bool {
	if !IsSourceFile(path.Base(p)) {
		return false
	}
	for _, tree := range sourceTrees {
		if strings.HasPrefix(p, tree+"/") {
			return true
		}
	}
	return false
}

// GlobPrefix normalizes a change-graph glob key to the path prefix it
// names, stripping a trailing `/...` (Go-style recursive) or `/`
// (directory hint). It is the one reading of a key the tree has: the
// completeness check treats the result as coverage, and the `--changed`
// selector resolves a changed path against the same result. The two
// must agree, because a key accepted as coverage while the selector
// matched nothing would certify a path for which `--changed` selects no
// tiers, which is the fail-open outcome the completeness check exists to
// prevent.
func GlobPrefix(key string) string {
	key = strings.TrimSuffix(key, "/...")
	return strings.TrimSuffix(key, "/")
}

// CoveredByGlob reports whether a glob key names the path or one of its
// ancestor directories.
func CoveredByGlob(p string, keys map[string]bool) bool {
	for probe := p; probe != "." && probe != "/" && probe != ""; probe = path.Dir(probe) {
		if keys[probe] {
			return true
		}
	}
	return false
}

// ParseGlobKeys returns the graph's glob keys as path prefixes, read
// through GlobPrefix. The path names the document in the failure
// message alone.
//
// A malformed graph, or one carrying no globs block, is an error rather
// than an empty key set: a load that degraded to carrying nothing would
// report every tracked source path in the tree as uncovered.
func ParseGlobKeys(p string, data []byte) (map[string]bool, error) {
	var doc struct {
		Globs map[string]json.RawMessage `json:"globs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse change graph %s: %w", p, err)
	}
	if doc.Globs == nil {
		return nil, fmt.Errorf("change graph %s: carries no globs block", p)
	}
	keys := make(map[string]bool, len(doc.Globs))
	for key := range doc.Globs {
		keys[GlobPrefix(key)] = true
	}
	return keys, nil
}

// ReadGlobKeys reads the graph at a filesystem path and returns its glob
// keys through ParseGlobKeys.
func ReadGlobKeys(p string) (map[string]bool, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read change graph: %w", err)
	}
	return ParseGlobKeys(p, data)
}
