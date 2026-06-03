// SPDX-License-Identifier: MIT

package upgradeservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/releasechannel"
)

// CodeChannelUnreachable is the §25.8 error code returned when the
// release channel has no advertised manifest and no cache to fall back
// on (first check after install with the channel unreachable).
const CodeChannelUnreachable = "UPGRADE_CHANNEL_UNREACHABLE"

// ErrChannelDisabled reports that the release channel is disabled
// (platform.upgradeChannel: ""), so upgrade-check is a no-op.
var ErrChannelDisabled = errors.New("upgradeservice: release channel disabled")

// AvailabilityGauge sets the §16.5 lenny_platform_upgrade_available
// gauge: 1 when a newer release than the running version is advertised,
// 0 otherwise. lenny-ops supplies a Prometheus-backed setter; tests
// substitute a recorder. A nil setter is a no-op.
type AvailabilityGauge func(available bool)

// CheckResult is the GET /v1/admin/platform/upgrade-check response.
type CheckResult struct {
	// CurrentVersion is the running platform version.
	CurrentVersion string `json:"currentVersion"`
	// AvailableVersion is the version the channel advertises.
	AvailableVersion string `json:"availableVersion"`
	// UpgradeAvailable reports whether AvailableVersion is newer than
	// CurrentVersion.
	UpgradeAvailable bool `json:"upgradeAvailable"`
	// Manifest is the full §25.8 release manifest the channel advertised.
	Manifest releasechannel.Manifest `json:"manifest"`
}

// Checker implements the §25.8 upgrade-check: it queries a release
// channel for the latest manifest, emits platform.version_checked on
// every check, and emits the §16.6 platform_upgrade_available
// operational event plus raises the lenny_platform_upgrade_available
// gauge when a newer version than the running one is advertised.
//
// spec: §25.8 (Upgrade Check / Release Channel), §16.6, §16.5.
type Checker struct {
	source         releasechannel.Source
	currentVersion string
	emitter        events.EventEmitter
	audit          AuditSink
	gauge          AvailabilityGauge
	now            func() time.Time
}

// CheckerOptions configures a Checker.
type CheckerOptions struct {
	// Source is the release-channel manifest store. A nil source disables
	// upgrade-check (the platform.upgradeChannel: "" posture); Check then
	// returns ErrChannelDisabled.
	Source releasechannel.Source
	// CurrentVersion is the running platform version the check compares
	// against. Required.
	CurrentVersion string
	// Emitter publishes the §16.6 platform_upgrade_available operational
	// event. A nil emitter is a no-op.
	Emitter events.EventEmitter
	// Audit receives the platform.version_checked audit event. A nil sink
	// drops it.
	Audit AuditSink
	// Gauge sets the §16.5 lenny_platform_upgrade_available gauge. A nil
	// setter is a no-op.
	Gauge AvailabilityGauge
	// Now supplies the current time; nil defaults to time.Now.
	Now func() time.Time
}

// NewChecker returns a Checker over opts.
func NewChecker(opts CheckerOptions) *Checker {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Checker{
		source:         opts.Source,
		currentVersion: opts.CurrentVersion,
		emitter:        opts.Emitter,
		audit:          opts.Audit,
		gauge:          opts.Gauge,
		now:            now,
	}
}

// Enabled reports whether a release channel is configured.
func (c *Checker) Enabled() bool { return c != nil && c.source != nil }

// Check queries the channel for the stable manifest, records
// platform.version_checked, and signals availability. It returns
// ErrChannelDisabled when no channel is configured and
// releasechannel.ErrManifestNotFound when the channel has no advertised
// release (the §25.8 503 UPGRADE_CHANNEL_UNREACHABLE first-check case).
func (c *Checker) Check(ctx context.Context) (CheckResult, error) {
	if !c.Enabled() {
		return CheckResult{}, ErrChannelDisabled
	}
	m, err := c.source.Latest(releasechannel.ChannelStable)
	if err != nil {
		return CheckResult{}, err
	}
	available := CompareSemver(m.Version, c.currentVersion) > 0
	c.emitAudit(AuditEvent{
		Type:          string(audit.EventPlatformVersionChecked),
		Actor:         "lenny-ops",
		TargetVersion: m.Version,
		Detail:        fmt.Sprintf("current=%s available=%t", c.currentVersion, available),
	})
	if c.gauge != nil {
		c.gauge(available)
	}
	if available {
		c.emitAvailable(ctx, m)
	}
	return CheckResult{
		CurrentVersion:   c.currentVersion,
		AvailableVersion: m.Version,
		UpgradeAvailable: available,
		Manifest:         m,
	}, nil
}

// emitAvailable publishes the §16.6 platform_upgrade_available
// operational event carrying the current and available versions.
func (c *Checker) emitAvailable(ctx context.Context, m releasechannel.Manifest) {
	if c.emitter == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"currentVersion":   c.currentVersion,
		"availableVersion": m.Version,
		"releaseNotes":     m.ReleaseNotes,
	})
	_ = c.emitter.Emit(ctx, events.OperationalEvent{
		Source:          "//lenny.dev/ops",
		Type:            events.EventPlatformUpgradeAvailable.CloudEventsType(),
		Subject:         "platform",
		Severity:        "info",
		DataContentType: "application/json",
		Data:            payload,
	})
}

func (c *Checker) emitAudit(ev AuditEvent) {
	if c.audit == nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = c.now()
	}
	c.audit(ev)
}

// CompareSemver compares two dotted version strings numerically. It
// returns -1 when a < b, 0 when equal, and +1 when a > b. A leading "v"
// and any "-prerelease" or "+build" suffix are stripped before the
// numeric component compare; a non-numeric component sorts as 0. The
// helper is intentionally minimal — the §25.8 upgrade-check only needs
// the "is the advertised version newer than the running one" predicate,
// not full SemVer precedence over pre-release tags.
func CompareSemver(a, b string) int {
	pa := parseVersion(a)
	pb := parseVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

// parseVersion splits a version into its [major, minor, patch] numeric
// components, tolerating a leading "v" and trailing pre-release/build
// metadata.
func parseVersion(v string) [3]int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, part := range strings.SplitN(v, ".", 3) {
		if i >= 3 {
			break
		}
		out[i], _ = strconv.Atoi(strings.TrimSpace(part))
	}
	return out
}
