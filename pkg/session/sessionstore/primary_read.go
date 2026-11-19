// SPDX-License-Identifier: MIT

package sessionstore

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultPrimaryReadTimeout bounds a ReadPrimary call that passes no timeout
// of its own. It matches the bound the activity stamper already puts on its
// primary write. The value is not fixed by the spec; the gateway's
// --session-primary-read-timeout flag overrides it.
const DefaultPrimaryReadTimeout = 5 * time.Second

// PrimaryReader is implemented by a store whose Get reads a lagging §12.3
// read pool and that can read one row from the primary without taking a row
// lock. ReadPrimary uses it when the store provides it, so a primary read never
// blocks a concurrent Update. A store that does not implement it is read
// through an Update whose mutator writes nothing.
type PrimaryReader interface {
	// GetPrimary returns the row as the primary holds it, scoped to
	// tenantID. It returns ErrNotFound for a missing row and for a row of
	// another tenant.
	GetPrimary(ctx context.Context, tenantID, id string) (Session, error)
}

// errPrimaryReadOnly aborts the Update that the read-only fallback of
// ReadPrimary issues, so the store writes nothing.
var errPrimaryReadOnly = errors.New("sessionstore: primary read writes nothing")

// ReadPrimary reads one session row from the primary for a one-shot decision
// that has no write of its own: a route, a skip, a nil return, or the expected
// value of a compare-and-swap. It writes nothing, so it advances no UpdatedAt
// and accrues no session age. It returns ErrNotFound and every other store
// error unchanged.
//
// The read runs under context.WithTimeout(ctx, timeout); a timeout at or below
// zero uses DefaultPrimaryReadTimeout, so no call is unbounded. The call
// returns when that context ends even if the store does not honor it, with an
// error that wraps the context's error (context.DeadlineExceeded on expiry).
// An expired deadline is a primary error to the caller.
//
// spec: §12.3 (Postgres HA Requirements)
func ReadPrimary(ctx context.Context, st Store, timeout time.Duration, tenantID, id string) (Session, error) {
	ctx, cancel := context.WithTimeout(ctx, PrimaryReadBound(timeout))
	defer cancel()
	type result struct {
		row Session
		err error
	}
	done := make(chan result, 1)
	go func() {
		row, err := readPrimaryOnce(ctx, st, tenantID, id)
		done <- result{row: row, err: err}
	}()
	select {
	case r := <-done:
		return r.row, r.err
	case <-ctx.Done():
		return Session{}, fmt.Errorf("sessionstore: read session %s from the primary: %w", id, ctx.Err())
	}
}

// PrimaryReadBound returns the bound ReadPrimary applies for timeout: timeout
// itself when it is positive, and DefaultPrimaryReadTimeout otherwise. A caller
// that bounds its own read of the same row, such as a replica read it re-reads
// from the primary on failure, uses it so both reads share one bound.
//
// spec: §12.3 (Postgres HA Requirements)
func PrimaryReadBound(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return DefaultPrimaryReadTimeout
	}
	return timeout
}

// readPrimaryOnce issues the primary read: GetPrimary when the store provides
// it, otherwise an Update whose mutator copies the locked row and aborts.
func readPrimaryOnce(ctx context.Context, st Store, tenantID, id string) (Session, error) {
	if pr, ok := st.(PrimaryReader); ok {
		return pr.GetPrimary(ctx, tenantID, id)
	}
	var seen Session
	_, err := st.Update(ctx, tenantID, id, func(r *Session) error {
		seen = *r
		return errPrimaryReadOnly
	})
	if errors.Is(err, errPrimaryReadOnly) {
		return seen, nil
	}
	return Session{}, err
}
