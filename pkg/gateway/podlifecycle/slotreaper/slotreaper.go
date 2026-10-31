// SPDX-License-Identifier: MIT

// Package slotreaper implements the gateway's stale-slot reaper: the
// leader-run loop that releases a concurrent-workspace slot set member whose
// release failed or never ran, once the member's session row has been
// terminal for longer than claimOrphanTimeout.
//
// A pass lists the pods a non-terminal session row names, reads each pod's
// slot set, drops the leaked: members (a leaked slot stays counted until the
// pod drains), and resolves the plain members against the session store in
// batches. A member whose row is terminal with a terminated_at older than
// the terminal age is eligible. Every activity that can still act on a
// session's slot after its terminal write has ended by then, which is why
// the terminal age must equal claimOrphanTimeout and meet the gateway's
// startup gate. For each pod with an eligible member the reaper calls the
// releaser, which tears the slot down with an unconditional Shutdown and
// removes the member only on a clean exit.
//
// The reaper shares no lock with the release paths. The keyed removal
// serializes them: a late release and the reaper each remove a member at
// most once, and the reaper never empties a set, so it never runs the
// occupancy-zero disposition and writes no claim.
//
// spec: §5.2 (Slot assignment atomicity).
package slotreaper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// The operator-tunable defaults of the gateway's --slot-reaper-interval,
// --slot-reaper-rate, and --slot-reaper-terminal-age flags. DefaultTerminalAge
// is the claimOrphanTimeout default the controller's --claim-orphan-timeout
// shares.
const (
	DefaultInterval    = 60 * time.Second
	DefaultRate        = 50
	DefaultTerminalAge = 15 * time.Minute
)

// DefaultBatchSize is the number of identifiers one ListStaleTerminalSessions
// call resolves. It bounds the size of the primary-key array a Postgres read
// carries, so a pass over about 12,000 members (the residue a 60-second
// Redis outage leaves at Tier 3) issues about 24 reads.
const DefaultBatchSize = 500

// leakedPrefix marks a member that keeps a leaked slot counted. The reaper
// never releases one.
const leakedPrefix = "leaked:"

// PodLister lists the pods a non-terminal per-slot session row names.
type PodLister interface {
	ListPodsWithActiveSlots(ctx context.Context) ([]string, error)
}

// MemberReader reads every member of a pod's slot set.
type MemberReader interface {
	Members(ctx context.Context, podID string) ([]string, error)
}

// StaleResolver returns the identifiers among ids whose row is terminal
// with a terminated_at earlier than terminatedBefore.
type StaleResolver interface {
	ListStaleTerminalSessions(ctx context.Context, ids []string, terminatedBefore time.Time) ([]string, error)
}

// Releaser tears the named stale slots of one pod down and removes their
// members, calling wait once before each identifier. Its error may join
// several failures; a failure with a SessionID method names the one member
// it kept, and the reaper logs it against that session.
type Releaser interface {
	ReapStaleSlots(ctx context.Context, sandboxName string, sessionIDs []string, wait func(context.Context) error) (int, error)
}

// ReservationReclaimer deletes the abandoned §12.4 slot reservation records:
// those recorded before cutoff whose session row is absent or has been
// terminal since before cutoff.
type ReservationReclaimer interface {
	ReclaimSlotReservations(ctx context.Context, cutoff time.Time) (int, error)
}

// Limiter paces releases. *rate.Limiter satisfies it.
type Limiter interface {
	Wait(ctx context.Context) error
}

// Config configures a Reaper. Pods, Members, Sessions, and Releaser are
// required; Rate, Interval, and TerminalAge must be positive.
type Config struct {
	Pods     PodLister
	Members  MemberReader
	Sessions StaleResolver
	Releaser Releaser
	// Reservations, when set, reclaims the abandoned slot reservation
	// records once per pass, with the cutoff TerminalAge before now. A
	// reservation record whose create ended before its session row was
	// persisted, or whose session ended without a release, is deleted only
	// here, so an unreclaimed record over-counts its pod's §12.4 gate.
	// spec: §12.4 (Redis failure behavior), §4.6.1 (Orphaned SandboxClaim
	// detection).
	Reservations ReservationReclaimer
	// Rate is the most members released per second, and the most pods
	// whose release is in flight at once.
	Rate int
	// Interval is the wait between the end of one pass and the start of
	// the next.
	Interval time.Duration
	// TerminalAge is the gateway's copy of claimOrphanTimeout: a member is
	// eligible once its row has been terminal for longer than this.
	TerminalAge time.Duration
	// BatchSize is the identifiers per ListStaleTerminalSessions call. Zero
	// selects DefaultBatchSize.
	BatchSize int
	// Limiter overrides the token bucket New builds from Rate, for tests.
	Limiter Limiter
	// Now and After are the injected clock. Nil selects time.Now and
	// time.After.
	Now   func() time.Time
	After func(time.Duration) <-chan time.Time
	// Logger receives the pass summary and the per-pod errors. Nil selects
	// slog.Default().
	Logger *slog.Logger
}

// Reaper is the stale-slot reaper. Construct it with New.
type Reaper struct {
	cfg     Config
	limiter Limiter
}

// New validates cfg and returns a Reaper. The default limiter is a token
// bucket refilling Rate tokens per second with a burst of one, so releases
// are spread across each second rather than issued in a burst.
func New(cfg Config) (*Reaper, error) {
	if cfg.Pods == nil || cfg.Members == nil || cfg.Sessions == nil || cfg.Releaser == nil {
		return nil, errors.New("slotreaper: the pod lister, member reader, session resolver, and releaser are required")
	}
	if cfg.Rate <= 0 {
		return nil, fmt.Errorf("slotreaper: rate must be positive, got %d", cfg.Rate)
	}
	if cfg.Interval <= 0 || cfg.TerminalAge <= 0 {
		return nil, fmt.Errorf("slotreaper: interval (%s) and terminal age (%s) must be positive", cfg.Interval, cfg.TerminalAge)
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultBatchSize
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	lim := cfg.Limiter
	if lim == nil {
		lim = rate.NewLimiter(rate.Limit(cfg.Rate), 1)
	}
	return &Reaper{cfg: cfg, limiter: lim}, nil
}

// Run runs a pass, waits Interval, and repeats until ctx ends.
// spec: §5.2 (Slot assignment atomicity).
func (r *Reaper) Run(ctx context.Context) {
	for {
		r.Pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-r.cfg.After(r.cfg.Interval):
		}
	}
}

// PassStats is what one pass did.
type PassStats struct {
	// Pods is the number of pods listed.
	Pods int
	// Eligible is the number of members found stale.
	Eligible int
	// Released is the number of members the releaser removed.
	Released int
	// Reservations is the number of abandoned slot reservation records the
	// pass deleted.
	Reservations int
}

// Kept is the number of eligible members the pass left in their sets.
func (p PassStats) Kept() int { return p.Eligible - p.Released }

// Pass runs one pass and returns what it did. An error on one pod, or on
// one batch, keeps the members it affects for the next pass and does not
// stop the pass.
func (r *Reaper) Pass(ctx context.Context) PassStats {
	var stats PassStats
	stats.Reservations = r.reclaimReservations(ctx)
	pods, err := r.cfg.Pods.ListPodsWithActiveSlots(ctx)
	if err != nil {
		r.cfg.Logger.Warn("stale-slot reaper: list pods failed", "error", err)
		return stats
	}
	stats.Pods = len(pods)
	members := r.readMembers(ctx, pods)
	eligible := r.resolveEligible(ctx, members)
	for _, ids := range eligible {
		stats.Eligible += len(ids)
	}
	stats.Released = r.release(ctx, pods, eligible)
	// A pass with nothing eligible logs at debug level, so a quiet leader
	// does not write a line every interval.
	level := slog.LevelDebug
	if stats.Eligible > 0 || stats.Reservations > 0 {
		level = slog.LevelInfo
	}
	r.cfg.Logger.Log(ctx, level, "stale-slot reaper pass",
		"pods", stats.Pods, "eligible", stats.Eligible, "released", stats.Released, "kept", stats.Kept(),
		"reservations_reclaimed", stats.Reservations)
	return stats
}

// reclaimReservations deletes the abandoned slot reservation records with
// the cutoff TerminalAge before now, and returns how many it deleted. A
// failure is logged and leaves the records for the next pass.
// spec: §12.4 (Redis failure behavior), §4.6.1 (Orphaned SandboxClaim
// detection).
func (r *Reaper) reclaimReservations(ctx context.Context) int {
	if r.cfg.Reservations == nil {
		return 0
	}
	n, err := r.cfg.Reservations.ReclaimSlotReservations(ctx, r.cfg.Now().Add(-r.cfg.TerminalAge))
	if err != nil {
		r.cfg.Logger.Warn("stale-slot reaper: reclaim slot reservations failed", "error", err)
		return 0
	}
	return n
}

// readMembers reads each pod's plain members. A pod whose read fails is
// skipped for this pass.
func (r *Reaper) readMembers(ctx context.Context, pods []string) map[string][]string {
	out := make(map[string][]string, len(pods))
	for _, pod := range pods {
		if ctx.Err() != nil {
			return out
		}
		all, err := r.cfg.Members.Members(ctx, pod)
		if err != nil {
			r.cfg.Logger.Warn("stale-slot reaper: read slot set failed", "pod", pod, "error", err)
			continue
		}
		for _, m := range all {
			if m != "" && !strings.HasPrefix(m, leakedPrefix) {
				out[pod] = append(out[pod], m)
			}
		}
	}
	return out
}

// resolveEligible resolves the plain members in batches and returns, per
// pod, the members whose row is stale. A session ID can be a member of more
// than one pod's set (a release that failed on one pod before the session's
// later release targeted another), so each ID maps to every pod holding it
// and a stale ID is eligible on each of them in the same pass. Each ID is
// sent to the store once. A batch whose read fails makes none of its
// members eligible.
// spec: §5.2 (Slot assignment atomicity).
func (r *Reaper) resolveEligible(ctx context.Context, members map[string][]string) map[string][]string {
	owners, ids := indexOwners(members)
	cutoff := r.cfg.Now().Add(-r.cfg.TerminalAge)
	eligible := map[string][]string{}
	for start := 0; start < len(ids); start += r.cfg.BatchSize {
		if ctx.Err() != nil {
			return eligible
		}
		batch := ids[start:min(start+r.cfg.BatchSize, len(ids))]
		stale, err := r.cfg.Sessions.ListStaleTerminalSessions(ctx, batch, cutoff)
		if err != nil {
			r.cfg.Logger.Warn("stale-slot reaper: resolve members failed",
				"session_ids", batch, "error", err)
			continue
		}
		for _, id := range stale {
			for _, pod := range owners[id] {
				eligible[pod] = append(eligible[pod], id)
			}
			// A store that returned an ID twice adds it once.
			delete(owners, id)
		}
	}
	return eligible
}

// indexOwners maps each member ID to every pod whose set holds it, and
// returns the distinct IDs in the order first seen. A pod's set holds an ID
// at most once, so each owner list has no duplicate pod.
func indexOwners(members map[string][]string) (map[string][]string, []string) {
	owners := map[string][]string{}
	var ids []string
	for pod, ms := range members {
		for _, m := range ms {
			if _, seen := owners[m]; !seen {
				ids = append(ids, m)
			}
			owners[m] = append(owners[m], pod)
		}
	}
	return owners, ids
}

// release calls the releaser for each pod with an eligible member, in the
// listing's order, with at most Rate pods in flight. It starts no call once
// ctx has ended.
func (r *Reaper) release(ctx context.Context, pods []string, eligible map[string][]string) int {
	sem := make(chan struct{}, r.cfg.Rate)
	var (
		mu       sync.Mutex
		released int
		wg       sync.WaitGroup
	)
	for _, pod := range pods {
		ids := eligible[pod]
		if len(ids) == 0 {
			continue
		}
		select {
		case <-ctx.Done():
		case sem <- struct{}{}:
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(pod string, ids []string) {
			defer wg.Done()
			defer func() { <-sem }()
			n, err := r.cfg.Releaser.ReapStaleSlots(ctx, pod, ids, r.limiter.Wait)
			r.logReleaseErrors(pod, ids, n, err)
			mu.Lock()
			released += n
			mu.Unlock()
		}(pod, ids)
	}
	wg.Wait()
	return released
}

// sessionScoped is a release failure that names the one session it kept.
type sessionScoped interface {
	error
	SessionID() string
}

// logReleaseErrors logs each failure a release returned. A failure that
// names its session is logged against that session; any other failure (a
// Sandbox read, dial, negotiation, or limiter error) applies to every
// identifier offered for the pod and is logged against the whole list.
func (r *Reaper) logReleaseErrors(pod string, ids []string, released int, err error) {
	if err == nil {
		return
	}
	errs := []error{err}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errs = joined.Unwrap()
	}
	for _, e := range errs {
		var scoped sessionScoped
		if errors.As(e, &scoped) {
			r.cfg.Logger.Warn("stale-slot reaper: release failed",
				"pod", pod, "session_id", scoped.SessionID(), "error", e)
			continue
		}
		r.cfg.Logger.Warn("stale-slot reaper: release failed",
			"pod", pod, "session_ids", ids, "released", released, "error", e)
	}
}
