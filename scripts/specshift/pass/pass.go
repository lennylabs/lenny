// SPDX-License-Identifier: MIT

// Package pass holds the dry-run and apply harness every specshift pass
// runs through.
//
// A pass computes its whole diff before anything is written, so the
// dry-run output and the applied change are the same object and are
// directly comparable. The dry-run diff is the entry criterion for
// applying a pass.
//
// The harness fails closed. A pass that reaches a site its register does
// not carry returns an Abort naming the file and the line, the run stops
// before its first write, and the tree is left byte-identical. Guessing
// a substitution at such a site is what silently corrupts an
// individually two-valued population.
package pass

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// FileDiff is one file's change. Before and After are the whole file
// contents, so a diff is replayable without the tree it was computed
// against.
type FileDiff struct {
	Path   string
	Before []byte
	After  []byte
}

// Diff is a pass's whole change, ordered by path.
type Diff struct {
	Files []FileDiff
}

// Paths returns the changed paths in order.
func (d Diff) Paths() []string {
	out := make([]string, 0, len(d.Files))
	for _, f := range d.Files {
		out = append(out, f.Path)
	}
	return out
}

// Equal reports whether two diffs describe the same change. The dry run
// and the apply of a pass must produce equal diffs; a caller compares
// them to detect a pass whose output depends on the tree it already
// wrote.
func (d Diff) Equal(other Diff) bool {
	if len(d.Files) != len(other.Files) {
		return false
	}
	for i, f := range d.Files {
		o := other.Files[i]
		if f.Path != o.Path || !bytes.Equal(f.Before, o.Before) || !bytes.Equal(f.After, o.After) {
			return false
		}
	}
	return true
}

// Abort is the fail-closed outcome of a pass reaching a site it cannot
// resolve. It names the file and the line so the operator can add the
// register entry the site needs.
type Abort struct {
	Path   string
	Line   int
	Reason string
}

// Error implements error.
func (a *Abort) Error() string {
	return fmt.Sprintf("%s:%d: %s", a.Path, a.Line, a.Reason)
}

// AsAbort reports whether the error chain carries an Abort, and returns
// it.
func AsAbort(err error) (*Abort, bool) {
	var a *Abort
	if errors.As(err, &a) {
		return a, true
	}
	return nil, false
}

// Rewriter is one specshift pass. Rewrite returns the file's new
// contents, or an Abort when the file carries a site the pass's register
// does not resolve. Returning the input unchanged means the pass has no
// work in that file.
type Rewriter interface {
	// Pass names which write domain the rewriter runs in.
	Pass() scope.Pass
	// LoadRegister reads and validates the register that drives the pass,
	// and fails on a missing or malformed one rather than proceeding with
	// no sites to rewrite, because a run that reported zero work would
	// read as a completed migration.
	//
	// Each pass owns this rather than a shared loader, because a driving
	// register is keyed for the rewrite it drives: a sense map is keyed by
	// file and occurrence and a citation register is keyed per file. The
	// residual registers, which record a triage decision under one entry
	// schema, are a separate family and are read by the residual gate
	// through the register package.
	LoadRegister(path string) error
	// Rewrite computes the new contents of one file.
	Rewrite(ctx context.Context, path string, content []byte) ([]byte, error)
}

// Harness walks a pass's write domain and turns it into a diff. Its
// dependencies are injected so a test drives it over a fixture tree.
type Harness struct {
	// List enumerates the tracked tree.
	List scope.Lister
	// Read reads a repo-relative tracked path.
	Read scope.FileReader
	// Write writes a repo-relative tracked path.
	Write func(path string, content []byte) error
}

// NewHarness returns a harness over the tracked tree at root.
func NewHarness(root string) *Harness {
	return &Harness{
		List:  scope.GitLister(root),
		Read:  scope.DirReader(root),
		Write: dirWriter(root),
	}
}

// NewHarnessOver returns a harness with each dependency supplied, so a
// caller can drive a pass over a tree other than a git checkout.
func NewHarnessOver(list scope.Lister, read scope.FileReader, write func(string, []byte) error) *Harness {
	return &Harness{List: list, Read: read, Write: write}
}

// dirWriter returns a writer rooted at dir that preserves each file's
// existing mode.
func dirWriter(dir string) func(string, []byte) error {
	return func(target string, content []byte) error {
		full := filepath.Join(dir, filepath.FromSlash(target))
		mode := os.FileMode(0o644)
		if info, err := os.Stat(full); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(full, content, mode); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		return nil
	}
}

// Plan computes the pass's whole diff without touching the tree. It is
// the dry run.
func (h *Harness) Plan(ctx context.Context, r Rewriter) (Diff, error) {
	if h.List == nil || h.Read == nil {
		return Diff{}, fmt.Errorf("plan %s pass: harness is missing a lister or a reader", r.Pass())
	}
	domain, err := scope.WriteDomain(ctx, h.List, r.Pass(), h.Read)
	if err != nil {
		return Diff{}, fmt.Errorf("plan %s pass: %w", r.Pass(), err)
	}
	var diff Diff
	for _, target := range domain {
		if err := ctx.Err(); err != nil {
			return Diff{}, fmt.Errorf("plan %s pass: %w", r.Pass(), err)
		}
		before, err := h.Read(target)
		if err != nil {
			return Diff{}, fmt.Errorf("plan %s pass: read %s: %w", r.Pass(), target, err)
		}
		after, err := r.Rewrite(ctx, target, before)
		if err != nil {
			return Diff{}, fmt.Errorf("plan %s pass: %w", r.Pass(), err)
		}
		if bytes.Equal(before, after) {
			continue
		}
		diff.Files = append(diff.Files, FileDiff{
			Path:   target,
			Before: append([]byte(nil), before...),
			After:  append([]byte(nil), after...),
		})
	}
	sort.Slice(diff.Files, func(i, j int) bool { return diff.Files[i].Path < diff.Files[j].Path })
	return diff, nil
}

// Apply computes the pass's whole diff and then writes it. Every file is
// planned before any file is written, so an abort anywhere in the domain
// leaves the tree byte-identical.
func (h *Harness) Apply(ctx context.Context, r Rewriter) (Diff, error) {
	diff, err := h.Plan(ctx, r)
	if err != nil {
		return Diff{}, err
	}
	if h.Write == nil {
		return Diff{}, fmt.Errorf("apply %s pass: harness is missing a writer", r.Pass())
	}
	for _, f := range diff.Files {
		if err := h.Write(f.Path, f.After); err != nil {
			return Diff{}, fmt.Errorf("apply %s pass: %w", r.Pass(), err)
		}
	}
	return diff, nil
}
