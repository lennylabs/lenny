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
	Path string
	// To is the path the file moves to, and is empty when the file stays
	// where it is. A move is part of the diff rather than a step beside
	// it, so the dry run states it, the apply performs it, and a failed
	// apply puts the file back under its old path.
	To     string
	Before []byte
	After  []byte
}

// Destination returns the path the file has after the diff is applied.
func (f FileDiff) Destination() string {
	if f.To != "" {
		return f.To
	}
	return f.Path
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
		if f.Path != o.Path || f.To != o.To {
			return false
		}
		if !bytes.Equal(f.Before, o.Before) || !bytes.Equal(f.After, o.After) {
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
//
// One site is one entry however many channels of the run reached it. A
// pass whose whole-tree preparation reports its aborts to every file the
// harness hands it would otherwise state the same site once per file in
// the write domain, and the hand-correction population the run exists to
// enumerate would be unreadable.
func Aborted(sites []*Abort) error {
	sites = distinctAborts(sites)
	switch len(sites) {
	case 0:
		return nil
	case 1:
		return sites[0]
	default:
		return &Aborts{Sites: sites}
	}
}

// distinctAborts keeps the first report of each site, in the order the
// walk reached them. Two reports of one site carry the same path, line,
// and reason, so the second states nothing the first does not.
func distinctAborts(sites []*Abort) []*Abort {
	seen := make(map[Abort]bool, len(sites))
	out := make([]*Abort, 0, len(sites))
	for _, site := range sites {
		if site == nil || seen[*site] {
			continue
		}
		seen[*site] = true
		out = append(out, site)
	}
	return out
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

// Confined is the optional interface a pass implements when a check it
// runs reads a register entry the walk never reaches. The harness hands
// the run's confinement to such a pass before the walk begins, and a
// nil confinement states that the run covers the whole write domain.
//
// The entries the walk does reach need no confinement, because the
// walk is already filtered. The claimed-entry checks read the whole
// register in the other direction, so without the confinement a run
// confined to one part of the domain would report every entry the
// complementary run owns as an entry no site claims. The identifier
// pass's file-name rename planning is in the same position: it runs
// over the whole tracked tree before the walk begins.
//
// spec: §28.1 (N3 and N4, the naming law the confined passes apply
// across the tree)
type Confined interface {
	// Confine states the confinement the run is under. The pass keeps
	// the predicate rather than the file list, so an entry whose file
	// the confinement covers is checked whether or not this run planned
	// a site for it.
	Confine(c *Confinement)
	// Deferred names the register entries the confinement put outside
	// the run, which the complementary run checks, sorted by file path
	// and, within a file, by the entry's position in that file. A
	// register is keyed by file and position when it loads, so the
	// order of the entries as they were written is not recoverable and
	// the sorted order is what a consumer of this list reads. A pass
	// that takes the predicate owes the run this list, because a
	// deferred entry is
	// checked by neither run until the complementary one lands and the
	// entry would otherwise be silently unconsumed.
	Deferred() []string
	// DeferredFiles names the distinct tracked paths those entries are
	// keyed to, in path order. The run reports the files rather than
	// re-parsing them out of the entry descriptions, because an entry
	// names its position in the file as well as the file and the two
	// registers that carry no per-occurrence key state something else
	// again.
	DeferredFiles() []string
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
//
// The run's confinement bounds this channel as it bounds the site walk: a
// register the confinement excludes belongs to the complementary run, and
// a move that would strand such a register's key fails the run rather
// than reaching outside the confinement to repair it.
type KeyRewriter interface {
	// RewriteKeys returns the register's new contents with the keys of
	// every file the run renames moved to their new paths. It carries the
	// same no-mutation obligation Rewrite does.
	RewriteKeys(ctx context.Context, path string, content []byte) ([]byte, error)
}

// Mover is the third write channel, which a pass that renames a file
// implements. The naming law reaches the name of a file as well as its
// contents, so the pass that rewrites a retired identifier inside a
// carrier is the pass that moves the carrier whose own name carries it.
//
// The move travels in the diff, so the dry run states it before it
// happens, an abort anywhere leaves every file under its old path, and
// the register key rewrite that follows the move is planned in the same
// run.
type Mover interface {
	// MoveTo returns the path the file takes after the pass, or the
	// empty string when the file stays where it is. It carries the same
	// fail-closed obligation Rewrite does: a path the pass cannot
	// resolve returns an Abort rather than a guess.
	MoveTo(ctx context.Context, path string) (string, error)
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
	// Remove deletes a repo-relative tracked path. It is used by the
	// move channel alone, so a harness driving a pass that moves no file
	// needs none.
	Remove func(path string) error
	// Confine narrows the walk to the tracked paths one run may write.
	// It is nil-able, and a nil confinement covers the whole write
	// domain, so a caller that sets none drives the pass over everything
	// the domain returned.
	Confine *Confinement
}

// NewHarness returns a harness over the tracked tree at root.
func NewHarness(root string) *Harness {
	return &Harness{
		List:   scope.GitLister(root),
		Read:   scope.DirReader(root),
		Write:  scope.DirWriter(root),
		Remove: scope.DirRemover(root),
	}
}

// NewHarnessOver returns a harness with each dependency supplied, so a
// caller can drive a pass over a tree other than a git checkout. It
// carries no remover, so a caller driving a pass that moves files sets
// Remove as well.
func NewHarnessOver(list scope.Lister, read scope.FileReader, write func(string, []byte) error) *Harness {
	return &Harness{List: list, Read: read, Write: write}
}

// Plan computes the pass's whole diff without touching the tree. It is
// the dry run.
func (h *Harness) Plan(ctx context.Context, r Rewriter) (Diff, error) {
	if h.List == nil || h.Read == nil {
		return Diff{}, fmt.Errorf("plan %s pass: harness is missing a lister or a reader", r.Pass())
	}
	// The confinement reaches the pass before the walk, because the
	// checks that read it run on the first file the walk hands the pass
	// and, for the rename planning, before that file is read.
	if confined, ok := r.(Confined); ok {
		confined.Confine(h.Confine)
	}
	domain, err := scope.WriteDomain(ctx, h.List, r.Pass(), h.Read)
	if err != nil {
		return Diff{}, fmt.Errorf("plan %s pass: %w", r.Pass(), err)
	}
	// The confinement filters the domain rather than redefining it, so
	// the exclusion lists and the generated-artifact rule still decide
	// membership and the run writes a subset of what an unconfined run
	// would. The zero-file guard is kept over the filtered result and
	// names the confinement: a confinement matching no file is an
	// operator error, and without the guard the run would report the
	// empty diff of a completed migration.
	domain = h.Confine.Filter(domain)
	if len(domain) == 0 {
		return Diff{}, fmt.Errorf("plan %s pass: the confinement %s selected zero files of the write domain", r.Pass(), h.Confine)
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
	sites, err := h.planKeyRewrites(ctx, &diff, r)
	if err != nil {
		return Diff{}, err
	}
	aborts = append(aborts, sites...)
	if err := Aborted(aborts); err != nil {
		return Diff{}, fmt.Errorf("plan %s pass: %w", r.Pass(), err)
	}
	sort.Slice(diff.Files, func(i, j int) bool { return diff.Files[i].Path < diff.Files[j].Path })
	if err := checkDestinations(ctx, h.List, diff); err != nil {
		return Diff{}, fmt.Errorf("plan %s pass: %w", r.Pass(), err)
	}
	// The confinement bounds both write channels, so a key this run may
	// not move is a key no run of this partition moves unless the
	// complementary one does. The check is the loud failure that replaces
	// the write outside the confinement a filtered walk would otherwise
	// have to make.
	if err := h.checkExcludedKeys(ctx, diff); err != nil {
		return Diff{}, fmt.Errorf("plan %s pass under the confinement %s: %w", r.Pass(), h.Confine, err)
	}
	return diff, nil
}

// planKeyRewrites plans the key rewrite over the path-keyed registers
// outside the pass's site-rewrite domain that the run's confinement
// covers, and returns the fail-closed sites the channel collected.
func (h *Harness) planKeyRewrites(ctx context.Context, diff *Diff, r Rewriter) ([]*Abort, error) {
	targets, err := h.KeyWriteTargets(ctx, r)
	if err != nil {
		return nil, err
	}
	var aborts []*Abort
	for _, target := range targets {
		if err := h.planInto(ctx, diff, r, target, false); err != nil {
			sites, ok := AllAborts(err)
			if !ok {
				return nil, err
			}
			aborts = append(aborts, sites...)
		}
	}
	return aborts, nil
}

// KeyWriteTargets returns the path-keyed registers the run rewrites
// through the key channel: the members of the pass's key-write domain the
// confinement covers. A pass that renames no file rewrites no key and has
// none.
//
// The confinement bounds this channel as it bounds the site walk. The
// gate that decides whether the channel runs and the set the channel
// writes are one set, so a run writes no path its confinement excludes.
// The stale key an excluded register is left holding is reported by the
// harness's own check rather than repaired by a write outside the
// confinement.
//
// The channel is skipped before the key-write domain is consulted when
// the confinement covers no path-keyed register at all. That run has
// nothing to rekey, and a tree carrying no register outside the
// site-rewrite domain is the complementary run's emptiness guard to
// reach; the guard names the confinement that made the run reach it.
//
// It is exported because the write-domain measurement states every path a
// run under the same confinement writes, which is the site domain
// together with these registers.
//
// spec: §28.1 (N4, the naming law: the run that moves a carrier moves
// every key written for it)
func (h *Harness) KeyWriteTargets(ctx context.Context, r Rewriter) ([]string, error) {
	if _, ok := r.(KeyRewriter); !ok {
		return nil, nil
	}
	covers, err := h.coversKeyedRegister(ctx)
	if err != nil {
		return nil, fmt.Errorf("the %s pass key-write targets: %w", r.Pass(), err)
	}
	if !covers {
		return nil, nil
	}
	keyed, err := scope.KeyWriteDomain(ctx, h.List, r.Pass(), h.Read)
	if err != nil {
		return nil, fmt.Errorf("the %s pass key-write targets under the confinement %s: %w", r.Pass(), h.Confine, err)
	}
	return h.Confine.Filter(keyed), nil
}

// coversKeyedRegister reports whether the run's confinement covers a
// path-keyed test-infrastructure register of the tracked tree. A nil
// confinement covers the whole tree and so answers yes without listing
// it.
func (h *Harness) coversKeyedRegister(ctx context.Context) (bool, error) {
	if h.Confine == nil {
		return true, nil
	}
	all, err := h.List(ctx)
	if err != nil {
		return false, fmt.Errorf("list tracked tree: %w", err)
	}
	for _, target := range all {
		if scope.KeyWritable(target) && h.Confine.Covers(target) {
			return true, nil
		}
	}
	return false, nil
}

// checkExcludedKeys refuses a plan that moves a file a path-keyed
// register outside the run's confinement names.
//
// The run may not write that register, and the complementary run does not
// perform the move, so the key would survive under a path the tree no
// longer carries with both runs exiting clean: the line-citation ratchet
// then fires on the new path as a file it has no count for, and every
// baselined non-resolving citation under the old path reappears as a
// resolver failure. The run stops with the tree unchanged, naming the
// register and the confinement, so the move is issued by a run that
// covers the keys it invalidates.
//
// The check reads every path-keyed register the confinement excludes,
// whether or not the pass site-rewrites it, because a register the
// confinement excludes takes neither channel of this run.
//
// spec: §28.1 (N4, the naming law: the run that moves a carrier moves
// every key written for it)
func (h *Harness) checkExcludedKeys(ctx context.Context, diff Diff) error {
	moved := diff.movedPaths()
	if len(moved) == 0 || h.Confine == nil {
		return nil
	}
	all, err := h.List(ctx)
	if err != nil {
		return fmt.Errorf("list tracked tree: %w", err)
	}
	for _, target := range all {
		if !scope.KeyWritable(target) || h.Confine.Covers(target) {
			continue
		}
		content, err := h.Read(target)
		if err != nil {
			return fmt.Errorf("read %s: %w", target, err)
		}
		for _, from := range moved {
			if bytes.Contains(content, []byte(from)) {
				return fmt.Errorf("%s names %s, which this run moves to %s, and the confinement excludes that register from the key rewrite: the run that covers the register owns the move",
					target, from, diff.destinationOf(from))
			}
		}
	}
	return nil
}

// movedPaths returns the paths the diff moves, in diff order.
func (d Diff) movedPaths() []string {
	var moved []string
	for _, f := range d.Files {
		if f.To != "" {
			moved = append(moved, f.Path)
		}
	}
	return moved
}

// destinationOf returns the path the diff moves one file to, so a message
// names both ends of the move.
func (d Diff) destinationOf(path string) string {
	for _, f := range d.Files {
		if f.Path == path {
			return f.Destination()
		}
	}
	return path
}

// checkDestinations refuses a plan whose moves would overwrite a file.
// A destination another file of the diff also takes, and a destination
// the tree already carries, both end with one of the two files gone and
// the run reporting a clean move.
func checkDestinations(ctx context.Context, list scope.Lister, diff Diff) error {
	moved := map[string]string{}
	for _, f := range diff.Files {
		if f.To == "" {
			continue
		}
		if other, ok := moved[f.To]; ok {
			return fmt.Errorf("%s and %s both move to %s", other, f.Path, f.To)
		}
		moved[f.To] = f.Path
	}
	if len(moved) == 0 {
		return nil
	}
	paths, err := list(ctx)
	if err != nil {
		return fmt.Errorf("list tracked tree: %w", err)
	}
	for _, p := range paths {
		if from, ok := moved[p]; ok && from != p {
			return fmt.Errorf("%s moves to %s, which the tree already carries", from, p)
		}
	}
	return nil
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
	// A file moves through the site channel alone. A path-keyed register
	// reached through the key channel is a fixed location the passes and
	// the gates address by name, so it is rekeyed where it stands.
	to := ""
	if mv, ok := r.(Mover); ok && site {
		to, err = mv.MoveTo(ctx, target)
		if err != nil {
			return fmt.Errorf("plan %s pass: %w", r.Pass(), err)
		}
		if to == target {
			to = ""
		}
	}
	if to == "" && bytes.Equal(before, after) {
		return nil
	}
	diff.Files = append(diff.Files, FileDiff{
		Path:   target,
		To:     to,
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
	if h.Remove == nil && diff.moves() {
		return Diff{}, fmt.Errorf("apply %s pass: the plan moves %d file(s) and the harness is missing a remover",
			r.Pass(), diff.moveCount())
	}
	for i, f := range diff.Files {
		if err := h.writeOne(f); err != nil {
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

// moves reports whether the diff carries a file move.
func (d Diff) moves() bool { return d.moveCount() > 0 }

// moveCount returns how many files the diff moves.
func (d Diff) moveCount() int {
	n := 0
	for _, f := range d.Files {
		if f.To != "" {
			n++
		}
	}
	return n
}

// writeOne applies one entry of the diff. A moved file is written under
// its new path and removed from its old one, in that order, so a failure
// between the two leaves the content present under both names rather
// than under neither.
func (h *Harness) writeOne(f FileDiff) error {
	if err := h.Write(f.Destination(), f.After); err != nil {
		return err
	}
	if f.To == "" {
		return nil
	}
	if err := h.Remove(f.Path); err != nil {
		return fmt.Errorf("remove %s after moving it to %s: %w", f.Path, f.To, err)
	}
	return nil
}

// restore puts back the pre-run contents of the files a failed apply had
// already written, and returns the cause. A moved file is written back
// under its old path and removed from its new one, so a rolled-back tree
// carries neither a copy under the new name nor a hole under the old.
// A restore that itself fails returns a distinct error carrying
// ErrTreeNotRestored, because an operator handling a half-written tree
// needs a different message from one handling a run that changed
// nothing.
func (h *Harness) restore(written []FileDiff, cause error) error {
	var failed []string
	for _, f := range written {
		if err := h.Write(f.Path, f.Before); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", f.Path, err))
			continue
		}
		if f.To == "" {
			continue
		}
		if err := h.Remove(f.To); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", f.To, err))
		}
	}
	if len(failed) == 0 {
		return cause
	}
	return fmt.Errorf("%w: %w; restoring %s failed", ErrTreeNotRestored, cause, strings.Join(failed, "; "))
}
