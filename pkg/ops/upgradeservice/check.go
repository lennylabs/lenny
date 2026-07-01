// SPDX-License-Identifier: MIT

package upgradeservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/events"
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
	// Cached reports that this result was served from
	// platform_upgrade_check_cache because the channel was unreachable at
	// check time (§25.8 line 3413 Unreachable behavior).
	Cached bool `json:"cached,omitempty"`
	// CacheAge is the human-readable age of the cached response when
	// Cached is true (for example "12m30s").
	CacheAge string `json:"cacheAge,omitempty"`
}

// CachedCheck is one persisted §25.8 upgrade-check result. The cache
// holds at most one row (the last successful check); a later successful
// check replaces it. CheckedAt drives the cacheAge the unreachable path
// reports.
type CachedCheck struct {
	// CheckedAt is when the cached check ran.
	CheckedAt time.Time
	// CurrentVersion is the running version recorded at check time.
	CurrentVersion string
	// Manifest is the release manifest the channel advertised.
	Manifest releasechannel.Manifest
}

// CheckCache persists the §25.8 release-channel cache
// (platform_upgrade_check_cache). lenny-ops supplies a Postgres-backed
// implementation so a successful check survives a restart and an
// air-gapped install can pre-populate the row; tests substitute an
// in-memory cache. A nil cache disables caching: an unreachable channel
// then returns 503 UPGRADE_CHANNEL_UNREACHABLE with no fallback.
//
// spec: §25.8 lines 3413-3414, 3598-3605.
type CheckCache interface {
	// Load returns the cached check. ok is false when the cache is empty
	// (the cold-start condition).
	Load(ctx context.Context) (cached CachedCheck, ok bool, err error)
	// Save replaces the cached check with the latest successful result.
	Save(ctx context.Context, cached CachedCheck) error
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
	cache          CheckCache
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
	// Cache persists the §25.8 release-channel cache. A nil cache disables
	// the cached-fallback path; an unreachable channel then returns
	// UPGRADE_CHANNEL_UNREACHABLE.
	Cache CheckCache
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
		cache:          opts.Cache,
		now:            now,
	}
}

// MemCheckCache is the in-process §25.8 release-channel cache used in
// the single-process dev path and in tests. It is safe for concurrent
// use and holds at most one cached check.
type MemCheckCache struct {
	mu     sync.Mutex
	cached *CachedCheck
}

// NewMemCheckCache returns an empty in-memory check cache.
func NewMemCheckCache() *MemCheckCache { return &MemCheckCache{} }

// Load returns the cached check.
func (m *MemCheckCache) Load(context.Context) (CachedCheck, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cached == nil {
		return CachedCheck{}, false, nil
	}
	return *m.cached, true, nil
}

// Save records the cached check.
func (m *MemCheckCache) Save(_ context.Context, cached CachedCheck) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := cached
	m.cached = &cp
	return nil
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
		// §25.8 line 3413 Unreachable behavior: serve the cached response
		// with cached=true and a cacheAge when the channel is unreachable.
		// A missing manifest and a transport error are both "unreachable"
		// for the cache fallback; an empty cache returns the original error
		// (the first-check 503 UPGRADE_CHANNEL_UNREACHABLE case).
		if res, ok := c.cachedResult(ctx); ok {
			return res, nil
		}
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
	// §25.8 line 3414 Caching: a successful check refreshes the durable
	// cache so a later unreachable check can fall back to it. A cache
	// write failure is non-fatal — the live result still returns.
	if c.cache != nil {
		_ = c.cache.Save(ctx, CachedCheck{
			CheckedAt:      c.now().UTC(),
			CurrentVersion: c.currentVersion,
			Manifest:       m,
		})
	}
	return CheckResult{
		CurrentVersion:   c.currentVersion,
		AvailableVersion: m.Version,
		UpgradeAvailable: available,
		Manifest:         m,
	}, nil
}

// cachedResult reads the durable cache and renders it as a CheckResult
// with cached=true and a cacheAge measured from the cached CheckedAt.
// ok is false when no cache is configured or the cache is empty, so the
// caller falls through to the unreachable error.
func (c *Checker) cachedResult(ctx context.Context) (CheckResult, bool) {
	if c.cache == nil {
		return CheckResult{}, false
	}
	cached, ok, err := c.cache.Load(ctx)
	if err != nil || !ok {
		return CheckResult{}, false
	}
	available := CompareSemver(cached.Manifest.Version, c.currentVersion) > 0
	if c.gauge != nil {
		c.gauge(available)
	}
	age := c.now().UTC().Sub(cached.CheckedAt).Round(time.Second)
	if age < 0 {
		age = 0
	}
	return CheckResult{
		CurrentVersion:   c.currentVersion,
		AvailableVersion: cached.Manifest.Version,
		UpgradeAvailable: available,
		Manifest:         cached.Manifest,
		Cached:           true,
		CacheAge:         age.String(),
	}, true
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
