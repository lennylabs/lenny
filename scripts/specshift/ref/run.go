// SPDX-License-Identifier: MIT

// Package ref is the reference pass of specshift. It builds the registry
// of the new specification tree (spec/registry.json), checks every link
// into that tree, rewrites link paths after a page or an identifier
// moves, and looks up where an identifier or a numbered section is.
//
// Every link into the new tree targets a page or an identifier anchor,
// and its path is derived data: the committed registry records where
// each page and identifier was, the tree says where it is now, and the
// fix command rewrites the paths between the two. The rules are those of
// "References and moves" in the specification-structure change's
// taxonomy.
//
// Usage:
//
//	specshift ref [-root <path>] registry [-check]
//	specshift ref [-root <path>] check
//	specshift ref [-root <path>] fix
//	specshift ref [-root <path>] lookup <identifier | code | §N.M>
package ref

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// usage is the command synopsis printed with a usage error.
const usage = "usage: specshift ref [-root <path>] registry [-check] | check | fix | lookup <identifier | code | §N.M>"

// Main runs the ref subcommand with its arguments. The repository root
// is the -root flag, or the git toplevel of the working directory.
func Main(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("specshift ref", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", "", "repo root (default: the git toplevel of the working directory)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w; %s", err, usage)
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
	return Run(ctx, NewTree(resolved), fs.Args(), out)
}

// Run runs one command of the ref subcommand over a tree.
func Run(ctx context.Context, t *Tree, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "registry":
		fs := flag.NewFlagSet("specshift ref registry", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		check := fs.Bool("check", false, "fail when the committed registry differs from a fresh build")
		if err := fs.Parse(args[1:]); err != nil {
			return fmt.Errorf("parse flags: %w; %s", err, usage)
		}
		if fs.NArg() != 0 {
			return errors.New(usage)
		}
		return runRegistry(ctx, t, *check, out)
	case "check":
		if len(args) != 1 {
			return errors.New(usage)
		}
		return runCheck(ctx, t, out)
	case "fix":
		if len(args) != 1 {
			return errors.New(usage)
		}
		return runFix(ctx, t, out)
	case "lookup":
		if len(args) != 2 {
			return errors.New(usage)
		}
		return Lookup(ctx, t, args[1], out)
	default:
		return fmt.Errorf("unknown command %q; %s", args[0], usage)
	}
}

// runRegistry builds the registry and writes it, or with check compares
// it with the committed file.
func runRegistry(ctx context.Context, t *Tree, check bool, out io.Writer) error {
	committed, err := LoadCommitted(t)
	if err != nil {
		return err
	}
	reg, findings, err := Build(ctx, t, committed)
	if err != nil {
		return err
	}
	fresh, err := reg.Encode()
	if err != nil {
		return err
	}
	if check {
		current, err := os.ReadFile(t.abs(RegistryPath))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read %s: %w", RegistryPath, err)
		}
		if !bytes.Equal(current, fresh) {
			return fmt.Errorf("%s differs from a fresh build (%s); run `specshift ref registry` and commit the result", RegistryPath, firstDifference(string(current), string(fresh)))
		}
		fmt.Fprintf(out, "%s is current: %s\n", RegistryPath, summary(reg))
		return nil
	}
	if err := writeRegistry(t, reg); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s: %s\n", RegistryPath, summary(reg))
	if len(findings) > 0 {
		fmt.Fprintf(out, "%d problem(s) found while building; run `specshift ref check`\n", len(findings))
	}
	return nil
}

// summary counts what a registry holds.
func summary(r *Registry) string {
	return fmt.Sprintf("%d page(s), %d requirement(s), %d operation(s), %d term(s), %d block(s), %d retired, %d move(s)",
		len(r.Pages), len(r.Requirements), len(r.Operations), len(r.Terms), len(r.Blocks), len(r.Retired), len(r.Moves))
}

// firstDifference names the first line at which two texts differ.
func firstDifference(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return fmt.Sprintf("line %d: committed %q, fresh %q", i+1, strings.TrimSpace(x), strings.TrimSpace(y))
		}
	}
	return "no line differs"
}

// runCheck prints every finding and fails when there is one.
func runCheck(ctx context.Context, t *Tree, out io.Writer) error {
	findings, err := Check(ctx, t)
	if err != nil {
		return err
	}
	for _, f := range findings {
		fmt.Fprintln(out, f)
	}
	if len(findings) > 0 {
		return fmt.Errorf("ref check: %d problem(s)", len(findings))
	}
	fmt.Fprintln(out, "ref check: no problems")
	return nil
}

// runFix rewrites moved links and reports what it changed.
func runFix(ctx context.Context, t *Tree, out io.Writer) error {
	res, err := Fix(ctx, t)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "rewrote %d link(s) in %d file(s)\n", res.Links, len(res.Files))
	for _, f := range res.Files {
		fmt.Fprintf(out, "  %s\n", f)
	}
	for _, u := range res.Unresolved {
		fmt.Fprintf(out, "unresolved: %s\n", u)
	}
	fmt.Fprintf(out, "wrote %s\n", RegistryPath)
	return nil
}
