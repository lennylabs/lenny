// SPDX-License-Identifier: MIT

// Package seamrow converts between the session store's row and the seam
// interface's session record for the packages of the platform tools seam.
// The packages read and write session rows through the session store, and
// they hand the session core a session record and receive one from it.
//
// spec: GWTOOLS
package seamrow

import (
	"context"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// Record returns the session record of a stored session. The record's pool
// is the row's pool reference, or its pool name when the row records no
// reference.
func Record(row sessionstore.Session) seam.Session {
	rec := seam.Session{
		ID:              row.ID,
		TenantID:        row.TenantID,
		UserID:          row.UserID,
		ParentSessionID: row.ParentSessionID,
		RootSessionID:   row.RootSessionID,
		RuntimeRef:      row.RuntimeRef,
		Pool:            row.PoolRef,
		State:           state.State(row.State),
		DelegationDepth: int(row.DelegationDepth),
		PodAssignment:   row.PodAssignment,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		TerminatedAt:    row.TerminatedAt,
	}
	if rec.Pool == "" {
		rec.Pool = row.Pool
	}
	if row.LastSeq > 0 {
		rec.LastSeq = uint64(row.LastSeq)
	}
	return rec
}

// Row returns the session store's row for a session record, with the
// fields the record carries. The record's pool becomes the row's pool
// reference.
func Row(rec seam.Session) sessionstore.Session {
	row := sessionstore.Session{
		ID:              rec.ID,
		TenantID:        rec.TenantID,
		UserID:          rec.UserID,
		State:           session.State(rec.State),
		CreatedAt:       rec.CreatedAt,
		UpdatedAt:       rec.UpdatedAt,
		TerminatedAt:    rec.TerminatedAt,
		RuntimeRef:      rec.RuntimeRef,
		PoolRef:         rec.Pool,
		ParentSessionID: rec.ParentSessionID,
		RootSessionID:   rec.RootSessionID,
		PodAssignment:   rec.PodAssignment,
	}
	if rec.DelegationDepth > 0 {
		row.DelegationDepth = uint32(rec.DelegationDepth)
	}
	if rec.LastSeq > 0 {
		row.LastSeq = int64(rec.LastSeq)
	}
	return row
}

// Terminals adapts the session core's terminal pipeline operations to the
// terminal hook of a platform tools sweep that holds stored rows, such as
// the orphan cleanup.
type Terminals struct {
	t seam.Terminals
}

// TerminalHook returns the hook over t. t is required.
func TerminalHook(t seam.Terminals) Terminals { return Terminals{t: t} }

// BeginTerminal opens the core's terminal claim for a session.
func (h Terminals) BeginTerminal(sessionID string) func() { return h.t.BeginTerminal(sessionID) }

// OnSessionTerminal runs the core's terminal pipeline for the row the
// sweep's write produced.
func (h Terminals) OnSessionTerminal(ctx context.Context, from session.State, row sessionstore.Session) {
	h.t.OnSessionTerminal(ctx, state.State(from), Record(row))
}
