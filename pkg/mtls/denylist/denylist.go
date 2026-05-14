// SPDX-License-Identifier: MIT

// Package denylist implements the gateway's in-memory certificate deny
// list per spec §10.3 "Certificate revocation".
//
// Short-lived agent-pod certificates (4h TTL) are the primary mitigation
// against compromise; the deny list adds immediate revocation between
// rotations. The gateway adds an entry keyed by SPIFFE URI (or serial)
// when a pod is terminated for security reasons; on every subsequent
// mTLS handshake the gateway checks the inbound certificate's
// SPIFFE URI against the deny list and rejects on hit.
//
// Entries expire at the certificate's natural TTL, keeping the deny
// list small. Lookup, add, and expiry sweep are all O(1) amortised
// (Add is O(1); Contains is O(1); the periodic sweep walks the map).
//
// Cross-replica propagation (the spec's Redis pub/sub fan-out) is the
// controller's responsibility; this package is a single-replica
// primitive. The controller wraps it with the pub/sub listener and
// hands entries to Add() as they arrive.
package denylist

import (
	"sync"
	"time"
)

// DenyList is a goroutine-safe SPIFFE-URI deny list with TTL eviction.
// The zero value is not usable; construct with New.
type DenyList struct {
	mu      sync.RWMutex
	entries map[string]time.Time

	// now is overridable for tests so deterministic TTL behaviour can
	// be exercised without sleeping. Defaults to time.Now.
	now func() time.Time
}

// Option configures a DenyList at construction.
type Option func(*DenyList)

// WithClock overrides the time source. Tests use this to advance time
// without sleeping.
func WithClock(now func() time.Time) Option {
	return func(d *DenyList) { d.now = now }
}

// New returns an empty DenyList.
func New(opts ...Option) *DenyList {
	d := &DenyList{
		entries: make(map[string]time.Time),
		now:     time.Now,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Add inserts uri into the deny list with the supplied expiry. Adding
// the same URI again with a later expiry extends the entry; an earlier
// expiry is ignored so a re-publication of the same revocation does
// not shorten its visible lifetime. An entry whose expiry is already
// in the past is dropped silently.
func (d *DenyList) Add(uri string, expiry time.Time) {
	if uri == "" {
		return
	}
	now := d.now()
	if !expiry.After(now) {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if existing, ok := d.entries[uri]; ok && existing.After(expiry) {
		return
	}
	d.entries[uri] = expiry
}

// Contains reports whether uri is on the deny list and the entry has
// not yet expired. Expired entries are not silently kept; Contains
// triggers an opportunistic expiry of the queried key so the read path
// also shrinks the map on hot lookups.
func (d *DenyList) Contains(uri string) bool {
	d.mu.RLock()
	expiry, ok := d.entries[uri]
	d.mu.RUnlock()
	if !ok {
		return false
	}
	if d.now().Before(expiry) {
		return true
	}
	// Expired — remove under the write lock. Re-check after acquiring
	// the lock so a concurrent Add cannot be clobbered.
	d.mu.Lock()
	defer d.mu.Unlock()
	if cur, ok := d.entries[uri]; ok && !d.now().Before(cur) {
		delete(d.entries, uri)
	}
	return false
}

// Sweep evicts every entry whose expiry has passed. Returns the number
// of entries evicted. The controller invokes Sweep periodically; a
// once-per-minute cadence is enough given the 4h cert TTL.
func (d *DenyList) Sweep() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	removed := 0
	for k, exp := range d.entries {
		if !now.Before(exp) {
			delete(d.entries, k)
			removed++
		}
	}
	return removed
}

// Size returns the count of currently-stored entries. Expired entries
// that have not yet been swept are included; the count is therefore an
// upper bound on the live entry count.
func (d *DenyList) Size() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.entries)
}

// Remove drops uri from the deny list. Returns true if an entry was
// present (independent of expiry). Used by the controller when a
// revocation is rescinded explicitly (rare; the natural TTL is the
// expected eviction path).
func (d *DenyList) Remove(uri string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.entries[uri]
	if ok {
		delete(d.entries, uri)
	}
	return ok
}
