// SPDX-License-Identifier: MIT

package credfallback_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credfallback"
)

var base = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestSelectReturnsPrimary(t *testing.T) {
	c := credfallback.New([]string{"primary", "backup"}, time.Minute)
	pool, ok := c.Select(base)
	if !ok || pool != "primary" {
		t.Errorf("Select = (%q, %t), want (primary, true)", pool, ok)
	}
}

func TestSelectSkipsCoolingDownPool(t *testing.T) {
	c := credfallback.New([]string{"primary", "backup"}, time.Minute)
	c.Fault("primary", base)

	pool, ok := c.Select(base.Add(time.Second))
	if !ok || pool != "backup" {
		t.Errorf("Select after faulting primary = (%q, %t), want (backup, true)", pool, ok)
	}
}

func TestSelectAllCoolingDown(t *testing.T) {
	c := credfallback.New([]string{"primary", "backup"}, time.Minute)
	c.Fault("primary", base)
	c.Fault("backup", base)

	if pool, ok := c.Select(base.Add(time.Second)); ok {
		t.Errorf("Select with every pool cooling = (%q, %t), want ('', false)", pool, ok)
	}
}

func TestCooldownExpires(t *testing.T) {
	c := credfallback.New([]string{"primary", "backup"}, time.Minute)
	c.Fault("primary", base)

	// Once the 60s cooldown has elapsed, the primary is selectable again.
	pool, ok := c.Select(base.Add(61 * time.Second))
	if !ok || pool != "primary" {
		t.Errorf("Select after the cooldown elapsed = (%q, %t), want (primary, true)", pool, ok)
	}
}

func TestFaultExtendsCooldown(t *testing.T) {
	c := credfallback.New([]string{"primary", "backup"}, time.Minute)
	c.Fault("primary", base)
	// A second fault 30s in pushes the cooldown to 30s+60s from base.
	c.Fault("primary", base.Add(30*time.Second))

	if !c.CoolingDown("primary", base.Add(80*time.Second)) {
		t.Error("primary should still be cooling down 80s in after the extended fault")
	}
	if c.CoolingDown("primary", base.Add(91*time.Second)) {
		t.Error("primary should be available 91s in, past the extended cooldown")
	}
}

func TestAvailableExcludesCoolingDownPools(t *testing.T) {
	c := credfallback.New([]string{"primary", "backup", "tertiary"}, time.Minute)
	c.Fault("backup", base)

	got := c.Available(base.Add(time.Second))
	if len(got) != 2 || got[0] != "primary" || got[1] != "tertiary" {
		t.Errorf("Available = %v, want [primary tertiary]", got)
	}
}

func TestDefaultCooldownApplied(t *testing.T) {
	c := credfallback.New([]string{"primary"}, 0) // 0 selects DefaultCooldown
	c.Fault("primary", base)
	if !c.CoolingDown("primary", base.Add(credfallback.DefaultCooldown-time.Second)) {
		t.Error("primary should be cooling within the default cooldown window")
	}
	if c.CoolingDown("primary", base.Add(credfallback.DefaultCooldown+time.Second)) {
		t.Error("primary should be available past the default cooldown window")
	}
}

func TestEmptyChain(t *testing.T) {
	c := credfallback.New(nil, time.Minute)
	if pool, ok := c.Select(base); ok {
		t.Errorf("Select on an empty chain = (%q, %t), want ('', false)", pool, ok)
	}
}
