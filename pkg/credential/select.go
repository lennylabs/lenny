// SPDX-License-Identifier: MIT

package credential

import "errors"

// ErrPoolExhausted reports that a credential pool has no assignable
// credential: every candidate is unhealthy (rate-limited in cooldown,
// auth-failed, or revoked) or the pool is empty. §4.9 surfaces it to
// the caller as CREDENTIAL_POOL_EXHAUSTED.
var ErrPoolExhausted = errors.New("credential: pool has no assignable credential")

// CredentialCandidate is one assignable credential in a pool, carrying
// the load and health state the §4.9 assignment strategies weigh.
type CredentialCandidate struct {
	// CredentialID identifies the credential within its pool.
	CredentialID string
	// ActiveSessions is the credential's current in-use lease count,
	// weighed by the least-loaded strategy.
	ActiveSessions int
	// Healthy reports whether the credential is assignable now: not
	// rate-limited in cooldown, not auth-failed, and not revoked.
	Healthy bool
}

// SelectionState carries the per-pool state the round-robin and
// sticky-until-failure strategies thread between assignments. The
// caller persists it per pool and passes it back on the next select.
type SelectionState struct {
	// Cursor is the round-robin rotation position.
	Cursor int
	// Sticky is the credential ID the sticky-until-failure strategy
	// last assigned. Empty when no credential is pinned.
	Sticky string
}

// SelectCredential picks one healthy credential from candidates per the
// §4.9 assignment strategy and returns the updated SelectionState. It
// returns ErrPoolExhausted when no candidate is healthy.
//
//   - least-loaded picks the healthy candidate with the fewest active
//     sessions, breaking ties toward the earlier candidate.
//   - round-robin picks the next healthy candidate from the rotation
//     cursor, advancing the cursor past it.
//   - sticky-until-failure keeps the pinned credential while it stays
//     healthy and re-pins the least-loaded healthy candidate once the
//     pinned one fails.
func SelectCredential(strategy AssignmentStrategy, candidates []CredentialCandidate, state SelectionState) (CredentialCandidate, SelectionState, error) {
	switch strategy {
	case StrategyLeastLoaded:
		i, ok := leastLoaded(candidates)
		if !ok {
			return CredentialCandidate{}, state, ErrPoolExhausted
		}
		return candidates[i], state, nil

	case StrategyRoundRobin:
		i, ok := nextHealthy(candidates, state.Cursor)
		if !ok {
			return CredentialCandidate{}, state, ErrPoolExhausted
		}
		return candidates[i], SelectionState{
			Cursor: (i + 1) % len(candidates),
			Sticky: state.Sticky,
		}, nil

	case StrategyStickyUntilFailure:
		if i, ok := healthyByID(candidates, state.Sticky); ok {
			return candidates[i], state, nil
		}
		i, ok := leastLoaded(candidates)
		if !ok {
			return CredentialCandidate{}, state, ErrPoolExhausted
		}
		return candidates[i], SelectionState{
			Cursor: state.Cursor,
			Sticky: candidates[i].CredentialID,
		}, nil

	default:
		return CredentialCandidate{}, state, &ConfigError{
			Name:       string(strategy),
			Violations: []string{"unknown assignment strategy"},
		}
	}
}

// leastLoaded returns the index of the healthy candidate with the
// fewest active sessions. ok is false when no candidate is healthy.
func leastLoaded(candidates []CredentialCandidate) (idx int, ok bool) {
	best := -1
	for i, c := range candidates {
		if !c.Healthy {
			continue
		}
		if best < 0 || c.ActiveSessions < candidates[best].ActiveSessions {
			best = i
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

// nextHealthy returns the index of the first healthy candidate at or
// after cursor, wrapping around. ok is false when none is healthy.
func nextHealthy(candidates []CredentialCandidate, cursor int) (idx int, ok bool) {
	n := len(candidates)
	if n == 0 {
		return 0, false
	}
	for off := 0; off < n; off++ {
		i := ((cursor % n) + n + off) % n
		if candidates[i].Healthy {
			return i, true
		}
	}
	return 0, false
}

// healthyByID returns the index of the candidate with the given
// credential ID when it is present and healthy. An empty id never
// matches.
func healthyByID(candidates []CredentialCandidate, id string) (idx int, ok bool) {
	if id == "" {
		return 0, false
	}
	for i, c := range candidates {
		if c.CredentialID == id && c.Healthy {
			return i, true
		}
	}
	return 0, false
}
