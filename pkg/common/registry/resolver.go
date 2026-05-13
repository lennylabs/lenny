// SPDX-License-Identifier: MIT

// Package registry implements the ImageResolver every Lenny binary uses
// to translate a short image name (for example "echo", "lenny-gateway")
// into a full image reference. Resolution precedence is fixed:
//
//  1. An explicit override in Config.Overrides for the short name.
//  2. The default url + short-name + tag-or-digest.
//
// When Config.RequireDigest is true, the resolver rejects any reference
// that lacks an @sha256: digest, regardless of which precedence rung
// produced it.
//
// The resolver is intentionally pure: it does not touch the network, it
// does not consult Kubernetes Secrets, and it does not read environment
// variables. Configuration is supplied by the caller (usually via Helm
// values surfaced through `platform.registry.*`).
//
// See TESTING.md §13.3 and the spec/18 ImageResolver requirement.
package registry

import (
	"errors"
	"fmt"
	"strings"
)

// Config is the resolver's input. Callers populate it once at startup
// from the platform.registry.* Helm values (or the equivalent in
// embedded/lenny-dev mode).
type Config struct {
	// URL is the base registry plus optional path, e.g.
	// "ghcr.io/lennylabs". Required unless every short name is satisfied
	// by an explicit override.
	URL string

	// PullSecretName is the name of the Kubernetes image-pull secret the
	// resolver advertises alongside resolved references. The resolver
	// does not read the secret; downstream components mount it on pod
	// specs.
	PullSecretName string

	// RequireDigest enforces that every resolved reference includes an
	// `@sha256:...` digest. Resolve returns ErrDigestRequired for any
	// reference (override or default) that does not.
	RequireDigest bool

	// DefaultTag is the tag appended to the default form when no digest
	// is supplied. Empty means "no tag" (callers usually pin via
	// overrides). The spec discourages :latest; the resolver does not
	// substitute a default.
	DefaultTag string

	// Overrides is keyed by short name (for example "echo"). Each value
	// is a complete image reference, typically digest-pinned. An
	// override always wins over the URL+short-name default.
	Overrides map[string]string
}

// Resolver translates short names into full image references.
type Resolver struct {
	cfg Config
}

// New constructs a Resolver from cfg. The Overrides map is copied to
// insulate the resolver from mutation by the caller.
func New(cfg Config) *Resolver {
	r := &Resolver{cfg: cfg}
	r.cfg.Overrides = copyMap(cfg.Overrides)
	return r
}

// Errors returned by Resolve.
var (
	// ErrEmptyName is returned when Resolve receives an empty short name.
	ErrEmptyName = errors.New("registry: empty image short name")
	// ErrNoBase is returned when no override exists and Config.URL is
	// empty, so the resolver has nothing to combine.
	ErrNoBase = errors.New("registry: no override and Config.URL is empty")
	// ErrDigestRequired is returned when RequireDigest is true but the
	// resolved reference does not contain "@sha256:".
	ErrDigestRequired = errors.New("registry: resolved reference lacks @sha256: digest and Config.RequireDigest is true")
)

// Resolve translates a short name into a full image reference.
//
// Precedence: Overrides[name] wins over the URL+name default. Digest
// enforcement applies to both rungs.
func (r *Resolver) Resolve(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrEmptyName
	}

	// Override rung.
	if override, ok := r.cfg.Overrides[name]; ok {
		ref := strings.TrimSpace(override)
		if r.cfg.RequireDigest && !hasDigest(ref) {
			return "", fmt.Errorf("%w: override for %q resolved to %q", ErrDigestRequired, name, ref)
		}
		return ref, nil
	}

	// Default rung.
	base := strings.TrimRight(strings.TrimSpace(r.cfg.URL), "/")
	if base == "" {
		return "", fmt.Errorf("%w (short name %q)", ErrNoBase, name)
	}
	ref := base + "/" + name
	if r.cfg.DefaultTag != "" {
		ref += ":" + r.cfg.DefaultTag
	}
	if r.cfg.RequireDigest && !hasDigest(ref) {
		return "", fmt.Errorf("%w: default ref for %q resolved to %q", ErrDigestRequired, name, ref)
	}
	return ref, nil
}

// PullSecretName returns the configured pull-secret name. Convenience for
// callers that want it via the resolver rather than the raw Config.
func (r *Resolver) PullSecretName() string { return r.cfg.PullSecretName }

// hasDigest reports whether ref contains an "@sha256:" component.
//
// The spec's notion of "digest-pinned" is restricted to sha256 in v1;
// other algorithms are not used and intentionally not recognised here.
func hasDigest(ref string) bool {
	return strings.Contains(ref, "@sha256:")
}

func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
