// SPDX-License-Identifier: MIT

// Package registryservice implements the §25.8 runtime registry API: the
// lenny-ops surface behind GET /v1/admin/platform/registry (returns the
// effective registry configuration with the pull-secret name but not its
// value) and PUT /v1/admin/platform/registry (a Postgres-backed,
// restart-free update of the registry URL and per-component overrides).
//
// The chart-rendered platform.registry.* Helm values are the base
// configuration. When an operator PUTs a runtime override it is persisted
// to the platform_registry_config singleton (migration 0135) and overlays
// the base, so the next image resolution (upgrade preflight, warm-pool
// reference) reads the override without a redeploy — the §25.8 line 3362
// "takes effect on next image resolution" contract.
//
// spec: §25.8 (Image Registry Configuration / Runtime API), lines
// 3300-3301, 3360-3362, 3352-3355.
package registryservice

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/common/registry"
	"github.com/lennylabs/lenny/pkg/observability/audit"
)

// componentImages maps the §25.8 component short names operators address
// (gateway, ops, controllers, backup) to the image short name the
// ImageResolver combines with the registry base (spec lines 3352-3355).
var componentImages = map[string]string{
	"gateway":     "lenny-gateway",
	"ops":         "lenny-ops",
	"controllers": "lenny-controllers",
	"backup":      "lenny-backup",
}

// Components returns the §25.8 platform component short names in a stable
// order. The upgrade preflight resolves a target image reference for each.
func Components() []string {
	out := make([]string, 0, len(componentImages))
	for c := range componentImages {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// EffectiveConfig is the resolved §25.8 registry configuration: the base
// Helm values overlaid by any persisted runtime override. It is the body
// GET /v1/admin/platform/registry returns. PullSecretName is the Secret
// name only; the secret value is never carried here.
type EffectiveConfig struct {
	// URL is the base image registry plus optional path
	// (e.g. "ghcr.io/lennylabs").
	URL string `json:"url"`
	// Overrides maps a component short name to a complete image reference.
	Overrides map[string]string `json:"overrides,omitempty"`
	// PullSecretName is the Kubernetes image-pull Secret name. The secret
	// contents are never returned (§25.8 line 3362).
	PullSecretName string `json:"pullSecretName"`
	// RequireDigest reports whether resolved references must be
	// digest-pinned (@sha256:).
	RequireDigest bool `json:"requireDigest"`
	// Source reports whether the effective config is the chart base
	// ("helm") or a persisted runtime override ("postgres").
	Source string `json:"source"`
	// UpdatedAt and UpdatedBy describe the last runtime override; both are
	// zero when the effective config is the chart base.
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
}

// Override is one persisted §25.8 runtime registry mutation. The store
// holds at most one (the latest PUT). It replaces the base configuration
// when present.
type Override struct {
	URL            string
	Overrides      map[string]string
	PullSecretName string
	RequireDigest  bool
	UpdatedAt      time.Time
	UpdatedBy      string
}

// Store persists the §25.8 platform_registry_config singleton. lenny-ops
// supplies a Postgres-backed store in production; MemoryStore is the
// single-process / test implementation. A nil store leaves the registry
// read-only at the chart base (PUT returns ErrReadOnly).
type Store interface {
	// Load returns the persisted override. ok is false when no runtime
	// override has been written (the effective config is then the base).
	Load(ctx context.Context) (override Override, ok bool, err error)
	// Save replaces the persisted override with the latest PUT.
	Save(ctx context.Context, override Override) error
}

// MemoryStore is the in-process Store. It is safe for concurrent use and
// holds at most one override.
type MemoryStore struct {
	mu  sync.Mutex
	ovr *Override
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

// Load returns the persisted override.
func (m *MemoryStore) Load(context.Context) (Override, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ovr == nil {
		return Override{}, false, nil
	}
	cp := *m.ovr
	cp.Overrides = copyMap(cp.Overrides)
	return cp, true, nil
}

// Save records the override.
func (m *MemoryStore) Save(_ context.Context, o Override) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := o
	cp.Overrides = copyMap(o.Overrides)
	m.ovr = &cp
	return nil
}

// AuditEvent is the §16.7 platform.registry_updated audit event the
// Service emits on a successful PUT. A nil AuditSink drops it.
type AuditEvent struct {
	Type   string
	Actor  string
	Detail string
	At     time.Time
}

// AuditSink receives the §16.7 platform.registry_updated audit event.
type AuditSink func(AuditEvent)

// Errors the Service returns.
var (
	// ErrReadOnly is returned by Update when no store is configured, so the
	// registry cannot be mutated at runtime (the chart-base-only posture).
	ErrReadOnly = errors.New("registryservice: registry is read-only (no runtime store configured)")
	// ErrNoBase is returned by Update when the request would leave the
	// resolver with no base URL and no override for some component.
	ErrNoBase = errors.New("registryservice: a registry url is required")
)

// Service backs the §25.8 runtime registry API.
type Service struct {
	base  EffectiveConfig
	store Store
	audit AuditSink
	now   func() time.Time
	mu    sync.Mutex // serializes the read-modify-write of the singleton
}

// Options configures a Service.
type Options struct {
	// Base is the chart-rendered platform.registry.* configuration. It is
	// the effective config until a runtime override is written.
	Base EffectiveConfig
	// Store persists runtime overrides. A nil store makes the registry
	// read-only at the chart base (Update returns ErrReadOnly).
	Store Store
	// Audit receives the §16.7 platform.registry_updated event. A nil sink
	// drops it.
	Audit AuditSink
	// Now supplies the current time; nil defaults to time.Now.
	Now func() time.Time
}

// New returns a Service over opts.
func New(opts Options) *Service {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	base := opts.Base
	base.Overrides = copyMap(base.Overrides)
	if base.Source == "" {
		base.Source = "helm"
	}
	return &Service{base: base, store: opts.Store, audit: opts.Audit, now: now}
}

// Effective returns the §25.8 effective registry configuration: the chart
// base overlaid by any persisted runtime override. A store error is
// returned so the handler surfaces the §25.8 line 3610 "Postgres down"
// degradation; a missing override is not an error (the base is effective).
func (s *Service) Effective(ctx context.Context) (EffectiveConfig, error) {
	if s.store == nil {
		return s.cloneBase(), nil
	}
	o, ok, err := s.store.Load(ctx)
	if err != nil {
		return EffectiveConfig{}, err
	}
	if !ok {
		return s.cloneBase(), nil
	}
	return EffectiveConfig{
		URL:            o.URL,
		Overrides:      copyMap(o.Overrides),
		PullSecretName: o.PullSecretName,
		RequireDigest:  o.RequireDigest,
		Source:         "postgres",
		UpdatedAt:      o.UpdatedAt,
		UpdatedBy:      o.UpdatedBy,
	}, nil
}

// UpdateRequest is the PUT /v1/admin/platform/registry body.
type UpdateRequest struct {
	// URL is the new base registry URL. Required unless every component is
	// covered by an override.
	URL string `json:"url"`
	// Overrides maps a component short name to a complete image reference.
	Overrides map[string]string `json:"overrides,omitempty"`
	// PullSecretName names the image-pull Secret (value not stored).
	PullSecretName string `json:"pullSecretName,omitempty"`
	// RequireDigest enforces digest-pinned references.
	RequireDigest bool `json:"requireDigest,omitempty"`
	// Actor is the operator/agent identity; the handler fills it from the
	// verified principal.
	Actor string `json:"-"`
}

// Update persists a runtime registry override and emits
// platform.registry_updated. It rejects an update with no base URL and no
// override (ErrNoBase) and an update with no store configured
// (ErrReadOnly). The returned config is the new effective configuration.
//
// spec: §25.8 PUT /v1/admin/platform/registry (line 3362).
func (s *Service) Update(ctx context.Context, req UpdateRequest) (EffectiveConfig, error) {
	if s.store == nil {
		return EffectiveConfig{}, ErrReadOnly
	}
	if strings.TrimSpace(req.URL) == "" && len(req.Overrides) == 0 {
		return EffectiveConfig{}, ErrNoBase
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o := Override{
		URL:            strings.TrimSpace(req.URL),
		Overrides:      copyMap(req.Overrides),
		PullSecretName: req.PullSecretName,
		RequireDigest:  req.RequireDigest,
		UpdatedAt:      s.now().UTC(),
		UpdatedBy:      req.Actor,
	}
	if err := s.store.Save(ctx, o); err != nil {
		return EffectiveConfig{}, err
	}
	s.emitAudit(AuditEvent{
		Type:   string(audit.EventPlatformRegistryUpdated),
		Actor:  req.Actor,
		Detail: "url=" + o.URL,
		At:     o.UpdatedAt,
	})
	return EffectiveConfig{
		URL:            o.URL,
		Overrides:      copyMap(o.Overrides),
		PullSecretName: o.PullSecretName,
		RequireDigest:  o.RequireDigest,
		Source:         "postgres",
		UpdatedAt:      o.UpdatedAt,
		UpdatedBy:      o.UpdatedBy,
	}, nil
}

// Resolver returns a pkg/common/registry.Resolver over the live effective
// configuration. The upgrade preflight resolves target image references
// through it so a runtime override takes effect without a restart.
func (s *Service) Resolver(ctx context.Context) (*registry.Resolver, error) {
	cfg, err := s.Effective(ctx)
	if err != nil {
		return nil, err
	}
	return registry.New(registry.Config{
		URL:            cfg.URL,
		PullSecretName: cfg.PullSecretName,
		RequireDigest:  cfg.RequireDigest,
		Overrides:      cfg.Overrides,
	}), nil
}

// ResolveImagePlan resolves the full image reference for every §25.8
// component at version, honoring per-component overrides and digest
// pinning. When digests is non-empty and the effective config requires
// digests, the component's digest is appended (@sha256:...) instead of the
// tag (spec line 3406). The returned map is keyed by component short name
// (gateway, ops, controllers, backup). It is the plan the preflight
// returns and the upgrade start records as target_images.
//
// spec: §25.8 Image Resolution (lines 3333-3358), line 3404-3406.
func (s *Service) ResolveImagePlan(ctx context.Context, version string, digests map[string]string) (map[string]string, error) {
	cfg, err := s.Effective(ctx)
	if err != nil {
		return nil, err
	}
	return resolveImagePlan(cfg, version, digests)
}

// resolveImagePlan is the pure plan computation Effective feeds. It is
// exported-by-helper so the preflight handler can resolve a plan from a
// caller-supplied EffectiveConfig in tests.
func resolveImagePlan(cfg EffectiveConfig, version string, digests map[string]string) (map[string]string, error) {
	version = strings.TrimSpace(version)
	plan := make(map[string]string, len(componentImages))
	for component, image := range componentImages {
		// An explicit override for the component wins as the full base path.
		if override, ok := cfg.Overrides[component]; ok && strings.TrimSpace(override) != "" {
			plan[component] = strings.TrimSpace(override)
			continue
		}
		base := strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
		if base == "" {
			return nil, ErrNoBase
		}
		ref := base + "/" + image
		if cfg.RequireDigest {
			d := strings.TrimSpace(digests[component])
			if d == "" {
				return nil, ErrNoBase
			}
			plan[component] = ref + "@" + ensureSha256Prefix(d)
			continue
		}
		if version != "" {
			ref += ":" + version
		}
		plan[component] = ref
	}
	return plan, nil
}

func ensureSha256Prefix(d string) string {
	if strings.HasPrefix(d, "sha256:") {
		return d
	}
	return "sha256:" + d
}

func (s *Service) cloneBase() EffectiveConfig {
	cp := s.base
	cp.Overrides = copyMap(s.base.Overrides)
	return cp
}

func (s *Service) emitAudit(ev AuditEvent) {
	if s.audit == nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = s.now()
	}
	s.audit(ev)
}

func copyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
