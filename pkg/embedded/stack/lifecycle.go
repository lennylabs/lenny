// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"fmt"
	"io"
)

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
	// CLIVersion is the running lenny CLI build version. lenny up records it
	// as the deployed image tag and a warm bring-up reconciles against it,
	// re-importing and re-applying the embedded manifests on a CLI-upgrade
	// mismatch (C4). spec: §17.4.
	CLIVersion string
	// Out and ErrOut receive progress and error output.
	Out    io.Writer
	ErrOut io.Writer
}

// RunUp implements the foreground `lenny up` command. §17.4: lenny up is
// idempotent — a second invocation reuses the persisted substrate and image
// store and restarts the in-cluster control plane in seconds. The control
// plane runs as in-cluster pods, so lenny up brings the stack up in-process
// (the in-cluster pods outlive the CLI) rather than re-executing a detached
// host supervisor. RunUp drives the in-cluster bring-up through Up, which
// provisions (or restarts) the substrate, imports the platform images,
// applies the embedded manifests, seeds the echo runtime, starts the
// host-side forwarder, and waits for the gateway to become ready.
//
// spec: §17.4 (the control plane runs as in-cluster pods; the substrate and
// imported-image store persist across down/up).
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
		CLIVersion:  opts.CLIVersion,
		Out:         out,
	})
	if err != nil {
		fmt.Fprintf(errOut, "lenny up: %v\n", err)
		return err
	}
	return nil
}

// DownOptions configures the lenny down command.
type DownOptions struct {
	// Root is the Embedded Mode state directory.
	Root string
	// Purge removes the persisted substrate and the entire state directory
	// after teardown. Without it, lenny down stops the substrate while
	// persisting the substrate and its imported-image store so a warm lenny
	// up restarts in seconds.
	Purge bool
	// Out and ErrOut receive progress and error output.
	Out    io.Writer
	ErrOut io.Writer
}

// RunDown implements the `lenny down` command. §17.4: lenny down stops the
// in-cluster control plane by stopping the substrate. By default it persists
// the substrate and its imported-image store (a Docker-backed `docker stop`
// keeps the container and its containerd image store; the Linux process stop
// leaves the k3s data directory on disk), so a warm lenny up restarts in
// seconds. `--purge` removes the substrate (force-remove the container, then
// purge the state directory that holds the Linux data directory). The
// in-memory application stores live inside the gateway pod and are lost on
// down regardless of `--purge`.
//
// The control plane runs as in-cluster pods that outlive the CLI, so RunDown
// reconstructs the substrate handle from the recorded state file rather than
// a live launcher: it stops or removes the Docker-backed container by its
// recorded name and the Linux k3s process group by its recorded leader PID.
//
// spec: §17.4 (lenny down persists the substrate and the imported-image
// store; --purge removes them; the application stores are ephemeral), §24.19.
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

	if opts.Purge {
		fmt.Fprintln(out, "lenny down: removing the embedded stack")
		// Remove the substrate before purgeRoot discards the state directory
		// that records its handle. The Docker-backed container runs inside the
		// Docker VM, so purgeRoot's os.RemoveAll never reaches it; without
		// this removal a --purge orphans the container with no recorded handle
		// to find it by. The Linux process is terminated by its recorded PID;
		// its data directory is then removed by purgeRoot below. Each handle
		// is empty on the other launcher, where its stop is a no-op.
		// spec: §17.4 (--purge removes the persisted substrate and the
		// imported-image store).
		removeSubstrateContainer(st.K3sContainer)
		stopSubstrateProcess(st.K3sPID)
		_ = removeState(paths.StateFile())
		if err := purgeRoot(root); err != nil {
			return err
		}
		fmt.Fprintf(out, "lenny down: purged %s\n", root)
		return nil
	}

	fmt.Fprintln(out, "lenny down: stopping the embedded stack")
	// Stop the substrate while persisting it and its imported-image store so a
	// warm lenny up restarts it. The Docker-backed container is `docker stop`d
	// (the container and its containerd image store persist); the Linux
	// process group is terminated by its recorded PID (the k3s data directory
	// persists on disk). The state file is cleared so a later lenny status
	// reports no running stack; lenny up rewrites it on the warm restart.
	// spec: §17.4 (lenny down persists the substrate and the imported-image
	// store).
	stopSubstrateContainer(st.K3sContainer)
	stopSubstrateProcess(st.K3sPID)
	_ = removeState(paths.StateFile())
	fmt.Fprintln(out, "lenny down: stack stopped")
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
