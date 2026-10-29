// SPDX-License-Identifier: MIT

package orphansession

// WaitDispatched blocks until every terminal-hook goroutine the reconciler
// has dispatched has returned, so a test observes a pass's hooks after Tick.
func (r *Reconciler) WaitDispatched() { r.dispatched.Wait() }
