// SPDX-License-Identifier: MIT

package pagegate

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/lennylabs/lenny/scripts/specshift/defs"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// Command is the specshift subcommand that runs this package.
const Command = "pages"

const usage = `usage: specshift pages [-root <path>] <command>

commands:
  check                          run the gloss, normative-keyword, style, and review-comment gates,
                                 and report the pages over their word budget
  contributions -manifests <f>   hold the contribution blocks and the unit register against
                                 the rendered chart in <f>, the output of helm template
`

// ErrProblems is returned when a gate finds problems, after they are
// written to the output.
var ErrProblems = errors.New("the specification pages have problems")

// Run runs the pages subcommand with the arguments that follow its name.
func Run(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("specshift pages", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { fmt.Fprint(out, usage) }
	root := fs.String("root", "", "repo root (default: the git toplevel of the working directory)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return errors.New("a pages command is required: check or contributions")
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
	t, err := defs.LoadTree(resolved)
	if err != nil {
		return err
	}
	switch rest[0] {
	case "check":
		if len(rest) > 1 {
			return fmt.Errorf("pages check takes no arguments, got %v", rest[1:])
		}
		pages, err := Pages(resolved, t)
		if err != nil {
			return err
		}
		for _, w := range Sizes(pages) {
			fmt.Fprintf(out, "warning: %s\n", w)
		}
		if err := report(out, Check(pages)); err != nil {
			return err
		}
		fmt.Fprintf(out, "pages check: %d page(s), no problems\n", len(pages))
		return nil
	case "contributions":
		cfs := flag.NewFlagSet("specshift pages contributions", flag.ContinueOnError)
		cfs.SetOutput(out)
		manifests := cfs.String("manifests", "", "the output of helm template for the chart")
		if err := cfs.Parse(rest[1:]); err != nil {
			return fmt.Errorf("parse flags: %w", err)
		}
		if *manifests == "" {
			return errors.New("pages contributions needs -manifests")
		}
		data, err := os.ReadFile(*manifests)
		if err != nil {
			return fmt.Errorf("read the rendered chart: %w", err)
		}
		objs, err := ParseManifests(data)
		if err != nil {
			return err
		}
		units, err := LoadUnits(filepath.Join(resolved, filepath.FromSlash(UnitRegisterPath)))
		if err != nil {
			return err
		}
		problems := append(CheckUnits(t, objs, units), CheckContributions(t, objs, units)...)
		if err := report(out, problems); err != nil {
			return err
		}
		fmt.Fprintf(out, "pages contributions: %d rendered object(s), no problems\n", len(objs))
		return nil
	}
	fs.Usage()
	return fmt.Errorf("unknown pages command %q", rest[0])
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
