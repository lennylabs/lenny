// SPDX-License-Identifier: MIT

package session

import (
	"testing"
)

// FuzzValidate exercises the §15.1 precondition validator on
// arbitrary (Endpoint, CurrentState) inputs. Invariant: Validate
// never panics regardless of what arbitrary string ends up as a
// state or endpoint name.
func FuzzValidate(f *testing.F) {
	f.Add("finalize", "created")
	f.Add("interrupt", "running")
	f.Add("terminate", "completed")
	f.Add("", "")
	f.Add("nonsense", "garbage")

	f.Fuzz(func(t *testing.T, endpoint, currentState string) {
		req := PreconditionRequest{
			Endpoint:     Endpoint(endpoint),
			CurrentState: State(currentState),
			Capabilities: map[Capability]bool{},
		}
		_ = Validate(req)
	})
}
