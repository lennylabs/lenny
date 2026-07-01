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

// MetricsSink receives the §8.8 deadlock observability signals. The
// gateway metric set satisfies it; tests pass a fake.
type MetricsSink interface {
	// IncDelegationDeadlockDetected fires once per newly-detected
	// deadlocked subtree root.
	IncDelegationDeadlockDetected(tenantID string)
	// ObserveDelegationDeadlockResolution closes out a tracked deadlock
	// with `resolved` (the root broke it before willTimeoutAt) or
	// `timeout` (the detector applied DEADLOCK_TIMEOUT), recording the
	// detection-to-resolution latency.
	ObserveDelegationDeadlockResolution(resolution string, seconds float64)
}

// Manager tracks active deadlocked subtrees across sweeps: it stamps a
// fixed detectedAt / willTimeoutAt at first detection, caches the
// deadlock_detected event the await stream serves, fires the detection
// metric once, and decides when a deadlock has timed out or been
// resolved. spec: §8.8 lines 981-997. F-8.8.6.
type Manager struct {
	maxWait time.Duration
	metrics MetricsSink

	mu     sync.Mutex
	active map[string]*tracked
}

type tracked struct {
	tenantID      string
	detectedAt    time.Time
	willTimeoutAt time.Time
	event         Event
	deepest       []string
}

// TimeoutAction is one subtree the caller must fail: the §8.8 deepest
// blocked tasks are driven to `failed` with reason deadlock_timeout.
type TimeoutAction struct {
	Root         string
	TenantID     string
	DeepestTasks []string
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
// detectedAt / willTimeoutAt fixed, resolves subtrees no longer
// detected, and returns the subtrees whose willTimeoutAt has passed for
// the caller to fail.
func (m *Manager) Observe(snap Snapshot, now time.Time) []TimeoutAction {
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
				detectedAt:    now,
				willTimeoutAt: now.Add(m.maxWait),
			}
			m.active[root] = t
			if m.metrics != nil {
				m.metrics.IncDelegationDeadlockDetected(st.TenantID)
			}
		}
		t.deepest = st.DeepestTasks
		t.event = Event{
			Type:                  EventType,
			DeadlockedSubtreeRoot: root,
			BlockedRequests:       st.BlockedRequests,
			DetectedAt:            t.detectedAt,
			WillTimeoutAt:         t.willTimeoutAt,
		}
	}

	var timeouts []TimeoutAction
	for root, t := range m.active {
		if _, still := byRoot[root]; still {
			if now.Before(t.willTimeoutAt) {
				continue
			}
			timeouts = append(timeouts, TimeoutAction{Root: root, TenantID: t.tenantID, DeepestTasks: t.deepest})
			if m.metrics != nil {
				m.metrics.ObserveDelegationDeadlockResolution("timeout", now.Sub(t.detectedAt).Seconds())
			}
			delete(m.active, root)
			continue
		}
		// No longer detected before willTimeoutAt — the root resolved it.
		if m.metrics != nil {
			m.metrics.ObserveDelegationDeadlockResolution("resolved", now.Sub(t.detectedAt).Seconds())
		}
		delete(m.active, root)
	}
	sort.Slice(timeouts, func(i, j int) bool { return timeouts[i].Root < timeouts[j].Root })
	return timeouts
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

// PendingSource is the subset of *inputwait.Registry the detector reads.
type PendingSource interface {
	PendingDetailsForSession(sessionID string) []inputwait.PendingRequest
}

// FailFunc drives one deepest blocked task to `failed` with reason
// deadlock_timeout. The gateway wires it to the terminal pipeline.
type FailFunc func(ctx context.Context, tenantID, sessionID string)

// StateLookup resolves a session's live state within a tenant. ok is
// false when the row is gone; the snapshot builder treats a missing row
// as terminal (a reclaimed child has settled). The detector reads the
// state only to classify whether an awaited child has settled.
type StateLookup func(ctx context.Context, tenantID, sessionID string) (state session.State, ok bool)

// Detector wires the Manager to the live gateway state: it builds a
// snapshot from the await tracker, the request_input registry, and a
// session-state lookup, runs Observe, and applies DEADLOCK_TIMEOUT.
type Detector struct {
	manager *Manager
	tracker *AwaitTracker
	inputs  PendingSource
	lookup  StateLookup
	fail    FailFunc
}

// NewDetector builds a Detector. lookup resolves a session's state within
// a tenant (used to classify settled awaited children); fail drives a
// task to deadlock_timeout failure.
func NewDetector(manager *Manager, tracker *AwaitTracker, inputs PendingSource,
	lookup StateLookup, fail FailFunc,
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
		state := session.StateCompleted // default for a reclaimed (settled) row
		if d.lookup != nil {
			if s, ok := d.lookup(ctx, tenant, id); ok {
				state = s
			}
		}
		awaiting := d.tracker.AwaitedChildren(id)
		var pend []PendingInput
		if d.inputs != nil {
			for _, pr := range d.inputs.PendingDetailsForSession(id) {
				pend = append(pend, PendingInput{RequestID: pr.RequestID, BlockedSince: pr.BlockedSince})
			}
		}
		nodes[id] = Node{
			SessionID:        id,
			TenantID:         tenant,
			State:            state,
			AwaitingChildIDs: awaiting,
			PendingInputs:    pend,
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
// deadlock set, and fail the deepest blocked tasks of any timed-out
// subtree.
func (d *Detector) RunOnce(ctx context.Context, now time.Time) {
	snap := d.buildSnapshot(ctx)
	for _, action := range d.manager.Observe(snap, now) {
		for _, taskID := range action.DeepestTasks {
			if d.fail != nil {
				d.fail(ctx, action.TenantID, taskID)
			}
		}
	}
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
