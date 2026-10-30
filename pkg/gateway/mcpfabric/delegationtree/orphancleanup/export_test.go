// SPDX-License-Identifier: MIT

package orphancleanup

// WaitDispatched blocks until every terminal-hook goroutine the sweeper has
// dispatched has returned, so a test observes a pass's hooks after Tick.
func (s *Sweeper) WaitDispatched() { s.dispatched.Wait() }
