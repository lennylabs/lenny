// SPDX-License-Identifier: MIT

// Package releasechannel implements the §25.8 release-channel manifest
// publisher: the HTTPS endpoint that serves the Ed25519-signed `latest`
// JSON document Lenny operators query via
// `GET /v1/admin/platform/upgrade-check`.
//
// The publisher is split into three pure pieces so each is unit-testable
// without a network or a clock:
//
//   - manifest.go declares the canonical JSON shape (§25.8 Release
//     Channel Response).
//   - signer.go implements the response signature with Ed25519 plus the
//     key-rotation overlap window. During a rotation both the current
//     and previous keys verify; outside a rotation only the current key
//     verifies.
//   - publisher.go wraps a Source and a Signer in an http.Handler that
//     serves GET requests with the `X-Lenny-Release-Signature` header
//     §25.8 specifies.
//
// The lenny-ops binary owns the long-lived Ed25519 signing key
// material; this package never reads from disk. The custody and
// rotation cadence are the operator's responsibility per §25.8's "the
// Lenny release-channel public key is compiled into lenny-ops" and the
// air-gap mirror guidance.
//
// The publisher targets the §25.8 99.9% monthly availability SLO. The
// handler reports a Prometheus-grade error envelope on every failure so
// the SLO can be measured from the response side.
package releasechannel

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// Manifest is the canonical §25.8 Release Channel Response. It is the
// JSON document the publisher signs and serves.
//
// The field set mirrors the spec example verbatim: `version`, the
// per-component `images` and `digests` maps, `minUpgradeFrom`,
// `schemaVersion`, `crdVersion`, and `releaseNotes`. The publisher
// does not enrich or redact the manifest; the Source supplies the
// fully-resolved document and the publisher signs and serves it.
type Manifest struct {
	// Version is the SemVer of the release the channel advertises.
	Version string `json:"version"`

	// Images maps each Lenny component (gateway, ops, controllers,
	// backup) to its image name and tag relative to the release
	// channel's own registry. Deployers translate these through the
	// platform.registry.url Helm value (§25.8 image resolution).
	Images map[string]string `json:"images"`

	// Digests maps each Lenny component to the `sha256:...` content
	// hash of its image. When `platform.registry.requireDigest` is
	// true the upgrade system uses these instead of the tags from
	// Images.
	Digests map[string]string `json:"digests"`

	// MinUpgradeFrom is the lowest current version the release can
	// upgrade from. The upgrade-check pre-flight rejects an upgrade
	// when the deployment's current version is below this value.
	MinUpgradeFrom string `json:"minUpgradeFrom"`

	// SchemaVersion is the Postgres schema version the release ships
	// (the value `SELECT version FROM schema_migrations ORDER BY
	// version DESC LIMIT 1` reports after the release's migrations).
	SchemaVersion int `json:"schemaVersion"`

	// CRDVersion is the Lenny-CRD API version the release ships
	// (`v1beta1`, `v1beta2`, etc.). The upgrade orchestrator reads
	// this to decide whether a `CRDUpdate` phase is required.
	CRDVersion string `json:"crdVersion"`

	// ReleaseNotes is the URL of the release notes the upgrade-check
	// surface displays to operators.
	ReleaseNotes string `json:"releaseNotes"`
}

// Channel labels the release track the request asks for. §25.8 names
// "stable" and "beta" as the two channels the publisher serves.
type Channel string

const (
	// ChannelStable is the default release track. Operators consume it
	// in production deployments.
	ChannelStable Channel = "stable"
	// ChannelBeta is the pre-release track. Operators opt in by
	// passing `?channel=beta`.
	ChannelBeta Channel = "beta"
)

// IsKnown reports whether c is a channel the publisher serves. The
// handler rejects every other value rather than silently substituting
// the default, so an operator typo surfaces as a 400 instead of as a
// stable manifest delivered under the wrong label.
func (c Channel) IsKnown() bool {
	return c == ChannelStable || c == ChannelBeta
}

// ParseChannel projects a query-string value into a Channel. An empty
// value defaults to ChannelStable per §25.8 ("?channel=stable|beta —
// selects release track"); the spec leaves the default implicit and the
// publisher honors stable as the well-defined choice.
func ParseChannel(s string) (Channel, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return ChannelStable, nil
	}
	c := Channel(s)
	if !c.IsKnown() {
		return "", errors.New("releasechannel: unknown channel " + s)
	}
	return c, nil
}

// MeetsMinUpgradeFrom reports whether currentVersion satisfies the
// manifest's MinUpgradeFrom prerequisite. An empty currentVersion (the
// caller did not pass ?currentVersion=) or an empty MinUpgradeFrom (the
// release imposes no hard floor) both pass: the §25.8 personalized
// filter is opt-in on both sides. Otherwise the release is advertised
// only when currentVersion is at or above MinUpgradeFrom.
//
// spec: §25.8 line 3410 ("?currentVersion=1.4.3 (for personalized
// minUpgradeFrom — the service may refuse to advertise a newer release
// if the current version is below a hard prerequisite)").
func (m Manifest) MeetsMinUpgradeFrom(currentVersion string) bool {
	currentVersion = strings.TrimSpace(currentVersion)
	if currentVersion == "" || strings.TrimSpace(m.MinUpgradeFrom) == "" {
		return true
	}
	return compareSemver(currentVersion, m.MinUpgradeFrom) >= 0
}

// compareSemver compares two dotted version strings numerically,
// returning -1 when a < b, 0 when equal, and +1 when a > b. A leading
// "v" and any "-prerelease"/"+build" suffix are stripped before the
// numeric component compare. The helper is the lower-layer twin of
// upgradeservice.CompareSemver; releasechannel cannot import
// upgradeservice (that package imports this one), so the minimal
// "is this version at least that one" predicate is duplicated here.
//
// spec: §25.8 line 3410 (minUpgradeFrom prerequisite compare).
func compareSemver(a, b string) int {
	pa := parseSemver(a)
	pb := parseSemver(b)
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

// parseSemver splits a version into its [major, minor, patch] numeric
// components, tolerating a leading "v" and trailing pre-release/build
// metadata. A non-numeric component sorts as 0.
func parseSemver(v string) [3]int {
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

// Source is the read-only manifest store the publisher serves from.
// lenny-ops constructs the concrete Source from the release-build
// artifacts (a baked-in map for the canonical builds, a Postgres-backed
// store for operator-managed mirrors); tests substitute a fake.
//
// The publisher consults Latest once per request. A Source that needs
// to refresh from durable storage caches that work; the publisher does
// not.
type Source interface {
	// Latest returns the most recent manifest published for the named
	// channel. ErrManifestNotFound is the spec's "no advertised
	// release yet" signal; every other error fails the request. The
	// context lets an I/O-backed Source (the HTTP consumer that fetches
	// the manifest from a remote release channel) honor the caller's
	// cancellation and deadline; an in-memory Source ignores it.
	Latest(ctx context.Context, channel Channel) (Manifest, error)
}

// ErrManifestNotFound is returned by Source.Latest when the channel
// has no advertised manifest. The publisher maps this to HTTP 404 so
// the operator sees the difference between a misconfigured channel
// and a transient outage.
var ErrManifestNotFound = errors.New("releasechannel: no manifest for channel")

// StaticSource serves manifests from an in-memory map. It is the
// implementation lenny-ops uses for canonical builds where the
// manifests are compiled in at release time. The chart-managed
// `platform.releaseChannel.publicKeyPath` mirror serves an
// operator-supplied map via the same shape.
type StaticSource struct {
	manifests map[Channel]Manifest
}

// NewStaticSource returns a StaticSource backed by manifests. The map
// is copied so the caller can mutate its own copy without racing the
// publisher.
func NewStaticSource(manifests map[Channel]Manifest) *StaticSource {
	copyMap := make(map[Channel]Manifest, len(manifests))
	for k, v := range manifests {
		copyMap[k] = v
	}
	return &StaticSource{manifests: copyMap}
}

// Latest returns the manifest for channel, or ErrManifestNotFound when
// the channel has no advertised release. The context is unused: the map
// lookup does no I/O.
func (s *StaticSource) Latest(_ context.Context, channel Channel) (Manifest, error) {
	m, ok := s.manifests[channel]
	if !ok {
		return Manifest{}, ErrManifestNotFound
	}
	return m, nil
}
