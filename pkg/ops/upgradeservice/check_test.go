// SPDX-License-Identifier: MIT

package upgradeservice_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/releasechannel"
)

func staticChannel(version string) releasechannel.Source {
	return releasechannel.NewStaticSource(map[releasechannel.Channel]releasechannel.Manifest{
		releasechannel.ChannelStable: {Version: version, ReleaseNotes: "https://example/notes"},
	})
}

// spec: §25.8 — a newer advertised version raises the gauge, emits
// platform_upgrade_available, and records platform.version_checked.
func TestCheckDetectsNewerVersion(t *testing.T) {
	var gauge []bool
	var audits []upgradeservice.AuditEvent
	buf := events.NewEventBuffer(0)
	chk := upgradeservice.NewChecker(upgradeservice.CheckerOptions{
		Source:         staticChannel("1.6.0"),
		CurrentVersion: "1.5.0",
		Emitter:        events.NewEmitter(buf, "test"),
		Audit:          func(ev upgradeservice.AuditEvent) { audits = append(audits, ev) },
		Gauge:          func(a bool) { gauge = append(gauge, a) },
	})
	res, err := chk.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.UpgradeAvailable || res.AvailableVersion != "1.6.0" {
		t.Errorf("result = %+v", res)
	}
	if len(gauge) != 1 || gauge[0] != true {
		t.Errorf("gauge = %v, want [true]", gauge)
	}
	if len(audits) != 1 || audits[0].Type != string(audit.EventPlatformVersionChecked) {
		t.Errorf("audits = %v", audits)
	}
	page := buf.Query(0, events.EventFilter{EventType: "platform_upgrade_available"}, 10)
	if len(page.Events) != 1 {
		t.Errorf("platform_upgrade_available events = %d, want 1", len(page.Events))
	}
}

// spec: §25.8 — the running version up to date lowers the gauge and
// emits no availability event, but still records version_checked.
func TestCheckUpToDate(t *testing.T) {
	var gauge []bool
	buf := events.NewEventBuffer(0)
	chk := upgradeservice.NewChecker(upgradeservice.CheckerOptions{
		Source:         staticChannel("1.5.0"),
		CurrentVersion: "1.5.0",
		Emitter:        events.NewEmitter(buf, "test"),
		Gauge:          func(a bool) { gauge = append(gauge, a) },
	})
	res, err := chk.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.UpgradeAvailable {
		t.Error("equal versions should not be an upgrade")
	}
	if len(gauge) != 1 || gauge[0] != false {
		t.Errorf("gauge = %v, want [false]", gauge)
	}
	if got := buf.Query(0, events.EventFilter{EventType: "platform_upgrade_available"}, 10); len(got.Events) != 0 {
		t.Errorf("availability events = %d, want 0", len(got.Events))
	}
}

// spec: §25.8 — an older advertised version (downgrade) is not flagged
// as an upgrade.
func TestCheckIgnoresDowngrade(t *testing.T) {
	chk := upgradeservice.NewChecker(upgradeservice.CheckerOptions{
		Source:         staticChannel("1.4.0"),
		CurrentVersion: "1.5.0",
	})
	res, err := chk.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.UpgradeAvailable {
		t.Error("a downgrade must not be reported as available")
	}
}

// spec: §25.8 — a disabled channel (nil source) reports
// ErrChannelDisabled and Enabled() is false.
func TestCheckDisabledChannel(t *testing.T) {
	chk := upgradeservice.NewChecker(upgradeservice.CheckerOptions{CurrentVersion: "1.5.0"})
	if chk.Enabled() {
		t.Error("nil source should report Enabled()=false")
	}
	if _, err := chk.Check(context.Background()); err != upgradeservice.ErrChannelDisabled {
		t.Errorf("Check err = %v, want ErrChannelDisabled", err)
	}
}

// spec: §25.8 — a channel with no advertised manifest surfaces
// ErrManifestNotFound (the first-check 503 UPGRADE_CHANNEL_UNREACHABLE
// case).
func TestCheckNoManifest(t *testing.T) {
	chk := upgradeservice.NewChecker(upgradeservice.CheckerOptions{
		Source:         releasechannel.NewStaticSource(nil),
		CurrentVersion: "1.5.0",
	})
	if _, err := chk.Check(context.Background()); err != releasechannel.ErrManifestNotFound {
		t.Errorf("Check err = %v, want ErrManifestNotFound", err)
	}
}

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.5.0", "1.5.0", 0},
		{"1.6.0", "1.5.0", 1},
		{"1.5.0", "1.6.0", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.5.1", "1.5.0", 1},
		{"v1.5.0", "1.5.0", 0},       // leading v tolerated
		{"1.6.0-beta.1", "1.5.0", 1}, // prerelease stripped before compare
		{"1.5.0", "1.5.0-rc1", 0},    // prerelease ignored in this minimal compare
		{"1.10.0", "1.9.0", 1},       // numeric, not lexical, compare
		{"1.5", "1.5.0", 0},          // missing patch defaults to 0
	}
	for _, c := range cases {
		if got := upgradeservice.CompareSemver(c.a, c.b); got != c.want {
			t.Errorf("CompareSemver(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
