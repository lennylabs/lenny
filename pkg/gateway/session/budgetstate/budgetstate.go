// SPDX-License-Identifier: MIT

// Package budgetstate reclaims the per-session budget state a gateway
// replica holds for sessions that ended on another replica or were erased.
//
// The terminal pipeline forgets a session's per-session budget state only on
// the replica that runs the pipeline. The §11.2 enforcer counter, the §8.6
// usage recorder's granted delta, and the §11.2 direct-mode anomaly
// detector's accumulator that another replica built for the same session
// stay behind, as does the state of a session whose row was erased. Each
// replica therefore runs a Sweep: every interval it takes the union of the
// session ids its trackers hold, resolves them against the session store in
// batches, and forgets every id whose row is terminal or absent.
//
// The sweep reclaims memory only. It does not refuse a late request on a
// terminal session, and an entry such a request re-creates is swept on the
// next pass.
//
// spec: §11.2, §10.1.1, §8.6.
package budgetstate

import (
	"context"
	"log/slog"
	"sort"
	"time"
)

// DefaultBatchSize is the number of session ids one pair of store reads
// resolves. It matches the stale-slot reaper's batch, which bounds the
// primary-key array one Postgres read carries.
const DefaultBatchSize = 500

// Sessions is the session-store read the sweep resolves tracked ids through.
// Both reads are cross-tenant primary-key reads, because the trackers key
// their state by session id alone.
type Sessions interface {
	ListStaleTerminalSessions(ctx context.Context, ids []string, terminatedBefore time.Time) ([]string, error)
	ListAbsentSessions(ctx context.Context, ids []string) ([]string, error)
}

// Config is the sweep's dependencies and cadence.
type Config struct {
	// Sessions resolves tracked ids. Required.
	Sessions Sessions
	// Tracked returns the session ids every per-session tracker holds. It is
	// called on each pass, so a tracker built after the sweep is still read.
	// Required.
	Tracked func() []string
	// Forget is the composed per-session forget the terminal pipeline also
	// calls. Required.
	Forget func(sessionID string)
	// Interval is the wait between passes. Required to be positive for Run.
	Interval time.Duration
	// BatchSize is the ids per store read pair. Zero selects
	// DefaultBatchSize.
	BatchSize int
	// Now returns the current time. Nil selects time.Now.
	Now func() time.Time
	// Logger receives the warn line for a failed batch. Nil selects
	// slog.Default.
	Logger *slog.Logger
}

// Sweep is one replica's per-session budget state sweep.
type Sweep struct {
	cfg Config
}

// New returns a Sweep for cfg with its defaults applied.
func New(cfg Config) *Sweep {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultBatchSize
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Sweep{cfg: cfg}
}

// Run runs a pass every Interval until ctx ends. Nil-safe, and a
// non-positive Interval returns at once rather than spinning.
func (s *Sweep) Run(ctx context.Context) {
	if s == nil || s.cfg.Interval <= 0 {
		return
	}
	tick := time.NewTicker(s.cfg.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.Pass(ctx)
		}
	}
}

// Pass runs one sweep and returns how many session ids it forgot. A batch
// whose store read fails keeps every one of its entries for the next pass
// and is logged at warn.
// spec: §11.2, §10.1.1.
func (s *Sweep) Pass(ctx context.Context) int {
	ids := Union(s.cfg.Tracked())
	if len(ids) == 0 {
		return 0
	}
	now := s.cfg.Now()
	forgotten := 0
	for start := 0; start < len(ids); start += s.cfg.BatchSize {
		if ctx.Err() != nil {
			break
		}
		batch := ids[start:min(start+s.cfg.BatchSize, len(ids))]
		ended, err := s.resolveEnded(ctx, batch, now)
		if err != nil {
			s.cfg.Logger.Warn("budget state sweep: resolve sessions failed; keeping the batch",
				"batch_size", len(batch), "error", err)
			continue
		}
		for _, id := range ended {
			s.cfg.Forget(id)
		}
		forgotten += len(ended)
	}
	if forgotten > 0 {
		s.cfg.Logger.Debug("budget state sweep pass", "tracked", len(ids), "forgotten", forgotten)
	}
	return forgotten
}

// resolveEnded returns the ids in batch whose row is terminal or absent. An
// error from either read fails the whole batch, so no entry is forgotten on
// a partial answer. Only ids of the batch are returned, so a store that
// answers with a foreign id cannot make the sweep forget it.
func (s *Sweep) resolveEnded(ctx context.Context, batch []string, now time.Time) ([]string, error) {
	terminal, err := s.cfg.Sessions.ListStaleTerminalSessions(ctx, batch, now)
	if err != nil {
		return nil, err
	}
	absent, err := s.cfg.Sessions.ListAbsentSessions(ctx, batch)
	if err != nil {
		return nil, err
	}
	asked := make(map[string]bool, len(batch))
	for _, id := range batch {
		asked[id] = true
	}
	var ended []string
	for _, id := range Union(terminal, absent) {
		if asked[id] {
			ended = append(ended, id)
		}
	}
	return ended, nil
}

// Union returns the distinct non-empty ids across sets, sorted so a pass
// splits the ids into the same batches each time.
func Union(sets ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, set := range sets {
		for _, id := range set {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
