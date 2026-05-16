// SPDX-License-Identifier: MIT

// Package erasure implements the §12.8 GDPR erasure orchestrator: the
// dependency-ordered DeleteByUser job that removes a user's data from
// every per-store erasure adapter.
//
// Stores fall into two shapes. User-scoped stores (sessions,
// interactions) carry a user id and expose a DeleteByUser adapter.
// Session-scoped stores (transcripts, blobs) are keyed by session and
// expose a DeleteBySession adapter; the orchestrator first lists the
// user's session ids, then erases the session-scoped stores for each,
// before erasing the user-scoped stores — so a child store whose rows
// reference a session is erased before the session rows themselves.
//
// The job is fail-fast: the first store error aborts it and is
// returned with the partial result, so a §12.8 phase-recovery retry
// resumes from a consistent point.
package erasure

import (
	"context"
	"fmt"
)

// DeleteByUserFunc is a user-scoped store's §12.8 erasure adapter: it
// removes the user's data within the tenant and returns the count
// deleted.
type DeleteByUserFunc func(ctx context.Context, tenantID, userID string) (int, error)

// DeleteBySessionFunc is a session-scoped store's §12.8 erasure
// adapter: it removes a session's data within the tenant and returns
// the count deleted.
type DeleteBySessionFunc func(ctx context.Context, tenantID, sessionID string) (int, error)

// SessionLister enumerates a user's session ids. The orchestrator
// needs them to erase session-scoped stores before the session rows
// themselves are deleted.
type SessionLister func(ctx context.Context, tenantID, userID string) ([]string, error)

// Eraser pairs a store name with its per-user erasure adapter.
type Eraser struct {
	Name         string
	DeleteByUser DeleteByUserFunc
}

// SessionEraser pairs a store name with its per-session erasure
// adapter, for stores keyed by session rather than by user.
type SessionEraser struct {
	Name            string
	DeleteBySession DeleteBySessionFunc
}

// Config builds an Orchestrator.
type Config struct {
	// Sessions lists a user's session ids. When nil, the
	// session-scoped erasers are skipped.
	Sessions SessionLister
	// SessionScoped erasers run per session id, in order, before the
	// user-scoped erasers.
	SessionScoped []SessionEraser
	// UserScoped erasers run per user, in dependency order.
	UserScoped []Eraser
}

// Orchestrator runs DeleteByUser across the configured stores.
type Orchestrator struct {
	sessions      SessionLister
	sessionScoped []SessionEraser
	userScoped    []Eraser
}

// New returns an Orchestrator for the given store configuration.
func New(cfg Config) *Orchestrator {
	return &Orchestrator{
		sessions:      cfg.Sessions,
		sessionScoped: append([]SessionEraser(nil), cfg.SessionScoped...),
		userScoped:    append([]Eraser(nil), cfg.UserScoped...),
	}
}

// Result is the outcome of a DeleteByUser job.
type Result struct {
	// Deleted maps each store's name to its deleted-row count, for the
	// stores the job reached.
	Deleted map[string]int
	// Total is the sum of Deleted across stores.
	Total int
}

// DeleteByUser runs the §12.8 erasure. It first erases every
// session-scoped store for each of the user's sessions, then erases
// the user-scoped stores. It is fail-fast: the first store error
// aborts the job and is returned with the partial Result covering the
// stores erased so far.
func (o *Orchestrator) DeleteByUser(ctx context.Context, tenantID, userID string) (Result, error) {
	res := Result{Deleted: map[string]int{}}

	if o.sessions != nil && len(o.sessionScoped) > 0 {
		sessionIDs, err := o.sessions(ctx, tenantID, userID)
		if err != nil {
			return res, fmt.Errorf("erasure: list sessions for user %q: %w", userID, err)
		}
		for _, se := range o.sessionScoped {
			for _, sid := range sessionIDs {
				n, err := se.DeleteBySession(ctx, tenantID, sid)
				if err != nil {
					return res, fmt.Errorf("erasure: store %q DeleteBySession(%s): %w", se.Name, sid, err)
				}
				res.Deleted[se.Name] += n
				res.Total += n
			}
		}
	}

	for _, e := range o.userScoped {
		n, err := e.DeleteByUser(ctx, tenantID, userID)
		if err != nil {
			return res, fmt.Errorf("erasure: store %q DeleteByUser: %w", e.Name, err)
		}
		res.Deleted[e.Name] += n
		res.Total += n
	}
	return res, nil
}
