// SPDX-License-Identifier: MIT

package docgate

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/defs"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// Command is the specshift subcommand that runs this package.
const Command = "docs"

const usage = `usage: specshift docs [-root <path>] <command>

commands:
  check    hold the documentation's spec front matter against spec/registry.json, and
           the names and values the pages in scope quote against the catalogs
  impact   hold the branch's diff against main: a changed requirement, operation, or
           definition block on a landed page needs an edited documentation page that
           names the page, or a change whose Documentation section waives the page
           with the line "No reader-facing behavior changes: CODE, CODE"
`

// ErrProblems is returned when a gate finds problems, after they are
// written to the output.
var ErrProblems = errors.New("the documentation has problems")

// Run runs the docs subcommand with the arguments that follow its name.
func Run(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("specshift docs", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { fmt.Fprint(out, usage) }
	root := fs.String("root", "", "repo root (default: the git toplevel of the working directory)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return errors.New("a docs command is required: check or impact")
	}
	if len(rest) > 1 {
		return fmt.Errorf("docs %s takes no arguments, got %s", rest[0], strings.Join(rest[1:], " "))
	}
	resolved := *root
	if resolved == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}
		if resolved, err = scope.RepoRoot(ctx, cwd); err != nil {
			return err
		}
	}
	switch rest[0] {
	case "check":
		return runCheck(resolved, out)
	case "impact":
		return runImpact(ctx, resolved, out)
	}
	fs.Usage()
	return fmt.Errorf("unknown docs command %q", rest[0])
}

// CheckResult is what docs check found.
type CheckResult struct {
	// Pages is the number of documentation pages, WithSpec the number
	// whose front matter carries spec, and InScope the number the
	// catalog-name gate read.
	Pages, WithSpec, InScope int
	// FrontMatter are the front-matter problems.
	FrontMatter []Problem
	// Names are the catalog-name gate's problems and undecided findings.
	Names Result
}

// Check runs the front-matter and catalog-name gates over the tree at root.
func Check(root string) (CheckResult, error) {
	var res CheckResult
	pages, err := LoadPages(root)
	if err != nil {
		return res, err
	}
	st, err := LoadStatuses(root)
	if err != nil {
		return res, err
	}
	t, err := defs.LoadTree(root)
	if err != nil {
		return res, err
	}
	cat, err := NewCatalog(root, t)
	if err != nil {
		return res, err
	}
	res.Pages = len(pages)
	for _, p := range pages {
		if p.HasSpec {
			res.WithSpec++
		}
	}
	res.FrontMatter = CheckFrontMatter(pages, st)
	res.Names = CheckNames(pages, st, cat)
	res.InScope = res.Names.Pages
	return res, nil
}

func runCheck(root string, out io.Writer) error {
	res, err := Check(root)
	if err != nil {
		return err
	}
	for _, u := range res.Names.Undecided {
		fmt.Fprintf(out, "undecided: %s\n", u)
	}
	problems := append(append([]Problem(nil), res.FrontMatter...), res.Names.Problems...)
	sortProblems(problems)
	if err := report(out, problems); err != nil {
		return err
	}
	fmt.Fprintf(out, "docs check: %d page(s), %d with spec front matter, %d in scope for the catalog names, %d undecided, no problems\n",
		res.Pages, res.WithSpec, res.InScope, len(res.Names.Undecided))
	return nil
}

func runImpact(ctx context.Context, root string, out io.Writer) error {
	rep, err := CheckImpact(ctx, root)
	if errors.Is(err, ErrNoBase) {
		fmt.Fprintf(out, "docs impact: skipped: %v, so there is no branch diff to read\n", err)
		return nil
	}
	if err != nil {
		return err
	}
	if len(rep.NotYet) > 0 {
		fmt.Fprintf(out, "not checked yet, because the page has not landed: %s\n", strings.Join(rep.NotYet, ", "))
	}
	for _, w := range rep.Waivers {
		fmt.Fprintf(out, "%s states that no reader-facing behavior changes on %s\n", w.Folder, strings.Join(w.Codes, ", "))
	}
	if err := report(out, rep.Problems); err != nil {
		return err
	}
	fmt.Fprintf(out, "docs impact: against %s (merge base with %s), %d landed page(s) touched, no problems\n",
		short(rep.Base), rep.BaseRef, len(rep.Checked))
	return nil
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func report(out io.Writer, problems []Problem) error {
	if len(problems) == 0 {
		return nil
	}
	for _, p := range problems {
		fmt.Fprintln(out, p.String())
	}
	return fmt.Errorf("%w: %d problem(s)", ErrProblems, len(problems))
}
