// SPDX-License-Identifier: MIT

//go:build !linux

package embeddedcheckpoint

import (
	"context"
	"errors"
	"testing"
)

// spec: §4.4 line 246 — every non-Linux entry point returns
// ErrNotSupported because /proc/{pid}/stat is unavailable.

func TestPauseReturnsNotSupportedOnNonLinux(t *testing.T) {
	h := &Helper{PID: 1, Stuck: &StuckFlag{}}
	if err := h.Pause(context.Background()); !errors.Is(err, ErrNotSupported) {
		t.Errorf("Pause err = %v, want ErrNotSupported", err)
	}
}

func TestResumeReturnsNotSupportedOnNonLinux(t *testing.T) {
	h := &Helper{PID: 1, Stuck: &StuckFlag{}}
	if err := h.Resume(context.Background()); !errors.Is(err, ErrNotSupported) {
		t.Errorf("Resume err = %v, want ErrNotSupported", err)
	}
}

func TestCheckpointReturnsNotSupportedOnNonLinux(t *testing.T) {
	h := &Helper{PID: 1, Stuck: &StuckFlag{}}
	if err := h.Checkpoint(context.Background(), nil); !errors.Is(err, ErrNotSupported) {
		t.Errorf("Checkpoint err = %v, want ErrNotSupported", err)
	}
}
