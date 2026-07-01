// SPDX-License-Identifier: MIT

// Package deploymentconfigstore holds the last-applied Helm
// deployment-scope configuration the gateway has already audited. It is
// the durable baseline the §16.7 deployment-transition audit emitter
// diffs each incoming `helm install`/`helm upgrade` render against, so a
// gateway restart does not lose the prior values and re-emit every
// transition as a first-install event.
//
// The recorded surface is the deployment-scope settings whose Helm-driven
// transitions §16.7 / §17.2 require an audit row for: the §8.2
// cycle-detection mode, the self-recursion master gate, the delegation
// default maxDepth, and the §9.2 / §17.2 elicitation-content-integrity
// platform floor. last_revision is the Helm `.Release.Revision` the row
// reflects; a reconciliation call carrying a revision at or below it is an
// idempotent replay (a retried post-upgrade hook) that emits nothing.
//
// This is platform-operational state with a single row; it is not
// tenant-isolated. The per-tenant floor-clamp audit rows the reconciler
// fans out land in the per-tenant audit chain, not here.
// spec: §16.7 lines 672, 676, 677, 682; §17.2 line 86.
package deploymentconfigstore

import (
	"context"
	"sync"
)

// Config is the last-applied deployment-scope configuration. Empty
// string / zero fields mean the value has not been recorded yet (the
// first-install case, which the §16.7 events render as a null previous
// value). LastRevision is the Helm release revision the row reflects.
type Config struct {
	// CycleDetectionMode is the §8.2 gateway.cycleDetection.mode value
	// (enforce | warn | permissive).
	CycleDetectionMode string
	// AllowSelfRecursion is the §8.2 gateway.allowSelfRecursion master
	// gate (yes | no).
	AllowSelfRecursion string
	// DefaultMaxDepth is the §8.2 step-5 gateway.delegation.defaultMaxDepth
	// Helm fallback. 0 means unrecorded.
	DefaultMaxDepth int
	// ElicitationFloor is the §9.2 / §17.2 platform minimum-enforcement
	// floor (off | detect-only | enforce).
	ElicitationFloor string
	// LastRevision is the Helm .Release.Revision this Config reflects.
	LastRevision int64
}

// Store is the durable deployment-config baseline. Get reports whether a
// prior render has been recorded (found=false on the first ever call, so
// the reconciler renders previous values as null). Put overwrites the
// single platform-scoped row.
type Store interface {
	// Get returns the recorded baseline. found is false when no render
	// has been recorded yet.
	Get(ctx context.Context) (cfg Config, found bool, err error)
	// Put overwrites the recorded baseline with cfg.
	Put(ctx context.Context, cfg Config) error
}

// Memory is an in-memory Store for the minimal (no-Postgres) gateway and
// for tests. The baseline is lost on restart; the Postgres-backed pgstore
// is the durable production form.
type Memory struct {
	mu    sync.Mutex
	cfg   Config
	found bool
}

// NewMemory returns an empty in-memory Store.
func NewMemory() *Memory { return &Memory{} }

var _ Store = (*Memory)(nil)

// Get implements Store.
func (m *Memory) Get(_ context.Context) (Config, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg, m.found, nil
}

// Put implements Store.
func (m *Memory) Put(_ context.Context, cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
	m.found = true
	return nil
}
