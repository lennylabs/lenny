// SPDX-License-Identifier: MIT

package annotation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// RegistryPath is the repo-relative path of the committed registry of
// specification identifiers that the reference tool writes.
const RegistryPath = "spec/registry.json"

// issuesDir holds the issue records, one folder per page code.
const issuesDir = "issues/"

// ErrNoRegistry reports that the tree carries no registry yet, so an
// identifier cannot be resolved and the check that needs one is skipped.
var ErrNoRegistry = errors.New("the tree carries no " + RegistryPath)

// Registry answers whether an identifier is registered.
//
// It reads the registry without depending on its schema: every object
// key and every string value in the document is a registered name,
// compared in lower case, because an identifier's anchor is its
// lower-cased form. A page code, a requirement or operation anchor, a
// term, and a definition block's anchor therefore all resolve whether
// the registry holds them as keys or as values.
type Registry struct {
	names map[string]bool
}

// LoadRegistry reads the registry at file. A registry the tree does not
// carry returns ErrNoRegistry; one that does not parse is an error.
func LoadRegistry(read scope.FileReader, file string) (*Registry, error) {
	data, err := read(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoRegistry
	}
	if err != nil {
		return nil, fmt.Errorf("read the identifier registry %s: %w", file, err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse the identifier registry %s: %w", file, err)
	}
	r := &Registry{names: map[string]bool{}}
	r.collect(doc)
	if len(r.names) == 0 {
		return nil, fmt.Errorf("the identifier registry %s registers no name", file)
	}
	return r, nil
}

// collect adds every key and string value of a decoded JSON value.
func (r *Registry) collect(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			r.names[strings.ToLower(k)] = true
			r.collect(child)
		}
	case []any:
		for _, child := range t {
			r.collect(child)
		}
	case string:
		r.names[strings.ToLower(t)] = true
	}
}

// AddIssuePaths registers the issue identifiers the issue records name.
// An issue is recorded under issues/<CODE>/, and a record's file or
// folder name opens with its identifier, so `issues/CLAIM/CLAIM-I4.md`
// and `issues/CLAIM/claim-i4-hold-expiry.md` both register `CLAIM-I4`.
func (r *Registry) AddIssuePaths(paths []string) {
	for _, p := range paths {
		if !strings.HasPrefix(p, issuesDir) {
			continue
		}
		for _, seg := range strings.Split(strings.TrimPrefix(p, issuesDir), "/") {
			name := strings.ToLower(strings.TrimSuffix(seg, path.Ext(seg)))
			r.names[name] = true
			if m := issueID.FindString(strings.ToUpper(leadingIssue(name))); m != "" {
				r.names[strings.ToLower(m)] = true
			}
		}
	}
}

// leadingIssue returns the `code-iN` prefix of a record name, which a
// descriptive suffix may follow.
func leadingIssue(name string) string {
	parts := strings.SplitN(name, "-", 3)
	if len(parts) < 2 {
		return name
	}
	return parts[0] + "-" + parts[1]
}

// Has reports whether the identifier is registered.
func (r *Registry) Has(id string) bool {
	return r.names[strings.ToLower(id)]
}
