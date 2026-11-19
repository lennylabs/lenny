// SPDX-License-Identifier: MIT

package sessionstore

import (
	"context"
	"errors"
)

// LastSeqCounter adapts a Store to the §7.3 sessions.last_seq
// durability hooks on the session event bus. It satisfies both
// sessionevents.LastSeqPersister (advance on every publish) and
// sessionevents.LastSeqLoader (seed the in-memory counter on the first
// publish for the session on a replica). Both methods are best-effort:
// a Postgres outage degrades to the bus's local counter without
// dropping events.
//
// spec: §7.3, §10.4.
type LastSeqCounter struct{ Sessions Store }

// LoadLastSeq returns the persisted sessions.last_seq counter so the
// bus seeds its local counter on the first publish for the session,
// and a replica publishing for a session another replica coordinates
// never rewinds the sequence. A missing row reads as zero so a fresh
// session starts at 1.
func (l LastSeqCounter) LoadLastSeq(ctx context.Context, tenantID, sessionID string) (int64, error) {
	sess, err := l.Sessions.Get(ctx, tenantID, sessionID)
	if errors.Is(err, ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return sess.LastSeq, nil
}

// AdvanceLastSeq persists the new per-session sequence. The mutate
// callback only raises the value, and the pgstore's GREATEST floor in
// its update keeps the persisted value monotonic against late writers
// from sibling replicas.
func (l LastSeqCounter) AdvanceLastSeq(ctx context.Context, tenantID, sessionID string, seq int64) error {
	_, err := l.Sessions.Update(ctx, tenantID, sessionID, func(row *Session) error {
		if seq > row.LastSeq {
			row.LastSeq = seq
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
