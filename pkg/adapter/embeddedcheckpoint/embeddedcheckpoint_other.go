// SPDX-License-Identifier: MIT

//go:build !linux

package embeddedcheckpoint

import "context"

// Pause returns ErrNotSupported on every non-Linux host.
//
// spec: §4.4 line 246 — "On non-Linux hosts where /proc/{pid}/stat is
// unavailable, the adapter skips polling".
func (h *Helper) Pause(_ context.Context) error { return ErrNotSupported }

// Resume returns ErrNotSupported on every non-Linux host.
func (h *Helper) Resume(_ context.Context) error { return ErrNotSupported }

// Checkpoint returns ErrNotSupported on every non-Linux host.
func (h *Helper) Checkpoint(_ context.Context, _ CheckpointWork) error {
	return ErrNotSupported
}
