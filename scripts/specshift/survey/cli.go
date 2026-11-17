// SPDX-License-Identifier: MIT

package survey

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// Commands are the subcommands this package serves.
var Commands = []string{"digest", "where", "since"}

// usages are the synopses printed with a usage error.
var usages = map[string]string{
	"digest": "usage: specshift digest [-root <path>] [-base <rev>] [-stdout] [-force] <step | step range>",
	"where":  "usage: specshift where [-root <path>] <identifier | code | anchor>",
	"since":  "usage: specshift since [-root <path>] <YYYY-MM-DD | commit>",
}

// Run runs one of the commands with its arguments. The repository root is
// the -root flag, or the git toplevel of the working directory.
func Run(ctx context.Context, command string, args []string, out io.Writer) error {
	usage, ok := usages[command]
	if !ok {
		return fmt.Errorf("unknown command %q", command)
	}
	fs := flag.NewFlagSet("specshift "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", "", "repo root (default: the git toplevel of the working directory)")
	base := fs.String("base", "", "commit to compare with (digest; default: the merge base with main)")
	stdout := fs.Bool("stdout", false, "print the digest instead of writing it (digest)")
	force := fs.Bool("force", false, "replace a digest that exists (digest)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w; %s", err, usage)
	}
	if command != "digest" && (*base != "" || *stdout || *force) {
		return errors.New(usage)
	}
	if fs.NArg() != 1 {
		return errors.New(usage)
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
	arg := fs.Arg(0)
	switch command {
	case "digest":
		opts := DigestOptions{Step: arg, Base: *base}
		if *stdout {
			text, err := Digest(ctx, resolved, opts)
			if err != nil {
				return err
			}
			_, err = io.WriteString(out, text)
			return err
		}
		p, err := WriteDigest(ctx, resolved, opts, *force)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "wrote %s\n", p)
		return nil
	case "where":
		return Where(ctx, resolved, arg, out)
	default:
		return Since(ctx, resolved, arg, out)
	}
}
