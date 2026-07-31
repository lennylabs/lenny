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
// not carry returns an Abort naming the file and the line, no file is
// written, and the tree is left byte-identical. The walk continues past
// such a site and reports every one it finds, so the operator reads the
// whole hand-correction population from one run. A write
// that fails part way through the diff is rolled back from the diff's
// own Before contents, so a failed run leaves the tree byte-identical
// whether it failed in planning or in writing. Guessing a substitution
// at an unresolved site is what silently corrupts an individually
// two-valued population.
package pass

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

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
// the first one.
func AsAbort(err error) (*Abort, bool) {
	sites, ok := AllAborts(err)
	if !ok {
		return nil, false
	}
	return sites[0], true
}

// Aborts is the fail-closed outcome of a run that reached more than one
// site it cannot resolve. A pass reports every such site of its whole
// walk in one run, because the operator hand-corrects the population
// before the pass is re-run and a report of the first site alone would
// need one run per site to enumerate it.
type Aborts struct {
	Sites []*Abort
}

// Error implements error, naming every site the run could not resolve.
func (a *Aborts) Error() string {
	lines := make([]string, 0, len(a.Sites)+1)
	lines = append(lines, fmt.Sprintf("%d site(s) need hand correction:", len(a.Sites)))
	for _, site := range a.Sites {
		lines = append(lines, "  "+site.Error())
	}
	return strings.Join(lines, "\n")
}

// Unwrap exposes the individual sites, so a caller inspecting the chain
// for an Abort finds the first of them.
func (a *Aborts) Unwrap() []error {
	out := make([]error, 0, len(a.Sites))
	for _, site := range a.Sites {
		out = append(out, site)
	}
	return out
}

// Aborted returns the fail-closed error for the sites a run could not
// resolve: nothing when there are none, the site itself when there is
// one, and the whole collection otherwise.
func Aborted(sites []*Abort) error {
	switch len(sites) {
	case 0:
		return nil
	case 1:
		return sites[0]
	default:
		return &Aborts{Sites: sites}
	}
}

// AllAborts reports whether the error chain carries one or more aborts,
// and returns them in the order the walk reached them.
func AllAborts(err error) ([]*Abort, bool) {
	var many *Aborts
	if errors.As(err, &many) {
		return many.Sites, true
	}
	var one *Abort
	if errors.As(err, &one) {
		return []*Abort{one}, true
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
	// Rewrite computes the new contents of one file. It must not mutate
	// the content it is given: the harness decides whether the file
	// changed by comparing the rewriter's result against the pre-run
	// contents, and rolls a failed apply back from those same contents, so
	// a rewriter that edited its input in place would drop the file from
	// the diff and from the rollback. The harness hands each rewriter its
	// own copy, so an in-place edit corrupts nothing beyond that pass.
	Rewrite(ctx context.Context, path string, content []byte) ([]byte, error)
}

// KeyRewriter is the second write channel, which a pass that renames a
// file implements. It rewrites the per-file keys of the path-keyed
// test-infrastructure registers a rename invalidates.
//
// The channel exists because two of those registers are outside every
// pass's site-rewrite domain, since a citation gate cannot read its own
// baseline as tree content, while a rename still has to move their keys
// in the same run. The harness plans and applies both channels together,
// so the dry-run diff still equals the applied change and an abort
// anywhere leaves the tree byte-identical.
type KeyRewriter interface {
	// RewriteKeys returns the register's new contents with the keys of
	// every file the run renames moved to their new paths. It carries the
	// same no-mutation obligation Rewrite does.
	RewriteKeys(ctx context.Context, path string, content []byte) ([]byte, error)
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
		Write: scope.DirWriter(root),
	}
}

// NewHarnessOver returns a harness with each dependency supplied, so a
// caller can drive a pass over a tree other than a git checkout.
func NewHarnessOver(list scope.Lister, read scope.FileReader, write func(string, []byte) error) *Harness {
	return &Harness{List: list, Read: read, Write: write}
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
	// A site the pass cannot resolve is collected rather than returned,
	// so one dry run names every site of the walk that needs hand
	// correction. The plan writes nothing, so the tree is byte-identical
	// whether the walk ends on the first unresolved site or on the last.
	var aborts []*Abort
	for _, target := range domain {
		if err := h.planInto(ctx, &diff, r, target, true); err != nil {
			sites, ok := AllAborts(err)
			if !ok {
				return Diff{}, err
			}
			aborts = append(aborts, sites...)
		}
	}
	// The registers outside the site-rewrite domain take the key rewrite
	// alone, in the same diff, so a run that renames a file leaves them
	// byte-identical apart from the moved key.
	if _, ok := r.(KeyRewriter); ok {
		keyed, err := scope.KeyWriteDomain(ctx, h.List, r.Pass(), h.Read)
		if err != nil {
			return Diff{}, fmt.Errorf("plan %s pass: %w", r.Pass(), err)
		}
		for _, target := range keyed {
			if err := h.planInto(ctx, &diff, r, target, false); err != nil {
				sites, ok := AllAborts(err)
				if !ok {
					return Diff{}, err
				}
				aborts = append(aborts, sites...)
			}
		}
	}
	if err := Aborted(aborts); err != nil {
		return Diff{}, fmt.Errorf("plan %s pass: %w", r.Pass(), err)
	}
	sort.Slice(diff.Files, func(i, j int) bool { return diff.Files[i].Path < diff.Files[j].Path })
	return diff, nil
}

// planInto computes one file's change and appends it to the diff when
// the file changes. site selects whether the pass's site rewrite runs;
// the key rewrite runs on every path-keyed register the pass carries one
// for, whichever channel reached the file.
func (h *Harness) planInto(ctx context.Context, diff *Diff, r Rewriter, target string, site bool) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("plan %s pass: %w", r.Pass(), err)
	}
	read, err := h.Read(target)
	if err != nil {
		return fmt.Errorf("plan %s pass: read %s: %w", r.Pass(), target, err)
	}
	// The pre-run contents are held in a buffer no rewriter has seen, so
	// the change test and the rollback compare against contents no pass
	// could have edited in place. A rewriter that mutated the buffer it
	// was handed would otherwise make before and after identical, dropping
	// a file the pass did rewrite from both the dry run and the applied
	// change, and leaving the rollback with post-rewrite contents.
	before := append([]byte(nil), read...)
	after := before
	if site {
		after, err = r.Rewrite(ctx, target, append([]byte(nil), before...))
		if err != nil {
			return fmt.Errorf("plan %s pass: %w", r.Pass(), err)
		}
	}
	if kr, ok := r.(KeyRewriter); ok && scope.KeyWritable(target) {
		after, err = kr.RewriteKeys(ctx, target, append([]byte(nil), after...))
		if err != nil {
			return fmt.Errorf("plan %s pass: rekey %s: %w", r.Pass(), target, err)
		}
	}
	if bytes.Equal(before, after) {
		return nil
	}
	diff.Files = append(diff.Files, FileDiff{
		Path:   target,
		Before: before,
		After:  append([]byte(nil), after...),
	})
	return nil
}

// ErrTreeNotRestored reports that a failed apply could not put back
// every file it had already written, so the tree is neither the pre-run
// tree nor the applied one and needs an operator before the run is
// retried.
var ErrTreeNotRestored = errors.New("the tree is not byte-identical to the pre-run tree")

// Apply computes the pass's whole diff and then writes it. Every file is
// planned before any file is written, so an abort anywhere in the domain
// leaves the tree byte-identical. A write that fails part way through the
// diff is restored from the diff's own Before contents, so the same
// guarantee holds for a failure the plan could not foresee and a
// partially rewritten tree is never left behind.
func (h *Harness) Apply(ctx context.Context, r Rewriter) (Diff, error) {
	diff, err := h.Plan(ctx, r)
	if err != nil {
		return Diff{}, err
	}
	if h.Write == nil {
		return Diff{}, fmt.Errorf("apply %s pass: harness is missing a writer", r.Pass())
	}
	for i, f := range diff.Files {
		if err := h.Write(f.Path, f.After); err != nil {
			cause := fmt.Errorf("apply %s pass: %w", r.Pass(), err)
			// The failing entry is restored along with the entries
			// already written: a writer that truncates before it fails
			// leaves that target torn, and skipping it would leave the
			// one file the run damaged unrepaired.
			return Diff{}, h.restore(diff.Files[:i+1], cause)
		}
	}
	return diff, nil
}

// restore puts back the pre-run contents of the files a failed apply had
// already written, and returns the cause. A restore that itself fails
// returns a distinct error carrying ErrTreeNotRestored, because an
// operator handling a half-written tree needs a different message from
// one handling a run that changed nothing.
func (h *Harness) restore(written []FileDiff, cause error) error {
	var failed []string
	for _, f := range written {
		if err := h.Write(f.Path, f.Before); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", f.Path, err))
		}
	}
	if len(failed) == 0 {
		return cause
	}
	return fmt.Errorf("%w: %w; restoring %s failed", ErrTreeNotRestored, cause, strings.Join(failed, "; "))
}
