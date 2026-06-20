// SPDX-License-Identifier: MIT

//go:build !unix

package k3s

import (
	"context"
	"fmt"
	"path/filepath"
)

// childk3s_other.go provides the off-unix stub of the child-process
// launcher so the package links on Windows. The Linux child-process
// launcher needs POSIX process-group control and a Linux kernel, so its
// body is build-tagged unix-only (childk3s_unix.go). New never selects
// this stub at runtime: it picks the child-process launcher only on
// Linux (which is a unix build) and the Docker-backed launcher on macOS
// and Windows. The stub exists solely so New's cross-platform reference
// to newChildLauncher resolves on a non-unix build; its Start fails
// closed.
//
// spec: §17.4 (the embedded substrate is a managed child process only on
// Linux; macOS and Windows provision it through the Docker-backed
// launcher).

// childStub is the off-unix placeholder for the child-process launcher.
type childStub struct {
	cfg Config
}

// newChildLauncher returns the off-unix stub. The unix build provides
// the real managed child-process launcher under the same name.
func newChildLauncher(cfg Config) Launcher {
	return &childStub{cfg: cfg}
}

// KubeconfigPath returns the kubeconfig path the launcher would write,
// keeping the path convention identical across builds.
func (s *childStub) KubeconfigPath() string {
	return filepath.Join(s.cfg.Dir, "kubeconfig.yaml")
}

// Start fails closed: the managed child-process launcher cannot run off
// a unix host. New does not select it there, so this guards against a
// caller constructing it directly.
func (s *childStub) Start(_ context.Context) error {
	return fmt.Errorf("embedded k3s: the managed child-process launcher requires a unix host")
}

// Stop is a no-op: the stub never starts a process.
func (s *childStub) Stop() error { return nil }

// Running reports false: the stub never starts a process.
func (s *childStub) Running() bool { return false }

// PID returns zero: the stub never starts a process.
func (s *childStub) PID() int { return 0 }
