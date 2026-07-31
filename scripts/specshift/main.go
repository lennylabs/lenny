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
//	  -apply             write the diff (default: dry run, write nothing)
//	  -domain            print the pass's write domain and exit
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
	apply    bool
	domain   bool
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
		scope.Name: name.New(scope.GitLister(root), scope.DirReader(root)),
		scope.Line: line.New(scope.GitLister(root), scope.DirReader(root)),
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
	if opts.domain {
		return printWriteDomain(ctx, harness, opts.pass, out)
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
	fmt.Fprintf(out, "%s pass (%s): %d file(s)\n", opts.pass, mode, len(diff.Files))
	for _, p := range diff.Paths() {
		fmt.Fprintf(out, "  %s\n", p)
	}
	return nil
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

// printWriteDomain prints the tracked paths the pass may write.
func printWriteDomain(ctx context.Context, h *pass.Harness, p scope.Pass, out io.Writer) error {
	domain, err := scope.WriteDomain(ctx, h.List, p, h.Read)
	if err != nil {
		return err
	}
	sort.Strings(domain)
	for _, target := range domain {
		fmt.Fprintln(out, target)
	}
	fmt.Fprintf(out, "# %d file(s) in the %s pass write domain\n", len(domain), p)
	return nil
}
