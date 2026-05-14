// SPDX-License-Identifier: MIT

package circuitbreaker

import (
	"testing"
	"time"
)

// FuzzBreakerValidate exercises the §11.6 breaker validator on
// arbitrary inputs. Invariant: never panics. The fuzz seed covers
// the two states, the limit tiers, and the canonical scope shapes
// plus edge cases (empty fields, oversized strings).
func FuzzBreakerValidate(f *testing.F) {
	f.Add("session-creation-paused", "open", "incident-1", "operation_type", "session_creation", "", "", "")
	f.Add("", "", "", "", "", "", "", "")
	f.Add(string(make([]byte, 1024)), "open", "x", "tenant", "", "claude-code", "", "")

	f.Fuzz(func(t *testing.T, name, state, reason, tier, opType, runtime, pool, connector string) {
		b := Breaker{
			Name:      name,
			State:     State(state),
			Reason:    reason,
			OpenedAt:  time.Now(),
			LimitTier: LimitTier(tier),
			Scope: Scope{
				Runtime:       runtime,
				Pool:          pool,
				Connector:     connector,
				OperationType: OperationType(opType),
			},
		}
		_ = b.Validate()
	})
}
