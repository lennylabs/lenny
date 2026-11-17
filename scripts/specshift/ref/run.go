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
// The stub command turns a numbered section whose content has migrated
// into a successor stub, as "Successor stubs" in spec/conventions.md
// states, and lookup follows a section citation through that stub.
//
// Usage:
//
//	specshift ref [-root <path>] registry [-check]
//	specshift ref [-root <path>] check
//	specshift ref [-root <path>] fix
//	specshift ref [-root <path>] lookup <identifier | code | §N.M>
//	specshift ref [-root <path>] stub <§N.M> --to <name>[,<name>...] [--keep] [--recursive]
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
const usage = "usage: specshift ref [-root <path>] registry [-check] | check | fix | lookup <identifier | code | §N.M> | stub <§N.M> --to <name>[,<name>...] [--keep] [--recursive]"

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
	case "stub":
		return runStub(ctx, t, args[1:], out)
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
		current, err := scope.ReadChecked(t.Root, RegistryPath)
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

// listFlag is a flag that takes a comma-separated list and may be
// repeated.
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }

func (l *listFlag) Set(v string) error {
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			*l = append(*l, item)
		}
	}
	return nil
}

// runStub parses the stub command, whose flags may come before or after
// the section, rewrites the section, and prints what it changed.
func runStub(ctx context.Context, t *Tree, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("specshift ref stub", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var to listFlag
	fs.Var(&to, "to", "the successors: page codes, page paths, identifiers, or anchors (comma-separated, repeatable)")
	keep := fs.Bool("keep", false, "keep the section's text below the successor list and write no stub marker")
	recursive := fs.Bool("recursive", false, "accept a section with subsections and stub every heading under it")
	var positional []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return fmt.Errorf("parse flags: %w; %s", err, usage)
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(positional) != 1 {
		return errors.New(usage)
	}
	res, err := Stub(ctx, t, StubOptions{Section: positional[0], To: to, Keep: *keep, Recursive: *recursive})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "stubbed §%s in %s\n", res.Section, res.File)
	for _, c := range res.Changes {
		if c.Full {
			fmt.Fprintf(out, "  %s: full stub, %d line(s) of text removed\n", c.Title, c.Removed)
			continue
		}
		fmt.Fprintf(out, "  %s: partial stub, text kept\n", c.Title)
	}
	fmt.Fprintln(out, "successors:")
	for _, s := range res.Successors {
		fmt.Fprintf(out, "  %s\n", s)
	}
	return nil
}
