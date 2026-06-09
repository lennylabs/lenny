// SPDX-License-Identifier: MIT

package slotlayout

import (
	"fmt"
	"os"
)

// EnsureTree creates the per-slot directory tree the §6.4 adapter
// responsibility requires on slot assignment: the slot's `current/`,
// `staging/`, `/sessions/{slotId}/`, `/artifacts/{slotId}/`, and the
// `/run/lenny/slots/{slotId}/` credential directory. It is idempotent —
// re-creating an existing slot tree is a no-op — so a retried slot bind
// does not error. Each directory is chmod'd to its exact mode after
// MkdirAll so the process umask cannot strip the bits the runtime or the
// lenny-cred-readers group needs. A path that is empty (its root was
// unconfigured) is skipped.
//
// spec: §6.4 lines 401-405 — "Adapter — creates ... per-slot directory
// trees ... on slotId assignment".
func EnsureTree(p SlotPaths) error {
	for _, d := range []struct {
		path string
		mode os.FileMode
	}{
		{p.Current, CurrentMode},
		{p.Staging, StagingMode},
		{p.Sessions, SessionsMode},
		{p.Artifacts, ArtifactsMode},
		{p.CredentialsDir, CredentialsDirMode},
	} {
		if d.path == "" {
			continue
		}
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			return fmt.Errorf("slotlayout: create %q: %w", d.path, err)
		}
		if err := os.Chmod(d.path, d.mode); err != nil {
			return fmt.Errorf("slotlayout: chmod %q: %w", d.path, err)
		}
	}
	return nil
}

// RemoveTree removes the per-slot directory tree on slot cleanup. It
// removes the slot's `/workspace/slots/{slotId}` root (current + staging),
// `/sessions/{slotId}`, `/artifacts/{slotId}`, and
// `/run/lenny/slots/{slotId}` credential directory. Removal is
// best-effort across the trees: a removal error on one tree is
// accumulated but does not abort the others, so a partial cleanup still
// drops as much per-slot state as possible. An already-absent tree is not
// an error (os.RemoveAll returns nil for a missing path).
//
// spec: §6.4 lines 401-405 — "... removes it during slot cleanup".
func RemoveTree(p SlotPaths) error {
	var firstErr error
	for _, dir := range []string{p.slotRoot(), p.Sessions, p.Artifacts, p.CredentialsDir} {
		if dir == "" {
			continue
		}
		if err := os.RemoveAll(dir); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("slotlayout: remove %q: %w", dir, err)
		}
	}
	return firstErr
}
