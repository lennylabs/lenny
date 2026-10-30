// SPDX-License-Identifier: MIT

package treerecovery_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treerecovery"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// --- fakes -----------------------------------------------------------

type fakeLister struct {
	rows map[string][]sessionstore.Session
	err  error
}

func (f *fakeLister) ListByRoot(_ context.Context, _, rootID string) ([]sessionstore.Session, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[rootID], nil
}

type reattachRecorder struct {
	started []string
	order   []string
	failOn  map[string]error
}

// StartNode records the node the traversal drove.
func (r *reattachRecorder) StartNode(_ context.Context, node sessionstore.Session) {
	r.started = append(r.started, node.ID)
}

func (r *reattachRecorder) ReattachNode(_ context.Context, node sessionstore.Session) error {
	r.order = append(r.order, node.ID)
	if err, ok := r.failOn[node.ID]; ok {
		return err
	}
	return nil
}

type failedNode struct {
	id     string
	reason string
}

type terminalRecorder struct{ failed []failedNode }

func (t *terminalRecorder) FailNode(_ context.Context, node sessionstore.Session, reason string) {
	t.failed = append(t.failed, failedNode{id: node.ID, reason: reason})
}

type durationSample struct {
	pool    string
	outcome string
	seconds float64
}

type timeoutSample struct {
	pool string
	kind string
}

type metricsRecorder struct {
	durations []durationSample
	timeouts  []timeoutSample
}

func (m *metricsRecorder) ObserveTreeRecoveryDuration(pool, outcome string, seconds float64) {
	m.durations = append(m.durations, durationSample{pool, outcome, seconds})
}

func (m *metricsRecorder) IncTreeRecoveryTimeout(pool, kind string) {
	m.timeouts = append(m.timeouts, timeoutSample{pool, kind})
}

// stepClock advances by step on each call so a test can drive
// recovery.Recover's wall-clock deadlines deterministically. A zero
// step pins the clock so no deadline is ever exceeded.
type stepClock struct {
	base time.Time
	step time.Duration
	n    int
}

func (c *stepClock) now() time.Time {
	t := c.base.Add(time.Duration(c.n) * c.step)
	c.n++
	return t
}

func node(id, parent, root string, depth uint32, state session.State) sessionstore.Session {
	return sessionstore.Session{
		ID:              id,
		TenantID:        "acme",
		ParentSessionID: parent,
		RootSessionID:   root,
		DelegationDepth: depth,
		State:           state,
	}
}

// tree returns a three-level tree: root r (depth 0) → child c1 (depth 1)
// → grandchildren g1, g2 (depth 2). All descendants are running.
func tree(rootPool string) []sessionstore.Session {
	r := node("r", "", "r", 0, session.StateRunning)
	r.PoolRef = rootPool
	return []sessionstore.Session{
		r,
		node("c1", "r", "r", 1, session.StateRunning),
		node("g1", "c1", "r", 2, session.StateRunning),
		node("g2", "c1", "r", 2, session.StateRunning),
	}
}

func newOrch(t *testing.T, cfg treerecovery.Config) *treerecovery.Orchestrator {
	t.Helper()
	o := treerecovery.New(cfg)
	if o == nil {
		t.Fatal("New returned nil for a fully-wired config")
	}
	return o
}

// --- tests -----------------------------------------------------------

// spec: §8.10 — recovery is bottom-up (leaves first), and
// within a level deterministically by session id.
func TestRecoverTreeBottomUpOrder_spec_8_10_1016(t *testing.T) {
	lister := &fakeLister{rows: map[string][]sessionstore.Session{"r": tree("gvisor-default")}}
	reatt := &reattachRecorder{}
	term := &terminalRecorder{}
	mx := &metricsRecorder{}
	clk := &stepClock{base: time.Unix(0, 0)} // step 0: no deadline fires

	o := newOrch(t, treerecovery.Config{
		Lister: lister, Reattacher: reatt, Terminal: term, Metrics: mx,
		LevelTimeout: time.Hour, TreeTimeout: time.Hour, Clock: clk.now,
	})
	got, err := o.RecoverTree(context.Background(), "acme", "r")
	if err != nil {
		t.Fatalf("RecoverTree: %v", err)
	}
	// Root excluded; three descendants recovered.
	if got.Recoverable != 3 || got.Recovered != 3 || got.Failed != 0 {
		t.Fatalf("summary = %+v, want {3 3 0}", got)
	}
	// Deepest level (g1, g2 sorted by id) before c1.
	want := []string{"g1", "g2", "c1"}
	if len(reatt.order) != len(want) {
		t.Fatalf("reattach order = %v, want %v", reatt.order, want)
	}
	for i := range want {
		if reatt.order[i] != want[i] {
			t.Fatalf("reattach order = %v, want %v", reatt.order, want)
		}
	}
	if len(term.failed) != 0 {
		t.Fatalf("no node should fail, got %v", term.failed)
	}
	if len(mx.durations) != 1 || mx.durations[0].outcome != "full_success" {
		t.Fatalf("durations = %+v, want one full_success", mx.durations)
	}
	if mx.durations[0].pool != "gvisor-default" {
		t.Fatalf("pool label = %q, want gvisor-default", mx.durations[0].pool)
	}
	if len(mx.timeouts) != 0 {
		t.Fatalf("no timeout counter expected, got %v", mx.timeouts)
	}
}

// spec: §8.10 — the root and already-terminal nodes are not
// recovered; the recovery acts only on the in-flight descendants.
func TestRecoverTreeSkipsRootAndTerminal_spec_8_10_1014(t *testing.T) {
	rows := []sessionstore.Session{
		node("r", "", "r", 0, session.StateRunning),
		node("c1", "r", "r", 1, session.StateCompleted), // settled — skip
		node("c2", "r", "r", 1, session.StateRunning),   // recover
		node("c3", "r", "r", 1, session.StateFailed),    // terminal — skip
	}
	lister := &fakeLister{rows: map[string][]sessionstore.Session{"r": rows}}
	reatt := &reattachRecorder{}
	o := newOrch(t, treerecovery.Config{
		Lister: lister, Reattacher: reatt, Terminal: &terminalRecorder{},
		LevelTimeout: time.Hour, TreeTimeout: time.Hour,
		Clock: (&stepClock{base: time.Unix(0, 0)}).now,
	})
	got, err := o.RecoverTree(context.Background(), "acme", "r")
	if err != nil {
		t.Fatalf("RecoverTree: %v", err)
	}
	if got.Recoverable != 1 || got.Recovered != 1 {
		t.Fatalf("summary = %+v, want one recovered node", got)
	}
	if len(reatt.order) != 1 || reatt.order[0] != "c2" {
		t.Fatalf("reattach order = %v, want [c2]", reatt.order)
	}
}

// spec: §8.10 — the Recoverable predicate scopes recovery to
// genuinely orphaned nodes, so a descendant still live on its pod is not
// torn down when the root resumes for its own reasons.
func TestRecoverTreeRecoverablePredicate(t *testing.T) {
	live := map[string]bool{"g1": true} // g1 still has a live pod
	lister := &fakeLister{rows: map[string][]sessionstore.Session{"r": tree("p")}}
	reatt := &reattachRecorder{}
	o := newOrch(t, treerecovery.Config{
		Lister: lister, Reattacher: reatt, Terminal: &terminalRecorder{},
		Recoverable:  func(s sessionstore.Session) bool { return !live[s.ID] },
		LevelTimeout: time.Hour, TreeTimeout: time.Hour,
		Clock: (&stepClock{base: time.Unix(0, 0)}).now,
	})
	got, err := o.RecoverTree(context.Background(), "acme", "r")
	if err != nil {
		t.Fatalf("RecoverTree: %v", err)
	}
	if got.Recoverable != 2 {
		t.Fatalf("recoverable = %d, want 2 (g1 is live)", got.Recoverable)
	}
	for _, id := range reatt.order {
		if id == "g1" {
			t.Fatalf("live node g1 must not be reattached, order=%v", reatt.order)
		}
	}
}

// spec: §8.10 — a node whose reattach fails is marked
// terminally failed; the pass reports partial_failure.
func TestRecoverTreeReattachErrorFailsNode_spec_8_10_1025(t *testing.T) {
	lister := &fakeLister{rows: map[string][]sessionstore.Session{"r": tree("p")}}
	reatt := &reattachRecorder{failOn: map[string]error{"g2": errors.New("pool exhausted")}}
	term := &terminalRecorder{}
	mx := &metricsRecorder{}
	o := newOrch(t, treerecovery.Config{
		Lister: lister, Reattacher: reatt, Terminal: term, Metrics: mx,
		LevelTimeout: time.Hour, TreeTimeout: time.Hour,
		Clock: (&stepClock{base: time.Unix(0, 0)}).now,
	})
	got, err := o.RecoverTree(context.Background(), "acme", "r")
	if err != nil {
		t.Fatalf("RecoverTree: %v", err)
	}
	if got.Recovered != 2 || got.Failed != 1 {
		t.Fatalf("summary = %+v, want 2 recovered 1 failed", got)
	}
	if len(term.failed) != 1 || term.failed[0].id != "g2" {
		t.Fatalf("failed = %v, want [g2]", term.failed)
	}
	if len(mx.durations) != 1 || mx.durations[0].outcome != "partial_failure" {
		t.Fatalf("durations = %+v, want partial_failure", mx.durations)
	}
	// A reattach error is not a budget timeout.
	if len(mx.timeouts) != 0 {
		t.Fatalf("no timeout counter expected, got %v", mx.timeouts)
	}
}

// spec: §8.10 — when maxLevelRecoverySeconds is exceeded, the
// unrecovered nodes at that depth are marked failed and the metric
// records a level timeout.
func TestRecoverTreeLevelTimeout_spec_8_10_1025(t *testing.T) {
	lister := &fakeLister{rows: map[string][]sessionstore.Session{"r": tree("p")}}
	reatt := &reattachRecorder{}
	term := &terminalRecorder{}
	mx := &metricsRecorder{}
	// step 10ms per clock call with a 1ms level budget forces every
	// node past its level deadline; the tree budget stays generous.
	clk := &stepClock{base: time.Unix(0, 0), step: 10 * time.Millisecond}
	o := newOrch(t, treerecovery.Config{
		Lister: lister, Reattacher: reatt, Terminal: term, Metrics: mx,
		LevelTimeout: time.Millisecond, TreeTimeout: time.Hour, Clock: clk.now,
	})
	got, err := o.RecoverTree(context.Background(), "acme", "r")
	if err != nil {
		t.Fatalf("RecoverTree: %v", err)
	}
	if got.Failed != 3 || got.Recovered != 0 {
		t.Fatalf("summary = %+v, want all 3 failed", got)
	}
	if len(reatt.order) != 0 {
		t.Fatalf("no reattach should be attempted past the level budget, got %v", reatt.order)
	}
	for _, f := range term.failed {
		if f.reason != "level recovery deadline exceeded" {
			t.Fatalf("fail reason = %q, want level deadline", f.reason)
		}
	}
	for _, tm := range mx.timeouts {
		if tm.kind != "level" {
			t.Fatalf("timeout kind = %q, want level", tm.kind)
		}
	}
	if len(mx.timeouts) != 3 {
		t.Fatalf("want 3 level-timeout increments, got %d", len(mx.timeouts))
	}
	if len(mx.durations) != 1 || mx.durations[0].outcome != "partial_failure" {
		t.Fatalf("durations = %+v, want partial_failure (level, not tree)", mx.durations)
	}
}

// spec: §8.10 — when maxTreeRecoverySeconds is exceeded, every
// remaining node is marked failed and the pass reports total_timeout.
func TestRecoverTreeTreeTimeout_spec_8_10_1025(t *testing.T) {
	lister := &fakeLister{rows: map[string][]sessionstore.Session{"r": tree("p")}}
	reatt := &reattachRecorder{}
	term := &terminalRecorder{}
	mx := &metricsRecorder{}
	clk := &stepClock{base: time.Unix(0, 0), step: 10 * time.Millisecond}
	o := newOrch(t, treerecovery.Config{
		Lister: lister, Reattacher: reatt, Terminal: term, Metrics: mx,
		LevelTimeout: time.Hour, TreeTimeout: time.Millisecond, Clock: clk.now,
	})
	got, err := o.RecoverTree(context.Background(), "acme", "r")
	if err != nil {
		t.Fatalf("RecoverTree: %v", err)
	}
	if got.Failed != 3 {
		t.Fatalf("summary = %+v, want all failed under the tree budget", got)
	}
	if len(mx.durations) != 1 || mx.durations[0].outcome != "total_timeout" {
		t.Fatalf("durations = %+v, want total_timeout", mx.durations)
	}
	sawTree := false
	for _, tm := range mx.timeouts {
		if tm.kind == "tree" {
			sawTree = true
		}
	}
	if !sawTree {
		t.Fatalf("want at least one tree-timeout increment, got %v", mx.timeouts)
	}
}

// A tree with no recoverable node is a no-op: no reattach, no terminal
// transition, and no metric (so the histogram is not polluted with a
// zero-work sample).
func TestRecoverTreeNoRecoverableNodes(t *testing.T) {
	rows := []sessionstore.Session{
		node("r", "", "r", 0, session.StateRunning),
		node("c1", "r", "r", 1, session.StateCompleted),
	}
	lister := &fakeLister{rows: map[string][]sessionstore.Session{"r": rows}}
	mx := &metricsRecorder{}
	o := newOrch(t, treerecovery.Config{
		Lister: lister, Reattacher: &reattachRecorder{}, Terminal: &terminalRecorder{}, Metrics: mx,
		Clock: (&stepClock{base: time.Unix(0, 0)}).now,
	})
	got, err := o.RecoverTree(context.Background(), "acme", "r")
	if err != nil {
		t.Fatalf("RecoverTree: %v", err)
	}
	if got != (treerecovery.Summary{}) {
		t.Fatalf("summary = %+v, want zero", got)
	}
	if len(mx.durations) != 0 {
		t.Fatalf("no metric expected for an empty pass, got %v", mx.durations)
	}
}

func TestRecoverTreeListerErrorPropagates(t *testing.T) {
	lister := &fakeLister{err: errors.New("shard down")}
	o := newOrch(t, treerecovery.Config{
		Lister: lister, Reattacher: &reattachRecorder{}, Terminal: &terminalRecorder{},
		Clock: (&stepClock{base: time.Unix(0, 0)}).now,
	})
	if _, err := o.RecoverTree(context.Background(), "acme", "r"); err == nil {
		t.Fatal("want lister error to propagate")
	}
}

// spec: §8.10 (Delegation Tree Recovery), §6.2 (resume_pending wall-clock cap)
// diagnosis: the traversal reads no per-node resume window, so a node whose
// ResumeEligibleUntil lies in the past or ends during the traversal is driven
// and recovered under the level and tree budgets alone. A failure means tree
// recovery again bounds a node by its resume window, a second owner of the
// window the §6.2 cap owns.
func TestRecoverTreeIgnoresResumeEligibleUntil_spec_8_10(t *testing.T) {
	base := time.Unix(1000, 0)
	past := node("c1", "r", "r", 1, session.StateResumePending)
	past.ResumeEligibleUntil = base.Add(-time.Minute)
	soon := node("c2", "r", "r", 1, session.StateResumePending)
	soon.ResumeEligibleUntil = base.Add(time.Millisecond)
	rows := []sessionstore.Session{node("r", "", "r", 0, session.StateRunning), past, soon}
	lister := &fakeLister{rows: map[string][]sessionstore.Session{"r": rows}}
	reatt := &reattachRecorder{}
	term := &terminalRecorder{}
	o := newOrch(t, treerecovery.Config{
		Lister: lister, Reattacher: reatt, Terminal: term,
		LevelTimeout: time.Hour, TreeTimeout: time.Hour,
		Clock: (&stepClock{base: base, step: 10 * time.Millisecond}).now,
	})
	got, err := o.RecoverTree(context.Background(), "acme", "r")
	if err != nil {
		t.Fatalf("RecoverTree: %v", err)
	}
	if got.Recovered != 2 || got.Failed != 0 || len(reatt.started) != 2 {
		t.Fatalf("summary = %+v, started %v; want both nodes driven and recovered", got, reatt.started)
	}
	if len(term.failed) != 0 {
		t.Errorf("FailNode calls = %v, want none: an elapsed resume window is not a tree-recovery failure", term.failed)
	}
}

// spec: §8.10 (Delegation Tree Recovery), §6.2 (resume_pending wall-clock cap)
// diagnosis: a node the §6.2 cap moved to awaiting_client_action during its
// wait ends its reattach with an error; the traversal counts it failed in the
// summary and hands it to the terminal marker once, which decides whether its
// row may be written. A failure means such a node is counted recovered or
// marked more than once.
func TestRecoverTreeCountsAwaitingClientActionNodeFailed_spec_8_10(t *testing.T) {
	rows := []sessionstore.Session{
		node("r", "", "r", 0, session.StateRunning),
		node("c1", "r", "r", 1, session.StateResumePending),
	}
	lister := &fakeLister{rows: map[string][]sessionstore.Session{"r": rows}}
	reatt := &reattachRecorder{failOn: map[string]error{"c1": errors.New("tree recovery of node c1 ended in awaiting_client_action")}}
	term := &terminalRecorder{}
	mx := &metricsRecorder{}
	o := newOrch(t, treerecovery.Config{
		Lister: lister, Reattacher: reatt, Terminal: term, Metrics: mx,
		LevelTimeout: time.Hour, TreeTimeout: time.Hour,
		Clock: (&stepClock{base: time.Unix(0, 0)}).now,
	})
	got, err := o.RecoverTree(context.Background(), "acme", "r")
	if err != nil {
		t.Fatalf("RecoverTree: %v", err)
	}
	if got != (treerecovery.Summary{Recoverable: 1, Failed: 1}) {
		t.Fatalf("summary = %+v, want one failed node", got)
	}
	if len(term.failed) != 1 || term.failed[0].id != "c1" {
		t.Errorf("FailNode calls = %v, want [c1] once", term.failed)
	}
	if len(mx.timeouts) != 0 {
		t.Errorf("timeouts = %v, want none: the node was not lost to a budget", mx.timeouts)
	}
}

// sequenceRecorder records the reattacher's StartNode calls and the terminal
// marker's FailNode calls on one sequence.
type sequenceRecorder struct {
	events []string
	failOn map[string]error
}

func (r *sequenceRecorder) StartNode(_ context.Context, n sessionstore.Session) {
	r.events = append(r.events, "start:"+n.ID)
}

func (r *sequenceRecorder) ReattachNode(_ context.Context, n sessionstore.Session) error {
	return r.failOn[n.ID]
}

func (r *sequenceRecorder) FailNode(_ context.Context, n sessionstore.Session, _ string) {
	r.events = append(r.events, "fail:"+n.ID)
}

func (r *sequenceRecorder) index(event string) int {
	for i, e := range r.events {
		if e == event {
			return i
		}
	}
	return -1
}

// spec: §8.10 (Delegation Tree Recovery)
// diagnosis: tree recovery fails each level's unrecovered nodes before it
// continues upward, so a depth-2 node whose reattach errors is failed before
// the depth-1 node is started. A failure means the orchestrator fails nodes
// only after the whole traversal, so a parent is driven while an unrecovered
// child is still in resume_pending.
func TestRecoverTreeFailsDeeperLevelBeforeShallowerStart_spec_8_10(t *testing.T) {
	lister := &fakeLister{rows: map[string][]sessionstore.Session{"r": tree("p")}}
	seq := &sequenceRecorder{failOn: map[string]error{"g2": errors.New("restore failed")}}
	o := newOrch(t, treerecovery.Config{
		Lister: lister, Reattacher: seq, Terminal: seq,
		LevelTimeout: time.Hour, TreeTimeout: time.Hour,
		Clock: (&stepClock{base: time.Unix(0, 0)}).now,
	})
	got, err := o.RecoverTree(context.Background(), "acme", "r")
	if err != nil {
		t.Fatalf("RecoverTree: %v", err)
	}
	if got.Failed != 1 || got.Recovered != 2 {
		t.Fatalf("summary = %+v, want 2 recovered 1 failed", got)
	}
	failG2, startC1 := seq.index("fail:g2"), seq.index("start:c1")
	if failG2 < 0 || startC1 < 0 || failG2 > startC1 {
		t.Errorf("events = %v, want fail:g2 before start:c1", seq.events)
	}
}

func TestNewRejectsMissingSeams(t *testing.T) {
	full := treerecovery.Config{Lister: &fakeLister{}, Reattacher: &reattachRecorder{}, Terminal: &terminalRecorder{}}
	if treerecovery.New(full) == nil {
		t.Fatal("New(full) = nil, want orchestrator")
	}
	cases := []treerecovery.Config{
		{Reattacher: &reattachRecorder{}, Terminal: &terminalRecorder{}},
		{Lister: &fakeLister{}, Terminal: &terminalRecorder{}},
		{Lister: &fakeLister{}, Reattacher: &reattachRecorder{}},
	}
	for i, c := range cases {
		if treerecovery.New(c) != nil {
			t.Fatalf("case %d: New with a missing seam = non-nil, want nil", i)
		}
	}
}

// A nil orchestrator and an empty root id are both no-ops so the resume
// path can call RecoverTree unconditionally.
func TestRecoverTreeNilAndEmptyRootNoOp(t *testing.T) {
	var nilOrch *treerecovery.Orchestrator
	if _, err := nilOrch.RecoverTree(context.Background(), "acme", "r"); err != nil {
		t.Fatalf("nil orchestrator should be a no-op, got %v", err)
	}
	o := newOrch(t, treerecovery.Config{
		Lister: &fakeLister{}, Reattacher: &reattachRecorder{}, Terminal: &terminalRecorder{},
		Clock: (&stepClock{base: time.Unix(0, 0)}).now,
	})
	if _, err := o.RecoverTree(context.Background(), "acme", ""); err != nil {
		t.Fatalf("empty root should be a no-op, got %v", err)
	}
}
