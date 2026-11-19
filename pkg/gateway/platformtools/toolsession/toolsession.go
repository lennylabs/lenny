// SPDX-License-Identifier: MIT

package toolsession

import (
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// Record returns the session record of a stored row, carrying the row.
func Record(row sessionstore.Session) seam.Session {
	var lastSeq uint64
	if row.LastSeq > 0 {
		lastSeq = uint64(row.LastSeq)
	}
	return seam.Session{
		ID:              row.ID,
		TenantID:        row.TenantID,
		UserID:          row.UserID,
		ParentSessionID: row.ParentSessionID,
		RootSessionID:   row.RootSessionID,
		RuntimeRef:      row.RuntimeRef,
		Pool:            row.PoolRef,
		State:           state.State(row.State),
		LastSeq:         lastSeq,
		Stored:          row,
	}
}

// Row returns the stored row a session record carries. A record that
// carries none returns the row its fields describe, with every other
// field zero.
func Row(s seam.Session) sessionstore.Session {
	if row, ok := s.Stored.(sessionstore.Session); ok {
		return row
	}
	var lastSeq int64
	if s.LastSeq > 0 {
		lastSeq = int64(s.LastSeq)
	}
	return sessionstore.Session{
		ID:              s.ID,
		TenantID:        s.TenantID,
		UserID:          s.UserID,
		ParentSessionID: s.ParentSessionID,
		RootSessionID:   s.RootSessionID,
		RuntimeRef:      s.RuntimeRef,
		PoolRef:         s.Pool,
		State:           session.State(s.State),
		LastSeq:         lastSeq,
	}
}
