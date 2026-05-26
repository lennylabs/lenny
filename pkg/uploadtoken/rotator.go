// SPDX-License-Identifier: MIT

package uploadtoken

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Rotator drives the §7.1 line 67 uploadToken signing-key rotation.
// On the configured cadence it generates a fresh 256-bit secret, calls
// KeyRing.Rotate to install it as the active signing key, and schedules
// the just-displaced key for expiry after the §7.1 overlap window so
// uploadTokens minted moments before rotation continue to verify. The
// Run loop terminates when the context is cancelled; the gateway
// passes its watchdog context so the rotator winds down with the rest
// of the process.
//
// One Rotator drives one KeyRing. Run is goroutine-safe; the rotator
// holds no mutable state outside the ring itself plus an in-process
// queue of pending expirations.
//
// spec: §7.1 line 67 — "Signing keys are rotated on a deployer-
// configurable schedule (default: 24 hours); the gateway keeps the
// previous key valid during a short overlap window (default: 5
// minutes)."
type Rotator struct {
	ring          *KeyRing
	interval      time.Duration
	overlap       time.Duration
	clock         func() time.Time
	keyIDPrefix   string
	keyGen        func() (SigningKey, error)
	rotateHook    func(active, expiring SigningKey)
	expireHook    func(expired []string)
	mu            sync.Mutex
	pendingExpiry []pendingExpiry
}

// pendingExpiry tracks a recently rotated-out key and the wall-clock
// instant after which it must be dropped from the ring. The Run loop
// scans this slice on every tick and drains entries whose deadline has
// passed.
type pendingExpiry struct {
	keyID   string
	expires time.Time
}

// RotatorOptions configures a Rotator. Every field is optional; the
// zero value selects the §7.1 defaults.
type RotatorOptions struct {
	// Interval overrides DefaultRotationInterval (24 h).
	Interval time.Duration
	// Overlap overrides DefaultOverlapWindow (5 min).
	Overlap time.Duration
	// Clock overrides time.Now (UTC). Tests inject a deterministic
	// clock to exercise the expiry sweep.
	Clock func() time.Time
	// KeyIDPrefix prefixes the auto-generated KeyID for each rotated
	// key (e.g., "gw" → "gw-2026-05-26T03:00:00Z"). The default is
	// "lenny-gateway-upload".
	KeyIDPrefix string
	// KeyGen overrides the default 256-bit crypto/rand SigningKey
	// generator. Tests inject a deterministic generator; production
	// callers leave it nil.
	KeyGen func() (SigningKey, error)
	// OnRotate, when non-nil, is called immediately after each
	// KeyRing.Rotate so an operator can audit the lifecycle event.
	// The first argument is the newly-active key, the second is the
	// just-displaced previous-active key now in the overlap window.
	OnRotate func(active, expiring SigningKey)
	// OnExpire, when non-nil, is called after each overlap-window
	// sweep with the IDs that the sweep removed from the ring.
	OnExpire func(expired []string)
}

// NewRotator returns a Rotator bound to ring. The active key in ring
// is the boot key; the first call to Rotate produces a fresh
// post-bootstrap key.
func NewRotator(ring *KeyRing, opts RotatorOptions) *Rotator {
	r := &Rotator{
		ring:        ring,
		interval:    opts.Interval,
		overlap:     opts.Overlap,
		clock:       opts.Clock,
		keyIDPrefix: opts.KeyIDPrefix,
		keyGen:      opts.KeyGen,
		rotateHook:  opts.OnRotate,
		expireHook:  opts.OnExpire,
	}
	if r.interval <= 0 {
		r.interval = DefaultRotationInterval
	}
	if r.overlap <= 0 {
		r.overlap = DefaultOverlapWindow
	}
	if r.clock == nil {
		r.clock = func() time.Time { return time.Now().UTC() }
	}
	if r.keyIDPrefix == "" {
		r.keyIDPrefix = "lenny-gateway-upload"
	}
	if r.keyGen == nil {
		r.keyGen = func() (SigningKey, error) {
			var seed [32]byte
			if _, err := rand.Read(seed[:]); err != nil {
				return SigningKey{}, fmt.Errorf("uploadtoken: rotator seed: %w", err)
			}
			return SigningKey{Secret: seed[:]}, nil
		}
	}
	return r
}

// Run drives the rotation timer until ctx is cancelled. Each tick
// rotates a fresh key into the ring and sweeps any overlap key whose
// deadline has elapsed. Run blocks; the gateway dispatches it under a
// goroutine paired with the §10.1 watchdog context.
func (r *Rotator) Run(ctx context.Context) {
	// Drain expired keys on a faster cadence than rotation so a key
	// rotated out at T enters the §7.1 5-min overlap window and is
	// dropped within ~30s of its deadline rather than waiting another
	// full rotation interval. The sweep is cheap (one slice scan) so
	// the cadence is not load-bearing.
	sweepInterval := r.overlap / 2
	if sweepInterval < time.Second {
		sweepInterval = time.Second
	}
	rotateTicker := time.NewTicker(r.interval)
	defer rotateTicker.Stop()
	sweepTicker := time.NewTicker(sweepInterval)
	defer sweepTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-rotateTicker.C:
			if err := r.RotateOnce(); err != nil {
				// Surface the failure but keep ticking; a transient
				// crypto/rand error must not stop the rotator forever.
				if r.rotateHook != nil {
					r.rotateHook(SigningKey{}, SigningKey{KeyID: err.Error()})
				}
			}
		case <-sweepTicker.C:
			r.SweepOnce()
		}
	}
}

// RotateOnce performs a single rotation: a fresh key replaces the
// active signing key, the previous active key enters the overlap
// window, and the expiry queue records when it must be dropped. It
// returns any error encountered generating the new key; the ring is
// untouched on error.
func (r *Rotator) RotateOnce() error {
	next, err := r.keyGen()
	if err != nil {
		return err
	}
	if next.KeyID == "" {
		next.KeyID = r.newKeyID()
	}
	displaced := r.ring.Active()
	r.ring.Rotate(next)
	r.mu.Lock()
	r.pendingExpiry = append(r.pendingExpiry, pendingExpiry{
		keyID:   displaced.KeyID,
		expires: r.clock().Add(r.overlap),
	})
	r.mu.Unlock()
	if r.rotateHook != nil {
		r.rotateHook(next, displaced)
	}
	return nil
}

// SweepOnce drops every overlap key whose deadline has elapsed.
// Returns the IDs of the keys removed from the ring; the empty slice
// signals "no work this sweep".
func (r *Rotator) SweepOnce() []string {
	now := r.clock()
	r.mu.Lock()
	kept := r.pendingExpiry[:0]
	var expired []string
	for _, p := range r.pendingExpiry {
		if !p.expires.After(now) {
			expired = append(expired, p.keyID)
			continue
		}
		kept = append(kept, p)
	}
	r.pendingExpiry = kept
	r.mu.Unlock()
	if len(expired) == 0 {
		return nil
	}
	r.ring.Expire(expired...)
	if r.expireHook != nil {
		r.expireHook(expired)
	}
	return expired
}

// PendingExpiryCount reports how many overlap-window keys are still
// scheduled for expiry. Exposed so the gateway can publish the
// `lenny_upload_token_overlap_keys` gauge (§16.1).
func (r *Rotator) PendingExpiryCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pendingExpiry)
}

// newKeyID returns an auto-generated KeyID for a freshly minted
// signing key: "<prefix>-<unix-nanos>-<random-suffix>". The unix-nanos
// stamp makes the IDs sortable, and the random suffix avoids
// collisions under sub-nanosecond clock resolution.
func (r *Rotator) newKeyID() string {
	now := r.clock().UTC()
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	return fmt.Sprintf("%s-%s-%s", r.keyIDPrefix, now.Format("20060102T150405Z"), hex.EncodeToString(suffix[:]))
}
