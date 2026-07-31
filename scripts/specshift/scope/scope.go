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

// planningRecords are the tracked root-level records the name and
// identifier passes may read but must not write.
var planningRecords = []string{
	"BUILD-PLAN.md",
	"BUILD-PROGRESS.md",
	"PROPOSAL-QUEUE.md",
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

// Writable reports whether the pass may write the tracked path. The
// write domain is the read domain, less the root-level planning records
// for the name and identifier passes, less every file the per-file
// generated-artifact rule selects. It fails rather than answering when
// the generated-artifact rule cannot read the file, because a pass that
// wrote a file it could not classify would write a derived artifact.
func Writable(p Pass, target string, read FileReader) (bool, error) {
	if !p.Valid() {
		return false, fmt.Errorf("write domain: unknown pass %q", p)
	}
	if !Readable(target) {
		return false, nil
	}
	if p == Name || p == Identifier {
		for _, rec := range planningRecords {
			if target == rec {
				return false, nil
			}
		}
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
// It fails when the tree contributes no tracked path, because a walk
// that inspected nothing must not certify the tree.
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
	sort.Strings(domain)
	return domain, nil
}

// WriteDomain returns the tracked paths the pass may write, sorted.
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
