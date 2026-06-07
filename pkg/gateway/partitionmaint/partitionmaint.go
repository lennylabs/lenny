// SPDX-License-Identifier: MIT

// Package partitionmaint implements the §16.4 line 378 EventStore
// partition lifecycle: a background maintainer that, per natively
// range-partitioned table, creates the current and a few future time
// partitions ahead of the write path and drops partitions whose entire
// range has aged past the retention window. Dropping a whole partition
// (DROP TABLE on the child) reclaims storage without the row-by-row
// vacuum cost of a DELETE sweep, which is the property §16.4 line 378
// and §12.2 line 131 call for.
//
// The package separates the date arithmetic (Plan, pure and exhaustively
// testable) from the DDL execution (Driver, a thin pgx wrapper). The
// maintainer parses each partition's time bounds from its name using the
// fixed naming scheme this package owns, so the Driver never has to parse
// Postgres partition-bound expressions.
//
// audit_log is deliberately out of scope: §12.8 line 815 requires a
// single-column foreign key onto audit_log.id, which Postgres forbids on
// a table range-partitioned by created_at, so the audit chain stays on
// the §16.4 DELETE-based pruner (see auditretention). The DropGuard seam
// exists so an audit table could later plug the §16.4 SIEM delivery
// guard into the drop decision if that spec tension is resolved.
//
// spec: §16.4 line 378 (EventStore partitioning, retention windows, the
// partition-dropping background job); §12.2 line 131 (declarative
// PARTITION BY RANGE (created_at) for parallel writes and fast
// detach/drop of old partitions).
package partitionmaint

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"
)

// Retention windows from §16.4 line 378 (mirrored in §17.8 line 877 for
// session logs). audit_log's 365-day window is included for completeness;
// this maintainer does not manage audit_log (see the package comment).
const (
	SessionLogRetention   = 30 * 24 * time.Hour
	StreamCursorRetention = 7 * 24 * time.Hour
	AuditRetention        = 365 * 24 * time.Hour
)

// DefaultInterval is the maintainer's sweep cadence. Partition creation
// and dropping is a daily-granularity obligation, so an hourly sweep
// keeps partitions provisioned ahead of the write path and reclaims
// expired ones promptly without per-cycle DDL churn.
const DefaultInterval = time.Hour

// MinInterval floors the cadence so a misconfigured interval cannot turn
// the leader-elected sweep into a DDL busy-loop.
const MinInterval = time.Minute

// DefaultAhead is how many future periods (beyond the current one) the
// maintainer keeps provisioned, so a write never races partition
// creation across a period boundary or a missed sweep.
const DefaultAhead = 2

// Granularity is the partition period width.
type Granularity string

const (
	Daily   Granularity = "daily"
	Monthly Granularity = "monthly"
)

// Spec describes one partitioned EventStore table the maintainer owns.
type Spec struct {
	// Table is the partitioned parent relation name.
	Table string
	// Granularity is the period width of each partition.
	Granularity Granularity
	// Retention is the §16.4 window: a partition whose upper bound is at
	// or before now-Retention is entirely expired and is dropped.
	Retention time.Duration
	// Ahead is the number of future periods to pre-create beyond the
	// current one. A zero value uses DefaultAhead.
	Ahead int
}

// ahead returns the configured look-ahead, defaulting when unset.
func (s Spec) ahead() int {
	if s.Ahead <= 0 {
		return DefaultAhead
	}
	return s.Ahead
}

// Bounds is a half-open partition range [Lower, Upper) and the child
// relation name the maintainer assigns it.
type Bounds struct {
	Child string
	Lower time.Time
	Upper time.Time
}

// periodStart truncates t to the start of its period in UTC.
func periodStart(t time.Time, g Granularity) time.Time {
	u := t.UTC()
	switch g {
	case Monthly:
		return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	}
}

// advance returns start shifted by n whole periods.
func advance(start time.Time, g Granularity, n int) time.Time {
	if g == Monthly {
		return start.AddDate(0, n, 0)
	}
	return start.AddDate(0, 0, n)
}

func layoutFor(g Granularity) string {
	if g == Monthly {
		return "200601"
	}
	return "20060102"
}

// childName returns the partition relation name for a period start.
func childName(table string, start time.Time, g Granularity) string {
	return fmt.Sprintf("%s_p%s", table, start.UTC().Format(layoutFor(g)))
}

// childNamePattern matches the dated partitions this package creates and
// is reused to validate names before they reach a DDL statement
// (identifiers cannot be parameterized). The DEFAULT partition
// (<table>_default) does not match, so it is never a drop candidate.
var childNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*_p[0-9]{6,8}$`)

// parseChild recovers the period start encoded in a dated partition
// name. ok is false for the DEFAULT partition or any name that does not
// follow this package's scheme.
func parseChild(table string, child string, g Granularity) (start time.Time, ok bool) {
	prefix := table + "_p"
	if len(child) <= len(prefix) || child[:len(prefix)] != prefix {
		return time.Time{}, false
	}
	t, err := time.Parse(layoutFor(g), child[len(prefix):])
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// Plan computes the partitions to create and drop for a spec at now,
// given the set of existing child partition names. It is pure: callers
// drive the actual DDL through a Driver and may interpose a DropGuard
// before each drop.
//
// Creates cover the current period and spec.ahead() future periods that
// do not already exist. Drops are existing dated partitions whose entire
// range (upper bound) is at or before the now-Retention cutoff; the
// DEFAULT partition and any unrecognized name are never dropped.
func Plan(spec Spec, now time.Time, existing []string) (creates []Bounds, drops []string) {
	have := make(map[string]bool, len(existing))
	for _, c := range existing {
		have[c] = true
	}
	base := periodStart(now, spec.Granularity)
	for i := 0; i <= spec.ahead(); i++ {
		start := advance(base, spec.Granularity, i)
		name := childName(spec.Table, start, spec.Granularity)
		if !have[name] {
			creates = append(creates, Bounds{
				Child: name,
				Lower: start,
				Upper: advance(start, spec.Granularity, 1),
			})
		}
	}
	if spec.Retention > 0 {
		cutoff := now.UTC().Add(-spec.Retention)
		for _, child := range existing {
			start, ok := parseChild(spec.Table, child, spec.Granularity)
			if !ok {
				continue
			}
			upper := advance(start, spec.Granularity, 1)
			// The whole partition is expired only when its exclusive upper
			// bound is at or before the cutoff, so a partition still
			// holding any in-window row is never dropped.
			if !upper.After(cutoff) {
				drops = append(drops, child)
			}
		}
	}
	return creates, drops
}

// Driver executes the partition DDL. *PGDriver is the production
// implementation; tests use a fake.
type Driver interface {
	// ListPartitions returns the child relation names of the partitioned
	// parent, including the DEFAULT partition.
	ListPartitions(ctx context.Context, parent string) ([]string, error)
	// CreatePartition attaches a child range partition [lower, upper) to
	// the parent. It is idempotent (CREATE ... IF NOT EXISTS).
	CreatePartition(ctx context.Context, parent, child string, lower, upper time.Time) error
	// DropPartition removes a child partition and its rows.
	DropPartition(ctx context.Context, child string) error
}

// DropGuard can veto a partition drop. The §16.4 SIEM delivery guard for
// audit partitions is the motivating case: a partition past its TTL but
// holding events the SIEM forwarder has not acknowledged must be held,
// not dropped. session_logs and stream_cursors run with a nil guard.
type DropGuard interface {
	// HoldDrop reports whether the partition must be held instead of
	// dropped. A non-nil error holds the partition conservatively.
	HoldDrop(ctx context.Context, parent, child string) (bool, error)
}

// Result reports one Tick's outcome for one spec.
type Result struct {
	Table   string
	Created []string
	Dropped []string
	Held    []string
}

// Maintainer runs the §16.4 partition lifecycle across a set of specs.
type Maintainer struct {
	driver   Driver
	specs    []Spec
	guards   map[string]DropGuard
	interval time.Duration
	clock    func() time.Time

	mu sync.Mutex
}

// Options configures a Maintainer. A zero field selects its default.
type Options struct {
	// Interval overrides DefaultInterval; values below MinInterval are
	// raised to the floor.
	Interval time.Duration
	// Clock overrides time.Now (UTC).
	Clock func() time.Time
	// Guards maps a table name to a DropGuard. Tables without an entry
	// drop expired partitions unconditionally.
	Guards map[string]DropGuard
}

// New returns a Maintainer over the given specs.
func New(driver Driver, specs []Spec, opts Options) *Maintainer {
	m := &Maintainer{
		driver:   driver,
		specs:    specs,
		guards:   opts.Guards,
		interval: clampInterval(opts.Interval),
		clock:    opts.Clock,
	}
	if m.clock == nil {
		m.clock = func() time.Time { return time.Now().UTC() }
	}
	return m
}

func clampInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return DefaultInterval
	}
	if d < MinInterval {
		return MinInterval
	}
	return d
}

// Interval is the resolved sweep cadence.
func (m *Maintainer) Interval() time.Duration { return m.interval }

// Tick runs one maintenance pass across every spec at now and returns a
// per-table Result. Creation precedes dropping so the write path always
// has a current partition even if a later drop fails. A per-spec error
// stops that spec and is returned with the results gathered so far; the
// caller decides whether a transient DDL error should abort the sweep.
//
// spec: §16.4 line 378.
func (m *Maintainer) Tick(ctx context.Context, now time.Time) ([]Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	results := make([]Result, 0, len(m.specs))
	for _, spec := range m.specs {
		res := Result{Table: spec.Table}
		existing, err := m.driver.ListPartitions(ctx, spec.Table)
		if err != nil {
			return append(results, res), fmt.Errorf("partitionmaint: list %s: %w", spec.Table, err)
		}
		creates, drops := Plan(spec, now, existing)
		for _, b := range creates {
			if err := m.driver.CreatePartition(ctx, spec.Table, b.Child, b.Lower, b.Upper); err != nil {
				return append(results, res), fmt.Errorf("partitionmaint: create %s: %w", b.Child, err)
			}
			res.Created = append(res.Created, b.Child)
		}
		guard := m.guards[spec.Table]
		for _, child := range drops {
			if guard != nil {
				hold, gerr := guard.HoldDrop(ctx, spec.Table, child)
				if gerr != nil || hold {
					// A guard error holds the partition conservatively: a
					// transient guard failure must never cause a drop the
					// §16.4 SIEM guard would have blocked.
					res.Held = append(res.Held, child)
					continue
				}
			}
			if err := m.driver.DropPartition(ctx, child); err != nil {
				return append(results, res), fmt.Errorf("partitionmaint: drop %s: %w", child, err)
			}
			res.Dropped = append(res.Dropped, child)
		}
		results = append(results, res)
	}
	return results, nil
}

// Run drives Tick on the configured interval until ctx is done. onTick,
// when non-nil, receives each pass's results and error. Production runs
// this under the §10.1 / §12.5 gateway-leader lease so exactly one
// replica owns the DDL.
func (m *Maintainer) Run(ctx context.Context, onTick func([]Result, error)) {
	// Run an immediate pass so a freshly-started leader provisions the
	// current partition without waiting a full interval.
	if res, err := m.Tick(ctx, m.clock()); onTick != nil {
		onTick(res, err)
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			res, err := m.Tick(ctx, m.clock())
			if onTick != nil {
				onTick(res, err)
			}
		}
	}
}
