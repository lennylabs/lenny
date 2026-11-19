// SPDX-License-Identifier: MIT

package watchdog

// WaitDispatched blocks until every terminal-hook goroutine the watchdog has
// dispatched has returned, so a test observes a pass's hooks after Tick.
func (w *Watchdog) WaitDispatched() { w.dispatched.Wait() }
