// SPDX-License-Identifier: MIT

package deadlock

import (
	"context"
	"sort"
	"sync"
	"time"

	session "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
)

// The §16.1 `resolution` label values of
// lenny_delegation_deadlock_resolution_total.
const (
	// ResolutionClientInput: a pending lenny/request_input in the subtree
	// was answered through inReplyTo and the subtree cleared.
	ResolutionClientInput = "client_input"
	// ResolutionChildCancel: a blocked descendant of the root was
	// cancelled and the subtree cleared.
	ResolutionChildCancel = "child_cancel"
	// ResolutionTimeout: the detector's DEADLOCK_TIMEOUT write applied to
	// at least one deepest task.
	ResolutionTimeout = "timeout"
)

// MetricsSink receives the §8.8 deadlock observability signals, each
// labelled by the deadlocked root's pool (§16.1). The gateway metric set
// satisfies it; tests pass a fake.
type MetricsSink interface {
	// IncDelegationDeadlockDetected fires once per newly-detected
	// deadlocked subtree root.
	IncDelegationDeadlockDetected(pool string)
	// ObserveDelegationDeadlockResolution closes out a tracked deadlock
	// with one of the Resolution values, recording the
	// detection-to-resolution latency.
	ObserveDelegationDeadlockResolution(pool, resolution string, seconds float64)
}

// Manager tracks active deadlocked subtrees across sweeps: it stamps a
// fixed detectedAt / willTimeoutAt at first detection, caches the
// deadlock_detected event the await stream serves, fires the detection
// metric once, and decides when a deadlock has timed out or been
// resolved. spec: §8.8. F-8.8.6.
type Manager struct {
	maxWait time.Duration
	metrics MetricsSink

	mu     sync.Mutex
	active map[string]*tracked
}

type tracked struct {
	tenantID      string
	pool          string
	detectedAt    time.Time
	willTimeoutAt time.Time
	event         Event
	deepest       []string
	// answered maps each subtree member to its Node.Answered count at the
	// latest sweep that still detected the deadlock.
	answered map[string]int
}

// Clearance is one tracked subtree the latest sweep no longer detected
// before its willTimeoutAt. Answered holds each member's answered-input
// count at the last sweep that detected the deadlock; the Detector
// compares it with the live counts to classify the clearing cause.
type Clearance struct {
	Root       string
	TenantID   string
	Pool       string
	DetectedAt time.Time
	Answered   map[string]int
}

// TimeoutAction is one subtree the caller must fail: the §8.8 deepest
// blocked tasks are driven to `failed` with reason deadlock_timeout.
// DetectedAt is the tracked entry's first-detection time, from which the
// caller measures the `timeout` resolution once a fail applied.
type TimeoutAction struct {
	Root         string
	TenantID     string
	Pool         string
	DeepestTasks []string
	DetectedAt   time.Time
}

// NewManager returns a Manager with the given per-pool
// maxDeadlockWaitSeconds (DefaultMaxWait when non-positive).
func NewManager(maxWait time.Duration, metrics MetricsSink) *Manager {
	if maxWait <= 0 {
		maxWait = DefaultMaxWait
	}
	return &Manager{maxWait: maxWait, metrics: metrics, active: map[string]*tracked{}}
}

// Observe runs the detector against snap as of now and reconciles the
// active set: it registers new deadlocks (firing the detection metric),
// refreshes the blocked set on still-deadlocked subtrees while holding
// detectedAt / willTimeoutAt fixed, and drops subtrees no longer detected.
// It returns the subtrees whose willTimeoutAt has passed for the caller to
// fail, and the subtrees that cleared before it for the caller to
// classify. Observe records no resolution itself: the caller records
// `timeout` only after a fail applied, and `client_input` or
// `child_cancel` only when it can attribute the clearing to one of them,
// because §16.1 lists no other resolution. A subtree still deadlocked on
// the next sweep is re-detected with a fresh detectedAt.
//
// spec: §8.8 (Subtree deadlock detection), §16.1
func (m *Manager) Observe(snap Snapshot, now time.Time) ([]TimeoutAction, []Clearance) {
	byRoot := make(map[string]Subtree)
	for _, st := range Detect(snap) {
		byRoot[st.Root] = st
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for root, st := range byRoot {
		t, ok := m.active[root]
		if !ok {
			t = &tracked{
				tenantID:      st.TenantID,
				pool:          st.Pool,
				detectedAt:    now,
				willTimeoutAt: now.Add(m.maxWait),
			}
			m.active[root] = t
			if m.metrics != nil {
				m.metrics.IncDelegationDeadlockDetected(st.Pool)
			}
		}
		t.deepest = st.DeepestTasks
		t.answered = make(map[string]int, len(st.Members))
		for _, id := range st.Members {
			t.answered[id] = snap.Nodes[id].Answered
		}
		t.event = Event{
			Type:                  EventType,
			DeadlockedSubtreeRoot: root,
			BlockedRequests:       st.BlockedRequests,
			DetectedAt:            t.detectedAt,
			WillTimeoutAt:         t.willTimeoutAt,
		}
	}

	var timeouts []TimeoutAction
	var cleared []Clearance
	for root, t := range m.active {
		if _, still := byRoot[root]; still {
			if now.Before(t.willTimeoutAt) {
				continue
			}
			timeouts = append(timeouts, TimeoutAction{
				Root: root, TenantID: t.tenantID, Pool: t.pool, DeepestTasks: t.deepest, DetectedAt: t.detectedAt,
			})
			delete(m.active, root)
			continue
		}
		// No longer detected before willTimeoutAt: the caller classifies
		// what cleared it.
		cleared = append(cleared, Clearance{
			Root: root, TenantID: t.tenantID, Pool: t.pool, DetectedAt: t.detectedAt, Answered: t.answered,
		})
		delete(m.active, root)
	}
	sort.Slice(timeouts, func(i, j int) bool { return timeouts[i].Root < timeouts[j].Root })
	sort.Slice(cleared, func(i, j int) bool { return cleared[i].Root < cleared[j].Root })
	return timeouts, cleared
}

// recordResolution closes out a tracked subtree with resolution under its
// root's pool, measured from its first detection.
//
// spec: §8.8 (Subtree deadlock detection), §16.1
func (m *Manager) recordResolution(pool, resolution string, detectedAt, now time.Time) {
	if m.metrics == nil {
		return
	}
	m.metrics.ObserveDelegationDeadlockResolution(pool, resolution, now.Sub(detectedAt).Seconds())
}

// Event returns the cached deadlock_detected event for sessionID when it
// is an active deadlocked subtree root, so the lenny/await_children poll
// loop can surface it. ok is false otherwise.
func (m *Manager) Event(sessionID string) (Event, bool) {
	if m == nil {
		return Event{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.active[sessionID]
	if !ok {
		return Event{}, false
	}
	return t.event, true
}

// PendingSource is the subset of *inputwait.Registry the detector reads:
// the pending rounds that make a task blocked, and the answered-round
// count that attributes a cleared deadlock to `client_input`.
type PendingSource interface {
	PendingDetailsForSession(sessionID string) []inputwait.PendingRequest
	Answered(sessionID string) int
}

// FailFunc drives one deepest blocked task to `failed` with reason
// deadlock_timeout. The gateway wires it to the terminal pipeline. It
// returns true only when the write applied. It returns false when the
// locked row refused the write (the row is terminal, or the task's wait
// left the live registry before the write) and when the store write
// failed; the implementation logs a store error.
//
// spec: §8.8 (Subtree deadlock detection)
type FailFunc func(ctx context.Context, tenantID, sessionID string) bool

// NodeInfo is the live row data the detector reads for one session: its
// state, which classifies whether an awaited child has settled or was
// cancelled, and the pool its row records (PoolRef), which labels the
// deadlock metrics.
type NodeInfo struct {
	State session.State
	Pool  string
}

// NodeLookup resolves a session's live row within a tenant. ok is false
// when the row is gone; the snapshot builder treats a missing row as
// terminal (a reclaimed child has settled).
type NodeLookup func(ctx context.Context, tenantID, sessionID string) (info NodeInfo, ok bool)

// Detector wires the Manager to the live gateway state: it builds a
// snapshot from the await tracker, the request_input registry, and a
// session-state lookup, runs Observe, and applies DEADLOCK_TIMEOUT.
type Detector struct {
	manager *Manager
	tracker *AwaitTracker
	inputs  PendingSource
	lookup  NodeLookup
	fail    FailFunc
}

// NewDetector builds a Detector. lookup resolves a session's state within
// a tenant (used to classify settled awaited children); fail drives a
// task to deadlock_timeout failure.
func NewDetector(manager *Manager, tracker *AwaitTracker, inputs PendingSource,
	lookup NodeLookup, fail FailFunc,
) *Detector {
	return &Detector{manager: manager, tracker: tracker, inputs: inputs, lookup: lookup, fail: fail}
}

// Manager returns the detector's Manager so the await handler can read
// active deadlock events.
func (d *Detector) Manager() *Manager { return d.manager }

func (d *Detector) buildSnapshot(ctx context.Context) Snapshot {
	nodes := map[string]Node{}
	type seed struct{ tenant, id string }
	var queue []seed
	enqueue := func(tenant, id string) {
		if _, ok := nodes[id]; ok {
			return
		}
		// A reclaimed row reads as settled (completed) with no pool.
		state, pool := session.StateCompleted, ""
		if d.lookup != nil {
			if got, ok := d.lookup(ctx, tenant, id); ok {
				state, pool = got.State, got.Pool
			}
		}
		awaiting := d.tracker.AwaitedChildren(id)
		var pend []PendingInput
		answered := 0
		if d.inputs != nil {
			for _, pr := range d.inputs.PendingDetailsForSession(id) {
				pend = append(pend, PendingInput{RequestID: pr.RequestID, BlockedSince: pr.BlockedSince})
			}
			answered = d.inputs.Answered(id)
		}
		nodes[id] = Node{
			SessionID:        id,
			TenantID:         tenant,
			State:            state,
			AwaitingChildIDs: awaiting,
			PendingInputs:    pend,
			Pool:             pool,
			Answered:         answered,
		}
		for _, c := range awaiting {
			queue = append(queue, seed{tenant, c})
		}
	}
	for _, s := range d.tracker.AwaitingSessions() {
		enqueue(s.TenantID, s.SessionID)
	}
	for i := 0; i < len(queue); i++ {
		enqueue(queue[i].tenant, queue[i].id)
	}
	return Snapshot{Nodes: nodes}
}

// RunOnce performs one sweep: build a snapshot, reconcile the active
// deadlock set, fail the deepest blocked tasks of any timed-out subtree,
// and classify every subtree that cleared. It records the `timeout`
// resolution once per action, measured from the action's DetectedAt, and
// only when at least one fail applied. An action whose fails all returned
// false records nothing; when the subtree is still deadlocked the next
// sweep re-detects it. A cleared subtree records `child_cancel` or
// `client_input` per classifyClearance, and nothing when neither applies.
//
// spec: §8.8 (Subtree deadlock detection), §16.1
func (d *Detector) RunOnce(ctx context.Context, now time.Time) {
	snap := d.buildSnapshot(ctx)
	timeouts, cleared := d.manager.Observe(snap, now)
	for _, action := range timeouts {
		if d.failDeepest(ctx, action) {
			d.manager.recordResolution(action.Pool, ResolutionTimeout, action.DetectedAt, now)
		}
	}
	for _, c := range cleared {
		if resolution, ok := d.classifyClearance(ctx, c); ok {
			d.manager.recordResolution(c.Pool, resolution, c.DetectedAt, now)
		}
	}
}

// classifyClearance names the §16.1 resolution of a subtree that cleared
// before its timeout. A member other than the root whose row is now
// cancelled makes it `child_cancel`; otherwise a member with more answered
// lenny/request_input rounds than at the last sweep that detected the
// deadlock makes it `client_input`. ok is false when neither holds, as when
// a blocked child ended on its own, its request_input timed out, or the
// root's await ended; §16.1 lists no resolution for those, and recording
// one of the listed values would misattribute the clearing. A sweep that
// sees both a cancel and an answer cannot order them, and `child_cancel`
// takes precedence.
//
// spec: §8.8 (Subtree deadlock detection), §16.1
func (d *Detector) classifyClearance(ctx context.Context, c Clearance) (string, bool) {
	members := make([]string, 0, len(c.Answered))
	for id := range c.Answered {
		members = append(members, id)
	}
	sort.Strings(members)
	if d.lookup != nil {
		for _, id := range members {
			if id == c.Root {
				continue
			}
			if info, ok := d.lookup(ctx, c.TenantID, id); ok && info.State == session.StateCancelled {
				return ResolutionChildCancel, true
			}
		}
	}
	if d.inputs != nil {
		for _, id := range members {
			if d.inputs.Answered(id) > c.Answered[id] {
				return ResolutionClientInput, true
			}
		}
	}
	return "", false
}

// failDeepest calls fail for every deepest task of action and reports
// whether at least one write applied. Every task is attempted even after
// one applies, because §8.8 fails all of the deepest blocked tasks.
func (d *Detector) failDeepest(ctx context.Context, action TimeoutAction) bool {
	if d.fail == nil {
		return false
	}
	applied := false
	for _, taskID := range action.DeepestTasks {
		if d.fail(ctx, action.TenantID, taskID) {
			applied = true
		}
	}
	return applied
}

// Run sweeps every interval until ctx is cancelled. The gateway starts
// it as a background goroutine.
func (d *Detector) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			d.RunOnce(ctx, now)
		}
	}
}
