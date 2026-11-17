// SPDX-License-Identifier: MIT

package annotation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/ref"
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

// Registry answers whether an identifier a `// spec:` annotation cites is
// live.
//
// It reads spec/registry.json by the schema the reference tool writes
// (ref.Registry). An identifier resolves only when it is a live page code
// or alias, requirement, operation, owned term anchor, or definition block
// anchor, or an issue an issue record names. A retired identifier does not
// resolve: the lint reports it with the successor or the removal its
// retirement records. File paths, move records, and the anchors of
// retired identifiers are never registered names. Names compare in lower
// case, because an identifier's anchor is its lower-cased form.
type Registry struct {
	names   map[string]bool
	retired map[string]ref.Retired
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
	var doc ref.Registry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse the identifier registry %s: %w", file, err)
	}
	r := &Registry{names: map[string]bool{}, retired: map[string]ref.Retired{}}
	add := func(name string) {
		if name != "" {
			r.names[strings.ToLower(name)] = true
		}
	}
	for _, p := range doc.Pages {
		add(p.Code)
		for _, a := range p.Aliases {
			add(a)
		}
	}
	for _, ids := range [][]ref.Identifier{doc.Requirements, doc.Operations} {
		for _, id := range ids {
			add(id.ID)
			add(id.Anchor)
		}
	}
	for _, t := range doc.Terms {
		add(t.Anchor)
	}
	for _, b := range doc.Blocks {
		add(b.Anchor)
	}
	for _, rt := range doc.Retired {
		r.retired[strings.ToLower(rt.ID)] = rt
		if rt.Anchor != "" {
			r.retired[strings.ToLower(rt.Anchor)] = rt
		}
	}
	if len(r.names) == 0 {
		return nil, fmt.Errorf("the identifier registry %s registers no name", file)
	}
	return r, nil
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

// Has reports whether the identifier is a live registered name.
func (r *Registry) Has(id string) bool {
	return r.names[strings.ToLower(id)]
}

// Retired returns the retirement record of an identifier the registry
// records as retired, and false for any other identifier.
func (r *Registry) Retired(id string) (ref.Retired, bool) {
	rt, ok := r.retired[strings.ToLower(id)]
	return rt, ok
}
