// SPDX-License-Identifier: MIT

// Command specshift is the committed, reproducible migration engine for
// the specification naming and citation rewrites. It carries four
// passes:
//
//   - name, which removes reserved bare noun phrases from prose;
//   - identifier, which rewrites a channel identifier across code,
//     schemas, SDKs, charts, and documentation;
//   - anchor, which rewrites a retired section anchor to its successor;
//   - line, which rewrites or retires a line citation.
//
// Each pass is driven by a register keyed per occurrence rather than by
// a global pattern, and each fails closed on a site its register does
// not carry, so a sense the enumeration missed aborts the run instead of
// being rewritten wrongly. Every pass runs through the harness in the
// pass subpackage, which computes the whole diff before it writes
// anything: the dry run and the apply produce the same diff, and an
// abort leaves the tree byte-identical.
//
// The file domain lives in the scope subpackage, which is the one
// implementation of the tracked-tree walk, the read and write exclusion
// lists, and the per-file generated-artifact rule. The tier-0 gates
// import it rather than re-deriving the domain they read.
//
// Usage:
//
//	specshift [flags]
//	  -root <path>       repo root (default: the git toplevel of the cwd)
//	  -pass <name>       name, identifier, anchor, or line
//	  -register <path>   the pass's register
//	  -only <path>       confine the run to a tracked path or directory
//	                     prefix, repeatable
//	  -except <path>     exclude a tracked path or directory prefix from
//	                     the run, repeatable
//	  -apply             write the diff (default: dry run, write nothing)
//	  -domain            print the pass's write domain and exit
//
// A run of a pass requires at least one of -only and -except, because an
// unconfined run writes every file of the pass's write domain, which is
// wider than the commit scope some invocations sit inside. Two runs whose
// confinements partition the domain cover it between them. -domain takes
// both flags as optional and prints the whole write domain when neither
// is given, so an operator measures a confinement before applying it.
//
// A run of a pass reports the confinement it ran under, the files it
// planned, and the register entries it deferred because their files lie
// outside that confinement, so an entry the complementary run still owes
// stands in the output rather than being inferred from the command line.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/anchor"
	"github.com/lennylabs/lenny/scripts/specshift/identifier"
	"github.com/lennylabs/lenny/scripts/specshift/line"
	"github.com/lennylabs/lenny/scripts/specshift/name"
	"github.com/lennylabs/lenny/scripts/specshift/pass"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "specshift: %v\n", err)
		os.Exit(1)
	}
}

// options are the parsed command line.
type options struct {
	root     string
	pass     scope.Pass
	register string
	only     []string
	except   []string
	apply    bool
	domain   bool
}

// stringList collects a repeatable string flag in the order the command
// line states its values. An empty value is refused rather than recorded,
// because a confinement value matching nothing would leave a run to fail
// on the zero-file guard with no path to name.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, " ") }

func (l *stringList) Set(value string) error {
	if value == "" {
		return errors.New("the value is empty")
	}
	*l = append(*l, value)
	return nil
}

// builtPasses returns the pass implementations over the tree at root.
// Each pass lands in the change that seeds the register driving it, so
// the table names only the passes that are built.
//
// The table is built per root rather than held as a package variable,
// because a pass reads the tree it rewrites: the line pass resolves a
// path-form citation against the sections under spec/ in the same tree,
// and the name pass holds every identifier it substitutes to the
// identifier space those files declare.
func builtPasses(root string) map[scope.Pass]pass.Rewriter {
	return map[scope.Pass]pass.Rewriter{
		scope.Name:       name.New(scope.GitLister(root), scope.DirReader(root)),
		scope.Identifier: identifier.New(scope.GitLister(root), scope.DirReader(root)),
		scope.Anchor:     anchor.New(scope.GitLister(root), scope.DirReader(root)),
		scope.Line:       line.New(scope.GitLister(root), scope.DirReader(root)),
	}
}

// rewriterFor returns the pass implementation for a name from a pass
// table. A request for a pass that is not built fails rather than
// reporting an empty diff, because a pass that reported no work would
// read as a completed migration.
func rewriterFor(passes map[scope.Pass]pass.Rewriter, name scope.Pass) (pass.Rewriter, error) {
	r, ok := passes[name]
	if !ok {
		return nil, fmt.Errorf("pass %q is not built yet", name)
	}
	return r, nil
}

// parseArgs parses the command line and resolves the repo root.
func parseArgs(ctx context.Context, args []string) (options, error) {
	fs := flag.NewFlagSet("specshift", flag.ContinueOnError)
	root := fs.String("root", "", "repo root (default: the git toplevel of the working directory)")
	name := fs.String("pass", "", "pass to run: "+passNames())
	registerPath := fs.String("register", "", "the register that drives the pass")
	var only, except stringList
	fs.Var(&only, "only", "confine the run to a tracked path or directory prefix (repeatable)")
	fs.Var(&except, "except", "exclude a tracked path or directory prefix from the run (repeatable)")
	apply := fs.Bool("apply", false, "write the diff (default: dry run, write nothing)")
	domain := fs.Bool("domain", false, "print the pass's write domain and exit")
	if err := fs.Parse(args); err != nil {
		return options{}, fmt.Errorf("parse flags: %w", err)
	}
	p := scope.Pass(*name)
	if !p.Valid() {
		return options{}, fmt.Errorf("-pass must be one of %s, got %q", passNames(), *name)
	}
	resolved := *root
	if resolved == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return options{}, fmt.Errorf("resolve working directory: %w", err)
		}
		resolved, err = scope.RepoRoot(ctx, cwd)
		if err != nil {
			return options{}, err
		}
	}
	return options{
		root:     resolved,
		pass:     p,
		register: *registerPath,
		only:     only,
		except:   except,
		apply:    *apply,
		domain:   *domain,
	}, nil
}

// passNames renders the pass names for a usage message.
func passNames() string {
	names := make([]string, 0, len(scope.Passes()))
	for _, p := range scope.Passes() {
		names = append(names, string(p))
	}
	return strings.Join(names, ", ")
}

// run parses the command line and executes one pass, or prints one
// pass's write domain.
func run(ctx context.Context, args []string, out io.Writer) error {
	return runWith(ctx, builtPasses, args, out)
}

// runWith is run over a supplied pass table, so a caller drives the
// engine with the passes it built. The table is taken as a function of
// the root, because a pass is built over the tree it rewrites and the
// root is resolved from the command line.
func runWith(ctx context.Context, passesFor func(root string) map[scope.Pass]pass.Rewriter, args []string, out io.Writer) error {
	opts, err := parseArgs(ctx, args)
	if err != nil {
		return err
	}
	passes := passesFor(opts.root)
	harness := pass.NewHarness(opts.root)
	harness.Confine = pass.NewConfinement(opts.only, opts.except)
	if opts.domain {
		// The measurement reads the pass as well as its name, because the
		// registers a renaming pass rekeys are part of what a run under the
		// same confinement writes.
		rewriter, err := rewriterFor(passes, opts.pass)
		if err != nil {
			return err
		}
		return printWriteDomain(ctx, harness, rewriter, out)
	}
	// A run of a pass states the part of the write domain it covers. An
	// unconfined run writes every file of that domain, which is wider
	// than the commit scope an invocation may sit inside, so it is a
	// usage error rather than a default.
	if len(opts.only) == 0 && len(opts.except) == 0 {
		return fmt.Errorf("-only or -except is required to run the %s pass: a run that is not confined writes every file of the pass's write domain", opts.pass)
	}
	if opts.register == "" {
		return fmt.Errorf("-register is required to run the %s pass", opts.pass)
	}
	rewriter, err := rewriterFor(passes, opts.pass)
	if err != nil {
		return err
	}
	// The pass validates the register that drives it before any file is
	// read. The schema is the pass's own, because a driving register is
	// keyed for the rewrite it drives rather than by the residual entry
	// schema the triage registers carry. A missing or malformed register
	// that loaded as an empty document would let the run report zero
	// work, which reads as a completed migration rather than as the
	// failure it is.
	if err := rewriter.LoadRegister(opts.register); err != nil {
		return err
	}
	var diff pass.Diff
	if opts.apply {
		diff, err = harness.Apply(ctx, rewriter)
	} else {
		diff, err = harness.Plan(ctx, rewriter)
	}
	if err != nil {
		return reportAbort(err)
	}
	mode := "dry run"
	if opts.apply {
		mode = "applied"
	}
	reportRun(out, opts.pass, mode, harness.Confine, rewriter, diff)
	return nil
}

// redGates is the sentence every confined run's report closes with.
// The gates it names read the whole tree, so each one stays red for as
// long as one part of the partition carries the retired population,
// however clean the run that just finished reported itself to be.
const redGates = "the naming lint, the identifier-resolution gate, the fragment-link gate, " +
	"the per-class residual scans, and tier 11 stay red until every deferred entry is " +
	"covered by a complementary run"

// reportRun writes what one run of a pass did: the confinement it ran
// under, the files it planned, and the register entries it left to the
// complementary run.
//
// The confinement is named rather than left to be inferred from the
// command line, because the run's own output is what an operator and the
// agent that issued the command read, and a confined run's clean exit
// says nothing about the entries it never looked at.
func reportRun(out io.Writer, p scope.Pass, mode string, c *pass.Confinement, r pass.Rewriter, diff pass.Diff) {
	fmt.Fprintf(out, "%s pass (%s) under %s: %d file(s) planned\n", p, mode, c, len(diff.Files))
	for _, f := range diff.Files {
		if f.To != "" {
			fmt.Fprintf(out, "  %s -> %s\n", f.Path, f.To)
			continue
		}
		fmt.Fprintf(out, "  %s\n", f.Path)
	}
	reportDeferred(out, c, r)
}

// reportDeferred names the register entries the confinement put outside
// the run, by count and by the distinct files they are keyed to, and
// states which gates stay red until a complementary run covers them.
//
// For the name and the identifier passes the entries are the ones the
// claimed-entry check skipped, and for the line and the anchor passes,
// which carry no such check, they are the ones nothing checks in either
// direction. The report is the only signal in the second case, and the
// only signal of an under-consumed register in the first.
//
// The standing sentence closes every confined run's report, including a
// run that deferred nothing. The gates it names read the whole tree
// rather than one run's register slice, so they stay red for as long as
// the partition is half-migrated whatever this run happened to defer.
func reportDeferred(out io.Writer, c *pass.Confinement, r pass.Rewriter) {
	confined, ok := r.(pass.Confined)
	if !ok {
		return
	}
	entries, files := confined.Deferred(), confined.DeferredFiles()
	fmt.Fprintf(out, "%d register entr(ies) deferred as outside %s, in %d file(s)\n", len(entries), c, len(files))
	for _, target := range files {
		fmt.Fprintf(out, "  %s\n", target)
	}
	fmt.Fprintln(out, redGates)
}

// reportAbort turns a fail-closed abort into an operator-facing message
// naming every file and line the run could not resolve, and passes any
// other error through. Every site is named because the operator
// hand-corrects the population before the pass is re-run. A run that
// failed while writing and could not put back what it had written is
// reported as a tree that needs an operator, because the unchanged-tree
// message would be false for it.
func reportAbort(err error) error {
	if errors.Is(err, pass.ErrTreeNotRestored) {
		return fmt.Errorf("the tree is not clean and needs an operator: %w", err)
	}
	sites, ok := pass.AllAborts(err)
	if !ok {
		return err
	}
	lines := make([]string, 0, len(sites))
	for _, site := range sites {
		lines = append(lines, site.Error())
	}
	return fmt.Errorf("aborted with the tree unchanged at %s", strings.Join(lines, "; "))
}

// printWriteDomain prints the tracked paths the pass writes under the
// harness's confinement, and names that confinement in the count line.
// The measurement is the one surface that admits an unconfined run, so it
// is where an operator compares a confinement against the whole domain
// before applying it, and where a pair of confinements is checked to
// partition what the passes write.
//
// The paths are the site-rewrite domain together with the path-keyed
// registers the pass rekeys through its key channel, both under the same
// confinement, so the measurement accounts for every file a run under
// that confinement writes. A measurement over the site domain alone would
// name none of the registers a renaming run rewrites, and a partition
// checked against it would leave them unaccounted for.
func printWriteDomain(ctx context.Context, h *pass.Harness, r pass.Rewriter, out io.Writer) error {
	domain, err := scope.WriteDomain(ctx, h.List, r.Pass(), h.Read)
	if err != nil {
		return err
	}
	domain = h.Confine.Filter(domain)
	keyed, err := h.KeyWriteTargets(ctx, r)
	if err != nil {
		return err
	}
	domain = append(domain, keyed...)
	sort.Strings(domain)
	for _, target := range domain {
		fmt.Fprintln(out, target)
	}
	fmt.Fprintf(out, "# %d file(s) in the %s pass write domain (%s)\n", len(domain), r.Pass(), h.Confine)
	return nil
}
