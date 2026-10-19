// SPDX-License-Identifier: MIT

package slotcounter

import (
	"context"
	"time"
)

// ProbeTickForTest runs one recovery probe round without a ticker.
func (c *Counter) ProbeTickForTest(ctx context.Context) { c.probeTick(ctx) }

// OutageSinceForTest returns the outage clock.
func (c *Counter) OutageSinceForTest() time.Time {
	c.outageMu.Lock()
	defer c.outageMu.Unlock()
	return c.outageSince
}

// LedgerLenForTest returns the number of outage-ledger entries.
func (c *Counter) LedgerLenForTest() int {
	c.outageMu.Lock()
	defer c.outageMu.Unlock()
	return len(c.ledger)
}

// RecordAdmissionForTest records a fallback admission in the outage ledger
// and stamps the outage clock, as fallbackReserve does on an admission.
func (c *Counter) RecordAdmissionForTest(podID, sessionID string) {
	c.stampOutage()
	c.record(entryAdmission, podID, sessionID)
}
