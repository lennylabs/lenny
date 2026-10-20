// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"crypto/sha1" //nolint:gosec // Redis names a script by the SHA-1 of its body; this is not a security use.
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

// scriptKind classifies one slot-set Lua script by what a successful run of
// it does to a member.
type scriptKind uint8

const (
	scriptOther scriptKind = iota
	// scriptReserve adds the session's member under the cap.
	scriptReserve
	// scriptRelease removes the session's member (the keyed release).
	scriptRelease
	// scriptReap removes a stale member (the stale-slot reaper).
	scriptReap
)

// classifyScript names a slot-set script from its body. The release script
// is the only one that adds a leaked: member, the reap script the only one
// that refuses a pod's last member (-3), and the reserve script the only one
// that compares the cardinality against a cap.
func classifyScript(body string) scriptKind {
	switch {
	case strings.Contains(body, "SREM") && strings.Contains(body, "'leaked:'"):
		return scriptRelease
	case strings.Contains(body, "SREM") && strings.Contains(body, "return -3"):
		return scriptReap
	case strings.Contains(body, "SADD") && strings.Contains(body, "tonumber(ARGV[2])"):
		return scriptReserve
	default:
		return scriptOther
	}
}

// SlotLog records every member the gateway's slot counters reserved and
// removed, read off the Redis commands the counters send. A reservation
// opens a member's episode; each removal inside one episode is counted, so
// a member removed twice without a reservation between the removals, as a
// duplicate release after a rehydration re-adds it, is visible to the
// checker. A rehydration seed re-adds a member without opening an episode.
//
// spec: §5.2 (Slot assignment atomicity), §6.2 (leaked slot semantics)
type SlotLog struct {
	mu sync.Mutex
	// scripts maps a script's SHA-1 to its kind, learned from the first
	// EVAL that carries the body (go-redis sends EVAL after a NOSCRIPT).
	scripts map[string]scriptKind
	// removals counts removals of pod/member in its current episode.
	removals map[string]int
	// overRemoved lists every pod/member removed more than once in one
	// episode, in the order the second removal landed.
	overRemoved []string
}

// NewSlotLog returns an empty slot log. Add it to a Redis client with
// AddHook to record the slot-set scripts that client runs.
func NewSlotLog() *SlotLog {
	return &SlotLog{scripts: map[string]scriptKind{}, removals: map[string]int{}}
}

// DialHook implements redis.Hook.
func (l *SlotLog) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return next(ctx, network, addr)
	}
}

// ProcessPipelineHook implements redis.Hook. The counters issue no
// pipelines.
func (l *SlotLog) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// ProcessHook implements redis.Hook: it records the effect of each
// successful slot-set script run.
func (l *SlotLog) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if err == nil {
			l.observe(cmd)
		}
		return err
	}
}

// observe records one successful EVAL or EVALSHA.
func (l *SlotLog) observe(cmd redis.Cmder) {
	args := cmd.Args()
	if len(args) < 4 {
		return
	}
	name := strings.ToLower(fmt.Sprint(args[0]))
	if name != "eval" && name != "evalsha" {
		return
	}
	ref := fmt.Sprint(args[1])
	l.mu.Lock()
	defer l.mu.Unlock()
	if name == "eval" {
		sum := sha1.Sum([]byte(ref)) //nolint:gosec // script identity, as Redis computes it
		sha := hex.EncodeToString(sum[:])
		l.scripts[sha] = classifyScript(ref)
		ref = sha
	}
	kind := l.scripts[ref]
	if kind == scriptOther {
		return
	}
	pod, member, ok := scriptTarget(args)
	if !ok {
		return
	}
	key := pod + "/" + member
	c, isCmd := cmd.(*redis.Cmd)
	if !isCmd {
		return
	}
	switch kind {
	case scriptReserve:
		if n, err := c.Int64(); err == nil && n >= 1 {
			l.removals[key] = 0
		}
	case scriptRelease:
		if res, err := c.Int64Slice(); err == nil && len(res) == 2 && res[0] == 1 {
			l.removed(key)
		}
	case scriptReap:
		if n, err := c.Int64(); err == nil && n == 1 {
			l.removed(key)
		}
	}
}

// removed counts one removal of key in its current episode.
func (l *SlotLog) removed(key string) {
	l.removals[key]++
	if l.removals[key] == 2 {
		l.overRemoved = append(l.overRemoved, key)
	}
}

// scriptTarget returns the pod and the member a slot-set script names:
// the pod is the hash tag of its first key, and the member its first
// argument.
func scriptTarget(args []any) (pod, member string, ok bool) {
	var numKeys int
	if _, err := fmt.Sscan(fmt.Sprint(args[2]), &numKeys); err != nil || numKeys < 1 {
		return "", "", false
	}
	if len(args) < 3+numKeys+1 {
		return "", "", false
	}
	key := fmt.Sprint(args[3])
	open, closing := strings.Index(key, "{"), strings.Index(key, "}")
	if open < 0 || closing <= open || !strings.HasSuffix(key, ":active_slots") {
		return "", "", false
	}
	return key[open+1 : closing], fmt.Sprint(args[3+numKeys]), true
}

// OverRemoved returns every pod/member a release or a reap removed more
// than once within one reservation episode.
func (l *SlotLog) OverRemoved() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.overRemoved...)
}

// Removals returns how many times member was removed from pod in its
// current reservation episode.
func (l *SlotLog) Removals(pod, member string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.removals[pod+"/"+member]
}

// concurrentPoolStore returns the gateway pool registry that makes the
// harness pool a concurrent-workspace pool: its session policy carries
// maxConcurrentSessions above 1, the process-level isolation
// acknowledgment, and, on a recycling harness, the recycle block.
//
// spec: §5.2 (sessionPolicy block, gateway-enforced subset)
func concurrentPoolStore(maxConcurrent int32, recycling bool) (*poolstore.Memory, error) {
	sp := &runtimestore.SessionPolicy{
		MaxConcurrentSessions:            int(maxConcurrent),
		AcknowledgeProcessLevelIsolation: true,
	}
	if recycling {
		// §5.2 requires an explicit reuse limit on a recycling pool; the
		// harness pool never reaches it within a scenario.
		sp.Recycle = &runtimestore.RecyclePolicy{Enabled: true, AcknowledgeBestEffortScrub: true, MaxSessionsPerPod: 100}
	}
	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name: Pool, RuntimeRef: RuntimeRef, ExecutionMode: runtimestore.ExecutionModeSession, SessionPolicy: sp,
	}); err != nil {
		return nil, fmt.Errorf("lifecycleharness: register the concurrent pool: %w", err)
	}
	return pools, nil
}

// newSlotCounter returns one replica's slot counter: its own Counter, as
// each gateway replica holds its own, over the shared Redis client whose
// hook feeds the harness's SlotLog, with the shared session store as its
// rehydration source.
func (h *Harness) newSlotCounter() *slotcounter.Counter {
	return slotcounter.New(h.slotClient, slotcounter.WithSlotSource(h.Inner))
}

// SlotMembers returns the members of the pod's slot set, leaked: members
// included, sorted.
func (h *Harness) SlotMembers(ctx context.Context, pod string) ([]string, error) {
	members, err := slotcounter.New(h.slotClient).Members(ctx, pod)
	if err != nil {
		return nil, err
	}
	sort.Strings(members)
	return members, nil
}

// occupiedPods returns the pods whose slot set holds at least one member,
// leaked: members included, on a concurrent-workspace pool, and nil on an
// exclusive pool. A pod whose set cannot be read is left out, so the
// pods-and-claims invariant still checks its claim.
func (h *Harness) occupiedPods(ctx context.Context) map[string]bool {
	if !h.Concurrent() {
		return nil
	}
	out := map[string]bool{}
	for _, p := range h.Cluster.Pods() {
		if members, err := h.SlotMembers(ctx, p.Name); err == nil && len(members) > 0 {
			out[p.Name] = true
		}
	}
	return out
}

// Concurrent reports whether the harness pool is a concurrent-workspace
// pool.
func (h *Harness) Concurrent() bool { return h.maxConcurrent > 1 }

// slotInvariants evaluates the concurrent-pool invariants over every pod
// the cluster holds: each pod's slot set holds exactly the non-terminal
// sessions whose row names the pod plus its leaked: members (slot set); no
// pod whose set is empty and that no non-terminal row names keeps a bound
// claim (empty pod); and no member was removed twice in one reservation
// episode (single removal).
//
// spec: §5.2 (Slot assignment atomicity, Slot-holder predicate), §6.2
// (leaked slot semantics), §4.6.3 (SandboxClaim: a release that ends the
// occupancy without reuse)
func (h *Harness) slotInvariants(ctx context.Context, rows map[string]sessionRow, held map[string]bool) []Invariant {
	if !h.Concurrent() {
		return nil
	}
	sets := Invariant{Name: "slot sets"}
	empty := Invariant{Name: "empty pods keep no bound claim"}
	bound := map[string]bool{}
	if claims, err := h.Cluster.BoundClaims(ctx); err != nil {
		empty.Violations = append(empty.Violations, "list claims: "+err.Error())
	} else {
		for _, pod := range claims {
			bound[pod] = true
		}
	}
	for _, p := range h.Cluster.Pods() {
		members, err := h.SlotMembers(ctx, p.Name)
		if err != nil {
			sets.Violations = append(sets.Violations, fmt.Sprintf("read the slot set of %s: %v", p.Name, err))
			continue
		}
		sets.Violations = append(sets.Violations, h.slotSetViolations(p.Name, members, rows)...)
		if len(members) == 0 && !held[p.Name] && bound[p.Name] && !h.RelaxEmptySetClaims {
			empty.Violations = append(empty.Violations, fmt.Sprintf("claim on %s is bound, but its slot set is empty and no non-terminal row names the pod", p.Name))
		}
	}
	once := Invariant{Name: "each member removed at most once"}
	for _, key := range h.Slots.OverRemoved() {
		once.Violations = append(once.Violations, fmt.Sprintf("%s was removed more than once in one reservation", key))
	}
	return []Invariant{sets, empty, once}
}

// slotSetViolations compares one pod's slot set with the session rows: a
// non-terminal row naming the pod must be a member, and every plain member
// must be such a session. Under RelaxStaleSlotMembers a plain member whose
// session is terminal is accepted, because a release no replica performs
// leaves it counted until the stale-slot reaper or the orphan GC.
func (h *Harness) slotSetViolations(pod string, members []string, rows map[string]sessionRow) []string {
	var out []string
	present := map[string]bool{}
	for _, m := range members {
		present[m] = true
		if strings.HasPrefix(m, slotcounter.LeakedMember("")) {
			continue
		}
		row, ok := rows[m]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s's slot set holds %s, which no tracked session is", pod, m))
		case row.err != nil:
		case session.IsTerminal(row.state) && h.RelaxStaleSlotMembers:
		case session.IsTerminal(row.state):
			out = append(out, fmt.Sprintf("%s's slot set holds %s, whose session is %s", pod, m, row.state))
		case row.pod != pod:
			out = append(out, fmt.Sprintf("%s's slot set holds %s, whose %s row names pod %q", pod, m, row.state, row.pod))
		}
	}
	for id, row := range rows {
		if row.err == nil && !session.IsTerminal(row.state) && row.pod == pod && !present[id] {
			out = append(out, fmt.Sprintf("%s's slot set lacks %s, whose %s row names the pod", pod, id, row.state))
		}
	}
	sort.Strings(out)
	return out
}
