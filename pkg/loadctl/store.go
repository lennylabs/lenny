// SPDX-License-Identifier: MIT

package loadctl

import (
	"context"
	"errors"
)

// Store is the run-state persistence interface. The Server resolves
// a Store from the configured DatabaseURL (memory:// or postgres://).
type Store interface {
	CreateRun(ctx context.Context, r *Run) error
	GetRun(ctx context.Context, id string) (*Run, error)
	UpdateRun(ctx context.Context, r *Run) error
	ListRuns(ctx context.Context) ([]*Run, error)
	PinBaseline(ctx context.Context, name, runID string) error
	Close() error
}

// ErrRunNotFound is returned when the requested run is unknown.
var ErrRunNotFound = errors.New("loadctl: run not found")
