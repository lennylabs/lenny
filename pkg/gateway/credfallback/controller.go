// SPDX-License-Identifier: MIT

package credfallback

import (
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
)

// DefaultMaxRotations is the §4.9 credentialPolicy
// `fallback.maxRotationsPerSession` default applied when New receives a
// non-positive bound. spec: spec/04_system-components.md line 1321.
const DefaultMaxRotations = 3

// Decision is the outcome of a single fault evaluation against a
// session's fallback chain. It tells the caller whether the §4.9
// Fallback Flow can continue with a replacement pool or has reached the
// CREDENTIAL_FALLBACK_EXHAUSTED terminal state. spec:
// spec/04_system-components.md lines 1383-1411 (Fallback Flow steps
// 3-5).
type Decision struct {
	// Exhausted is true when the session's rotation budget is spent or
	// no fallback pool remains for the provider. The caller terminates
	// the session with CREDENTIAL_FALLBACK_EXHAUSTED, emits the
	// credential.fallback_exhausted audit event, and increments
	// lenny_gateway_credential_fallback_exhausted_total.
	Exhausted bool
	// NextPool is the replacement credential pool the caller leases from
	// when Exhausted is false (Fallback Flow step 4). Empty when
	// Exhausted is true.
	NextPool string
	// RotationCount is the session's running rotation counter after this
	// fault, shared across all providers. It is surfaced in the
	// fallback_exhausted audit event's rotation_count field.
	RotationCount int
	// ChainAttempted is the ordered fallback pool list for the faulted
	// provider, recorded in the audit event's fallback_chain_attempted
	// field.
	ChainAttempted []string
}

// sessionState is one session's §4.9 fallback runtime: the rotation
// counter shared across providers (spec line 1321
// "maxRotationsPerSession — total across all providers in a session")
// and a fallback Chain per provider.
type sessionState struct {
	rotationCount int
	chains        map[credential.Provider]*Chain
}

// Controller is the §4.9 credentialPolicy fallback orchestrator. It
// holds, per session, the rotation budget shared across providers and a
// fallback Chain per provider, and evaluates each upstream credential
// fault into a Decision (continue with a replacement pool, or terminate
// with CREDENTIAL_FALLBACK_EXHAUSTED). It is the caller for the pure
// Chain selection-and-cooldown state.
//
// A gateway replica owns the fallback state for the sessions it proxies
// LLM traffic for; session-prefix sharding (§10.1) routes a session's
// requests consistently, so the rotation counter is a replica-local
// runtime value rather than a persisted column. Release drops a
// session's state at teardown.
//
// The Controller is goroutine-safe; the per-session Chain mutations it
// drives are serialized under the Controller lock.
type Controller struct {
	mu           sync.Mutex
	maxRotations int
	cooldown     time.Duration
	sessions     map[string]*sessionState
	now          func() time.Time
}

// New returns a fallback Controller. maxRotations is the §4.9
// maxRotationsPerSession budget; a non-positive value selects
// DefaultMaxRotations. cooldown is the credentialPolicy
// cooldownOnRateLimit applied to a faulted pool; a non-positive value
// selects DefaultCooldown.
func NewController(maxRotations int, cooldown time.Duration) *Controller {
	if maxRotations <= 0 {
		maxRotations = DefaultMaxRotations
	}
	return &Controller{
		maxRotations: maxRotations,
		cooldown:     cooldown,
		sessions:     map[string]*sessionState{},
		now:          time.Now,
	}
}

// SetClock overrides the time source for tests.
func (c *Controller) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// RegisterChain installs the §4.9
// credentialPolicy.providerPools.{provider}.fallback.order for one
// provider in a session. The first pool is the primary. A deployment
// without an explicit fallback order does not call this; Fault then
// lazily seeds a single-pool chain from the faulted pool so the budget
// and cooldown still apply (a session with no fallback pool exhausts
// once its only pool faults). Re-registering replaces the order.
func (c *Controller) RegisterChain(sessionID string, provider credential.Provider, order []string) {
	if sessionID == "" || provider == "" || len(order) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.stateLocked(sessionID)
	st.chains[provider] = New(order, c.cooldown)
}

// Fault evaluates one upstream credential fault for a session's
// provider and returns the §4.9 Fallback Flow decision. faultedPool is
// the pool whose lease failed; it is placed on cooldown. trigger is the
// fault rotation trigger; only triggers that count against the rotation
// budget (every trigger except proactive_renewal) increment the
// session counter. spec: spec/04_system-components.md lines 1383-1411.
func (c *Controller) Fault(sessionID string, provider credential.Provider, faultedPool string, trigger credential.RotationTrigger) Decision {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	st := c.stateLocked(sessionID)

	chain, ok := st.chains[provider]
	if !ok {
		// No explicit fallback order: seed a single-pool chain so the
		// faulted pool is cooled and the chain exhausts with no
		// replacement (Fallback Flow step 4 finds nothing available).
		chain = New([]string{faultedPool}, c.cooldown)
		st.chains[provider] = chain
	}

	// Fallback Flow step 2: mark the faulted pool's cooldown.
	chain.Fault(faultedPool, now)

	// Fallback Flow step 3: increment the session's shared rotation
	// counter for fault-driven rotations and check the budget.
	if trigger.CountsAgainstRotationBudget() {
		st.rotationCount++
	}

	dec := Decision{
		RotationCount:  st.rotationCount,
		ChainAttempted: chain.Order(),
	}

	if st.rotationCount > c.maxRotations {
		dec.Exhausted = true
		return dec
	}

	// Fallback Flow step 4: select the highest-priority pool not on
	// cooldown. None available is the CREDENTIAL_FALLBACK_EXHAUSTED
	// terminal state.
	next, ok := chain.Select(now)
	if !ok {
		dec.Exhausted = true
		return dec
	}
	dec.NextPool = next
	return dec
}

// RotationCount returns the session's running rotation counter, shared
// across providers. It is zero for an unknown session.
func (c *Controller) RotationCount(sessionID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.sessions[sessionID]
	if !ok {
		return 0
	}
	return st.rotationCount
}

// CoolingDown reports whether a session's provider pool is on cooldown
// at the Controller's current clock. It backs the
// lenny_credential_pool_cooldown_count gauge recompute.
func (c *Controller) CoolingDown(sessionID string, provider credential.Provider, pool string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.sessions[sessionID]
	if !ok {
		return false
	}
	chain, ok := st.chains[provider]
	if !ok {
		return false
	}
	return chain.CoolingDown(pool, c.now())
}

// Release drops a session's fallback state at teardown (§7.1 session
// release). It is a no-op for an unknown session.
func (c *Controller) Release(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessions, sessionID)
}

// stateLocked returns the session's state, creating it on first use.
// The caller holds c.mu.
func (c *Controller) stateLocked(sessionID string) *sessionState {
	st, ok := c.sessions[sessionID]
	if !ok {
		st = &sessionState{chains: map[credential.Provider]*Chain{}}
		c.sessions[sessionID] = st
	}
	return st
}
