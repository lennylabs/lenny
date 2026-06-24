// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"fmt"
	"io"
)

// SuperviseEnvVar named the environment variable the foreground lenny up
// set when it re-executed itself as the detached host supervisor. The
// §17.4 control plane now runs as in-cluster pods, so lenny up brings the
// stack up in-process and there is no detached supervisor to re-exec into.
// The constant is retained as an empty marker until the hidden
// __supervise dispatch is removed (proposal 0017 C2); RunSupervisor is a
// no-op alias for the in-process bring-up.
const SuperviseEnvVar = "LENNY_EMBEDDED_SUPERVISE"

// UpOptions configures the foreground lenny up command.
type UpOptions struct {
	// Root is the Embedded Mode state directory. Empty resolves to the
	// default.
	Root string
	// HTTPPort and HTTPSPort are the host-side forwarder listen ports.
	// Zero uses the §17.4 defaults.
	HTTPPort  int
	HTTPSPort int
	// EchoTarball overrides the path to the pre-built echo-embedded
	// docker-save tarball the bring-up imports (the LENNY_ECHO_TARBALL
	// operator override). Empty discovers it alongside the lenny binary.
	// spec: §24.19.1 (the --file import path).
	EchoTarball string
	// Out and ErrOut receive progress and error output.
	Out    io.Writer
	ErrOut io.Writer
}

// RunUp implements the foreground `lenny up` command. §17.4: lenny up is
// idempotent — a second invocation is a no-op when a stack is already
// running. The control plane runs as in-cluster pods, so lenny up brings
// the stack up in-process (the in-cluster pods outlive the CLI) rather than
// re-executing a detached host supervisor.
//
// The in-cluster bring-up sequence lands in the next build step (proposal
// 0017 C2); RunUp drives Up directly here.
func RunUp(ctx context.Context, opts UpOptions) error {
	out := orDiscard(opts.Out)
	errOut := orDiscard(opts.ErrOut)

	root, err := resolveRoot(opts.Root)
	if err != nil {
		return err
	}

	_, err = Up(ctx, Config{
		Root:        root,
		HTTPPort:    opts.HTTPPort,
		HTTPSPort:   opts.HTTPSPort,
		EchoTarball: opts.EchoTarball,
		Out:         out,
	})
	if err != nil {
		fmt.Fprintf(errOut, "lenny up: %v\n", err)
		return err
	}
	return nil
}

// RunSupervisor is the in-process bring-up entry point. The §17.4 control
// plane runs as in-cluster pods, so there is no detached host supervisor:
// RunSupervisor brings the stack up in-process and returns. It is retained
// so the lenny binary's hidden __supervise dispatch keeps compiling until
// that dispatch is removed (proposal 0017 C2).
func RunSupervisor(ctx context.Context, opts UpOptions) error {
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return err
	}
	_, err = Up(ctx, Config{
		Root:        root,
		HTTPPort:    opts.HTTPPort,
		HTTPSPort:   opts.HTTPSPort,
		EchoTarball: opts.EchoTarball,
		Out:         orDiscard(opts.Out),
	})
	return err
}

// DownOptions configures the lenny down command.
type DownOptions struct {
	// Root is the Embedded Mode state directory.
	Root string
	// Purge removes the entire state directory after teardown.
	Purge bool
	// Out and ErrOut receive progress and error output.
	Out    io.Writer
	ErrOut io.Writer
}

// RunDown implements the `lenny down` command. §17.4: lenny down stops the
// stack; the persisted substrate and imported-image store survive a
// non-`--purge` down and `--purge` removes them. The in-memory application
// stores live inside the gateway pod and are lost on down regardless of
// `--purge`. RunDown stops the substrate (which stops the in-cluster pods)
// and removes the Docker-backed k3s container by its recorded handle so a
// teardown does not orphan it.
func RunDown(ctx context.Context, opts DownOptions) error {
	out := orDiscard(opts.Out)
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return err
	}
	paths := NewPaths(root)

	st, ok, err := readState(paths.StateFile())
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(out, "lenny down: no embedded stack is running")
		if opts.Purge {
			if err := purgeRoot(root); err != nil {
				return err
			}
			fmt.Fprintf(out, "lenny down: purged %s\n", root)
		}
		return nil
	}

	fmt.Fprintln(out, "lenny down: stopping the embedded stack")
	// The substrate-stop that tears down the in-cluster control plane lands
	// with the version-aware lifecycle in the next build step (proposal 0017
	// C4). Here RunDown removes the Docker-backed k3s container by its
	// recorded handle and clears the state file so a teardown does not orphan
	// the container or leave a stale state record.
	//
	// Remove the Docker-backed k3s container (macOS and Windows) by its
	// recorded handle before removeState and purgeRoot discard it. The
	// container runs inside the Docker VM, so purgeRoot's os.RemoveAll does
	// not reach it; without this removal a --purge orphans the container with
	// no recorded handle to find it by. The handle is empty on the Linux
	// child-process substrate, where RemoveContainer is a no-op.
	// spec: §24.19 (lenny up/down manage the substrate; a teardown or --purge
	// must not leak the Docker-backed k3s container).
	removeSubstrateContainer(st.K3sContainer)
	_ = removeState(paths.StateFile())
	fmt.Fprintln(out, "lenny down: stack stopped")

	if opts.Purge {
		if err := purgeRoot(root); err != nil {
			return err
		}
		fmt.Fprintf(out, "lenny down: purged %s\n", root)
	}
	return nil
}

// resolveRoot resolves the Embedded Mode state directory: the explicit
// value, or the default location.
func resolveRoot(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return DefaultRoot()
}

// orDiscard returns w, or io.Discard when w is nil.
func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}
