// SPDX-License-Identifier: MIT

// Package scope holds the one implementation of the specshift file
// domain. It carries the tracked-tree walk, the read exclusion list the
// gates and the passes share, the per-pass write exclusion list, and the
// per-file generated-artifact rule.
//
// Every pass and every gate reads the domain from here. A gate that
// re-derived the list would drift from the pass that writes the same
// files, and a file inside one domain but outside the other is exactly
// the hole the migration's completeness proof has to close.
//
// The population is the tracked tree, walked with an explicit exclusion
// list rather than an inclusion list of directories. An inclusion list
// is what leaves a carrier ungated, and an ungated carrier means the
// prohibition the citation gates end in is not flat. This is migration
// tooling rather than a platform behavior, so it carries no spec
// citation of its own.
package scope

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Pass names one specshift pass. The write domain differs between the
// name and identifier passes and the anchor and line passes, because a
// reserved phrase in a build or queue record is part of what was written
// at the time while a line citation in the same record is a pointer that
// has to keep resolving.
type Pass string

// The passes specshift carries.
const (
	// Name removes reserved bare noun phrases from prose.
	Name Pass = "name"
	// Identifier rewrites a channel identifier across code, schemas,
	// SDKs, charts, and documentation.
	Identifier Pass = "identifier"
	// Anchor rewrites a retired section anchor to its successor.
	Anchor Pass = "anchor"
	// Line rewrites or retires a line citation.
	Line Pass = "line"
)

// Passes returns every pass name in a stable order.
func Passes() []Pass { return []Pass{Name, Identifier, Anchor, Line} }

// Valid reports whether the name is one specshift carries.
func (p Pass) Valid() bool {
	for _, known := range Passes() {
		if p == known {
			return true
		}
	}
	return false
}

// readExcludedFiles are the tracked files outside the read domain of
// every pass and every gate.
//
// The audit records and the planning documents record findings as they
// were written rather than the current contract. The two citation
// registers are the baselines the citation gates consume, and a gate
// cannot read its own baseline as tree content: the baseline holds a
// copy of the text of every citation it carries, so reading it would
// report each copy under the register's own path and seeding an entry
// for that copy would not converge.
var readExcludedFiles = []string{
	"BUILD-GAPS.md",
	"TEST-GAPS.md",
	"gateway-runtime-comms.md",
	"gateway-runtime-comms-remediation.md",
	"tests/registers/line-citations.yaml",
	"tests/registers/line-citation-resolution.yaml",
}

// readExcludedPrefix is the staged-proposal tree, excluded for the same
// reason the audit records are.
const readExcludedPrefix = "proposals/"

// testdataSegment names the fixture directories excluded from every read
// and write domain. A gate's own fixture has to present the retired form
// verbatim, its route out of the population is the deletion of the case
// rather than a retirement, and a per-file count seeded for it would
// never fall.
const testdataSegment = "testdata"

// planningRecords are the tracked root-level records the reserved-phrase
// and identifier classes exclude. A reserved phrase in a build or queue
// record is part of what was written at the time and rewriting it would
// edit the record, while a line citation in the same file is a pointer
// that has to keep resolving.
var planningRecords = []string{
	"BUILD-PLAN.md",
	"BUILD-PROGRESS.md",
	"PROPOSAL-QUEUE.md",
}

// PlanningRecords returns those records, so a residual gate for the
// reserved-phrase or identifier class reads the same list the name and
// identifier passes write against instead of restating it.
func PlanningRecords() []string { return append([]string(nil), planningRecords...) }

// pathKeyedRegisters are the test-infrastructure registers keyed by file
// path, which a run that renames a file invalidates.
//
// The first two are outside every pass's site-rewrite domain, because a
// citation gate cannot read its own baseline as tree content. They
// remain subject to the key rewrite: the identifier pass rewrites the
// key of any file it renames in the same run, or the ratchet fires on a
// rename that changed no citation and every baselined non-resolving
// citation under the old path reappears as a resolver failure. The other
// two are ordinary domain members and take the key rewrite alongside
// their site rewrite.
var pathKeyedRegisters = []string{
	"tests/registers/line-citations.yaml",
	"tests/registers/line-citation-resolution.yaml",
	"tests/change-graph.json",
	"tests/spec-map.json",
}

// PathKeyedRegisters returns the registers a rename must rekey.
func PathKeyedRegisters() []string { return append([]string(nil), pathKeyedRegisters...) }

// KeyWritable reports whether the path is one of them. Membership is
// independent of the site-rewrite domain: a register outside that domain
// still takes the key rewrite.
func KeyWritable(target string) bool {
	for _, r := range pathKeyedRegisters {
		if target == r {
			return true
		}
	}
	return false
}

// Readable reports whether a tracked path is inside the read domain the
// passes and the gates share.
func Readable(p string) bool {
	if strings.HasPrefix(p, readExcludedPrefix) {
		return false
	}
	if hasSegment(p, testdataSegment) {
		return false
	}
	for _, f := range readExcludedFiles {
		if p == f {
			return false
		}
	}
	return true
}

// ReadableForClass reports whether a tracked path is inside the read
// domain of one class, which is the shared read domain less the
// root-level planning records for the reserved-phrase and identifier
// classes. The generated-artifact rule is not applied here: a generated
// artifact is inside a gate's read domain and carries a per-file count,
// and its route to zero is the regeneration of its source.
//
// The residual scan for a class ranges over this domain, so the scan,
// the exclusion, and the pass's own denylist cannot drift apart.
func ReadableForClass(p Pass, target string) (bool, error) {
	if !p.Valid() {
		return false, fmt.Errorf("class read domain: unknown pass %q", p)
	}
	if !Readable(target) {
		return false, nil
	}
	if p != Name && p != Identifier {
		return true, nil
	}
	for _, rec := range planningRecords {
		if target == rec {
			return false, nil
		}
	}
	return true, nil
}

// Writable reports whether the pass may write the tracked path. The
// site-rewrite domain is the class read domain less every file the
// per-file generated-artifact rule selects. It fails rather than
// answering when the generated-artifact rule cannot read the file,
// because a pass that wrote a file it could not classify would write a
// derived artifact.
//
// This answer governs site rewriting alone. The key rewrite over the
// path-keyed registers runs through KeyWritable, so a register excluded
// from reading is still rekeyed by the run that renames a file.
func Writable(p Pass, target string, read FileReader) (bool, error) {
	inClass, err := ReadableForClass(p, target)
	if err != nil {
		return false, err
	}
	if !inClass {
		return false, nil
	}
	disjunct, err := Generated(target, read)
	if err != nil {
		return false, fmt.Errorf("write domain for %s: %w", target, err)
	}
	return disjunct == NotGenerated, nil
}

// hasSegment reports whether a slash-separated path carries the named
// directory segment. Matching a segment rather than a substring keeps a
// file named `testdata.go` inside the domain.
func hasSegment(p, segment string) bool {
	for _, part := range strings.Split(p, "/") {
		if part == segment {
			return true
		}
	}
	return false
}

// FileReader reads a repo-relative, slash-separated tracked path.
type FileReader func(target string) ([]byte, error)

// DirReader returns a FileReader rooted at dir.
func DirReader(dir string) FileReader {
	return func(target string) ([]byte, error) {
		return os.ReadFile(filepath.Join(dir, filepath.FromSlash(target)))
	}
}

// Lister reports the tracked paths of a tree, repo-relative and
// slash-separated.
type Lister func(ctx context.Context) ([]string, error)

// GitLister lists the tracked tree with `git ls-files`. Membership comes
// from the index rather than from a filesystem walk, because an
// untracked file is outside every remedy the passes and the gates name.
func GitLister(root string) Lister {
	return func(ctx context.Context) ([]string, error) {
		cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z")
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git ls-files in %s: %w", root, err)
		}
		var paths []string
		for _, raw := range bytes.Split(out, []byte{0}) {
			if len(raw) == 0 {
				continue
			}
			paths = append(paths, string(raw))
		}
		return paths, nil
	}
}

// DirLister lists every ordinary file under dir, repo-relative to dir.
// It stands in for the git index over a fixture tree.
func DirLister(dir string) Lister {
	return func(ctx context.Context) ([]string, error) {
		var paths []string
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return fmt.Errorf("relativize %s: %w", p, err)
			}
			paths = append(paths, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", dir, err)
		}
		return paths, nil
	}
}

// ReadDomain returns the tracked paths inside the read domain, sorted.
//
// It fails on a domain that selected nothing over a tree that is not
// empty, as well as on an empty tree. A walk root pointed at an excluded
// subtree, or an exclusion list that happens to cover every path the
// walk produced, otherwise yields an empty domain that every caller
// reads as a completed inspection: a pass reports an empty diff and a
// gate reports green over content neither of them opened.
func ReadDomain(ctx context.Context, list Lister) ([]string, error) {
	all, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tracked tree: %w", err)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("list tracked tree: no tracked path found")
	}
	domain := make([]string, 0, len(all))
	for _, p := range all {
		if Readable(p) {
			domain = append(domain, p)
		}
	}
	if len(domain) == 0 {
		return nil, fmt.Errorf("read domain over %d tracked path(s): the exclusion list selected zero files", len(all))
	}
	sort.Strings(domain)
	return domain, nil
}

// ClassReadDomain returns the tracked paths inside one class's read
// domain, sorted. It is the domain the residual scan for that class
// ranges over.
func ClassReadDomain(ctx context.Context, list Lister, p Pass) ([]string, error) {
	readable, err := ReadDomain(ctx, list)
	if err != nil {
		return nil, err
	}
	domain := make([]string, 0, len(readable))
	for _, target := range readable {
		ok, err := ReadableForClass(p, target)
		if err != nil {
			return nil, err
		}
		if ok {
			domain = append(domain, target)
		}
	}
	if len(domain) == 0 {
		return nil, fmt.Errorf("%s class read domain over %d readable path(s): the exclusion list selected zero files",
			p, len(readable))
	}
	return domain, nil
}

// KeyWriteDomain returns the tracked path-keyed registers the pass
// rekeys through the key channel rather than through its site rewrite,
// sorted. A register the pass already site-rewrites is absent, because
// its key rewrite runs alongside that rewrite in one diff entry.
func KeyWriteDomain(ctx context.Context, list Lister, p Pass, read FileReader) ([]string, error) {
	all, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tracked tree: %w", err)
	}
	domain := make([]string, 0, len(pathKeyedRegisters))
	for _, target := range all {
		if !KeyWritable(target) {
			continue
		}
		site, err := Writable(p, target, read)
		if err != nil {
			return nil, err
		}
		if !site {
			domain = append(domain, target)
		}
	}
	sort.Strings(domain)
	return domain, nil
}

// WriteDomain returns the tracked paths the pass may write, sorted. It
// carries the same zero-inspection guard ReadDomain does, over its own
// filtered result: a pass whose write domain collapses to nothing aborts
// rather than reporting the empty diff of a completed migration.
func WriteDomain(ctx context.Context, list Lister, p Pass, read FileReader) ([]string, error) {
	readable, err := ReadDomain(ctx, list)
	if err != nil {
		return nil, err
	}
	domain := make([]string, 0, len(readable))
	for _, target := range readable {
		ok, err := Writable(p, target, read)
		if err != nil {
			return nil, err
		}
		if ok {
			domain = append(domain, target)
		}
	}
	if len(domain) == 0 {
		return nil, fmt.Errorf("%s pass write domain over %d readable path(s): the exclusion list selected zero files",
			p, len(readable))
	}
	return domain, nil
}

// RepoRoot returns the git top level containing dir.
func RepoRoot(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}
