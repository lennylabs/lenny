// SPDX-License-Identifier: MIT

package signingkeys

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// Defaults of the gateway settings that drive a Ring, as the upload-token
// properties state them. The gateway flags --signing-key-refresh-interval
// and --signing-key-publish-lead override them.
//
// spec: §7.1 (Normal Flow)
const (
	// DefaultRefreshInterval is how often a replica reloads its keys.
	DefaultRefreshInterval = 30 * time.Second

	// DefaultPublishLead is how far ahead of its activation instant a
	// replica writes the next key. It is longer than the refresh interval.
	DefaultPublishLead = 2 * time.Minute
)

var (
	// ErrSigningUnavailable is returned by SigningKey when the ring's last
	// successful reload is older than its stale bound. The replica cannot
	// confirm that its peers hold the key it would sign with, so it signs
	// nothing until a reload succeeds.
	ErrSigningUnavailable = errors.New("signingkeys: signing stopped: the key ring has not been refreshed within the stale bound")

	// ErrNoActiveKey is returned by SigningKey when no loaded key has
	// reached its activation instant.
	ErrNoActiveKey = errors.New("signingkeys: no key of the purpose is active")
)

// Key is one unwrapped key a Ring holds. Secret is shared with the ring and
// must not be modified. RetireAfter is zero until a later key replaces it.
type Key struct {
	KeyID        string
	Secret       []byte
	RotationSlot int64
	ActiveFrom   time.Time
	RetireAfter  time.Time
	DeleteAfter  time.Time
}

// retiredAt reports whether k has been replaced by now: a key never signs
// once its retirement instant has passed, whether or not the row of the
// key that replaced it still exists.
func (k Key) retiredAt(now time.Time) bool {
	return !k.RetireAfter.IsZero() && !now.Before(k.RetireAfter)
}

// Rotation describes the key a forced rotation wrote.
type Rotation struct {
	KeyID        string
	RotationSlot int64
	ActiveFrom   time.Time
}

// rotateNowAttempts bounds how many slots RotateNow tries when other
// writers take the slot it computed.
const rotateNowAttempts = 3

// ErrRotationContended is returned by RotateNow when other writers took
// every slot it tried.
var ErrRotationContended = errors.New("signingkeys: other writers took every rotation slot the forced rotation tried")

// Options configures a Ring. RefreshInterval and PublishLead default to
// DefaultRefreshInterval and DefaultPublishLead. StaleAfter is required:
// it is the purpose's overlap window for the upload-token purpose.
type Options struct {
	RefreshInterval time.Duration
	PublishLead     time.Duration
	StaleAfter      time.Duration
	// Clock overrides time.Now; tests drive rotation and staleness with it.
	Clock func() time.Time
	// Rand overrides crypto/rand.Reader for secrets and key ids.
	Rand io.Reader
	// Logger receives rotation, sweep, and refresh-failure records. It
	// never receives a secret.
	Logger *slog.Logger
}

// Ring serves one purpose's keys on one replica. It is safe for concurrent
// use: SigningKey and VerificationKeys read a snapshot under a read lock,
// and Refresh replaces the snapshot under the write lock.
type Ring struct {
	policy  Policy
	store   Store
	sealer  Sealer
	refresh time.Duration
	lead    time.Duration
	stale   time.Duration
	clock   func() time.Time
	rand    io.Reader
	log     *slog.Logger

	// refreshMu serializes Refresh calls, so two reloads cannot install
	// their snapshots out of order.
	refreshMu sync.Mutex

	// mu guards keys and lastReload. keys holds the purpose's keys in
	// ascending rotation-slot order, as of the last successful reload.
	mu         sync.RWMutex
	keys       []Key
	lastReload time.Time
}

// Open builds the Ring of policy's purpose over store and loads its keys.
// When the purpose has no key, Open writes the first one, active at once:
// no running replica can be signing with another key of the purpose, since
// any running replica would have found that key in the store. Its
// activation instant is backdated by the publish lead, so a replica whose
// clock runs behind the writer's still finds it active when it opens. When
// the newest key is retired because the row of its successor was deleted,
// Open writes the next key one publish lead ahead and opens a ring that
// signs nothing until that key is active. Open fails when the store cannot
// be read, when a key cannot be unwrapped, when no key is active or
// awaiting activation, or when the publish lead is not longer than the
// refresh interval; the gateway does not start on any of them.
//
// spec: §7.1 (Normal Flow), §12.3 (Postgres HA Requirements)
func Open(ctx context.Context, policy Policy, store Store, sealer Sealer, opts Options) (*Ring, error) {
	if err := policy.validate(); err != nil {
		return nil, err
	}
	if store == nil || sealer == nil {
		return nil, errors.New("signingkeys: a ring needs a store and a sealer")
	}
	r := &Ring{
		policy:  policy,
		store:   store,
		sealer:  sealer,
		refresh: opts.RefreshInterval,
		lead:    opts.PublishLead,
		stale:   opts.StaleAfter,
		clock:   opts.Clock,
		rand:    opts.Rand,
		log:     opts.Logger,
	}
	if err := r.resolveOptions(); err != nil {
		return nil, err
	}
	now := r.clock()
	recs, err := store.List(ctx, policy.Purpose, now)
	if err != nil {
		return nil, fmt.Errorf("signingkeys: load %q keys: %w", policy.Purpose, err)
	}
	if len(recs) == 0 {
		if _, _, err := r.writeKey(ctx, 1, now.Add(-r.lead)); err != nil {
			return nil, err
		}
	}
	if err := r.reload(ctx); err != nil {
		return nil, err
	}
	if _, err := r.SigningKey(); errors.Is(err, ErrNoActiveKey) {
		if err := r.rotateIfDue(ctx, now); err != nil {
			return nil, err
		}
		if err := r.reload(ctx); err != nil {
			return nil, err
		}
	}
	if _, err := r.SigningKey(); err != nil && !(errors.Is(err, ErrNoActiveKey) && r.awaitingActivation(now)) {
		return nil, fmt.Errorf("signingkeys: purpose %q: %w", policy.Purpose, err)
	}
	return r, nil
}

// awaitingActivation reports whether a held key becomes active after now.
func (r *Ring) awaitingActivation(now time.Time) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, k := range r.keys {
		if k.ActiveFrom.After(now) {
			return true
		}
	}
	return false
}

// resolveOptions fills the defaults and checks the publish lead against
// the refresh interval.
//
// spec: §7.1 (Normal Flow)
func (r *Ring) resolveOptions() error {
	if r.refresh <= 0 {
		r.refresh = DefaultRefreshInterval
	}
	if r.lead <= 0 {
		r.lead = DefaultPublishLead
	}
	if r.lead <= r.refresh {
		return fmt.Errorf("signingkeys: the publish lead (%s) must be longer than the refresh interval (%s)", r.lead, r.refresh)
	}
	if r.stale <= 0 {
		return errors.New("signingkeys: the stale bound must be positive")
	}
	if r.clock == nil {
		r.clock = func() time.Time { return time.Now().UTC() }
	}
	if r.rand == nil {
		r.rand = rand.Reader
	}
	if r.log == nil {
		r.log = slog.Default()
	}
	return nil
}

// RefreshInterval returns the interval at which Run calls Refresh.
func (r *Ring) RefreshInterval() time.Duration { return r.refresh }

// Run calls Refresh every refresh interval until ctx is done. A failed
// refresh is logged and retried on the next tick.
func (r *Ring) Run(ctx context.Context) {
	tick := time.NewTicker(r.refresh)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := r.Refresh(ctx); err != nil {
				r.log.Warn("signing-key refresh failed",
					"purpose", r.policy.Purpose, "error", err)
			}
		}
	}
}

// Refresh runs one refresh cycle: it writes the next key when the active
// key is due for rotation, deletes the keys whose verification time has
// passed, and reloads the keys. Only a successful reload moves the instant
// the stale bound is measured from. Each failing part is reported, and the
// parts that can run still run.
//
// spec: §7.1 (Normal Flow)
func (r *Ring) Refresh(ctx context.Context) error {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	now := r.clock()
	var errs []error
	if err := r.rotateIfDue(ctx, now); err != nil {
		errs = append(errs, err)
	}
	if n, err := r.store.DeleteExpired(ctx, r.policy.Purpose, now); err != nil {
		errs = append(errs, fmt.Errorf("signingkeys: sweep %q keys: %w", r.policy.Purpose, err))
	} else if n > 0 {
		r.log.Info("signing keys deleted after their verification time",
			"purpose", r.policy.Purpose, "count", n)
	}
	if err := r.reload(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// rotateIfDue writes the next key, active one publish lead from now, when
// the newest key has been active for the rotation interval. A newest key
// that is not active yet is a rotation already published, so nothing is
// written. The write is keyed by the next rotation slot, so when several
// replicas write at once one write succeeds and the others change nothing.
// A ring whose last reload found no key at all, because the rows were
// deleted, writes a first key active at once, as Open does, rather than
// staying without a signing key until a restart. A newest key that carries
// a retirement instant has lost the row of the key that replaced it; the
// ring writes that slot again one publish lead ahead, as a scheduled
// rotation does, because the retired key never signs again.
//
// spec: §7.1 (Normal Flow)
func (r *Ring) rotateIfDue(ctx context.Context, now time.Time) error {
	r.mu.RLock()
	var newest Key
	have := len(r.keys) > 0
	if have {
		newest = r.keys[len(r.keys)-1]
	}
	reloaded := !r.lastReload.IsZero()
	r.mu.RUnlock()
	if !have {
		if !reloaded {
			return nil
		}
		_, _, err := r.writeKey(ctx, 1, now.Add(-r.lead))
		return err
	}
	if !newest.RetireAfter.IsZero() {
		_, _, err := r.writeKey(ctx, newest.RotationSlot+1, now.Add(r.lead))
		return err
	}
	if newest.ActiveFrom.After(now) || now.Before(newest.ActiveFrom.Add(r.policy.RotationInterval)) {
		return nil
	}
	_, _, err := r.writeKey(ctx, newest.RotationSlot+1, now.Add(r.lead))
	return err
}

// RotateNow writes the purpose's next key immediately, even before the
// active key reaches its rotation interval, through the same write as
// rotateIfDue: the key's
// activation instant is one publish lead ahead, so every replica loads it
// before any replica signs with it, and the store retires the key before
// it at that instant. It reloads the keys first, so the slot it writes
// follows the newest key in the store. When another writer takes that slot
// first, it reloads and tries the slot after the winner's, up to
// rotateNowAttempts slots, and then returns ErrRotationContended. It
// returns the key it wrote.
//
// spec: §7.1 (Normal Flow), §15.1 (REST API)
func (r *Ring) RotateNow(ctx context.Context) (Rotation, error) {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	for attempt := 0; attempt < rotateNowAttempts; attempt++ {
		if err := r.reload(ctx); err != nil {
			return Rotation{}, err
		}
		r.mu.RLock()
		var slot int64 = 1
		if n := len(r.keys); n > 0 {
			slot = r.keys[n-1].RotationSlot + 1
		}
		r.mu.RUnlock()
		rec, inserted, err := r.writeKey(ctx, slot, r.clock().Add(r.lead))
		if err != nil {
			return Rotation{}, err
		}
		if !inserted {
			continue
		}
		if err := r.reload(ctx); err != nil {
			// The key is written; this replica loads it on its next refresh,
			// before its activation instant, as every peer does.
			r.log.Warn("signing-key reload after a forced rotation failed",
				"purpose", r.policy.Purpose, "key_id", rec.KeyID, "error", err)
		}
		return Rotation{KeyID: rec.KeyID, RotationSlot: rec.RotationSlot, ActiveFrom: rec.ActiveFrom}, nil
	}
	return Rotation{}, ErrRotationContended
}

// writeKey generates a secret, wraps it, and writes it as the purpose's
// key at slot, active from activeFrom. It returns the record it wrote and
// whether the write took the slot. Losing the slot to another writer is
// not an error.
func (r *Ring) writeKey(ctx context.Context, slot int64, activeFrom time.Time) (Record, bool, error) {
	secret := make([]byte, r.policy.SecretSize)
	if _, err := io.ReadFull(r.rand, secret); err != nil {
		return Record{}, false, fmt.Errorf("signingkeys: generate %q secret: %w", r.policy.Purpose, err)
	}
	defer clear(secret)
	wrapped, err := r.sealer.Seal(ctx, secret)
	if err != nil {
		return Record{}, false, fmt.Errorf("signingkeys: wrap %q secret: %w", r.policy.Purpose, err)
	}
	keyID, err := r.newKeyID(slot)
	if err != nil {
		return Record{}, false, err
	}
	rec := Record{
		Purpose:       r.policy.Purpose,
		KeyID:         keyID,
		Algorithm:     r.policy.Algorithm,
		KEKAlias:      r.policy.KEKAlias,
		WrappedSecret: wrapped,
		RotationSlot:  slot,
		ActiveFrom:    activeFrom,
	}
	inserted, err := r.store.Insert(ctx, rec, r.policy.VerifyFor)
	if err != nil {
		return Record{}, false, fmt.Errorf("signingkeys: write %q key at slot %d: %w", r.policy.Purpose, slot, err)
	}
	if inserted {
		r.log.Info("signing key published",
			"purpose", r.policy.Purpose, "key_id", keyID, "slot", slot, "active_from", activeFrom)
	}
	rec.WrappedSecret = nil
	return rec, inserted, nil
}

// newKeyID returns "<purpose>-<slot>-<8 hex>".
func (r *Ring) newKeyID(slot int64) (string, error) {
	var suffix [4]byte
	if _, err := io.ReadFull(r.rand, suffix[:]); err != nil {
		return "", fmt.Errorf("signingkeys: key id suffix: %w", err)
	}
	return fmt.Sprintf("%s-%d-%s", r.policy.Purpose, slot, hex.EncodeToString(suffix[:])), nil
}

// reload reads the purpose's keys and installs them. A key the ring
// already holds keeps its unwrapped secret, so a reload unwraps only new
// keys. Any read or unwrap failure leaves the installed keys and the
// reload instant unchanged.
func (r *Ring) reload(ctx context.Context) error {
	now := r.clock()
	recs, err := r.store.List(ctx, r.policy.Purpose, now)
	if err != nil {
		return fmt.Errorf("signingkeys: reload %q keys: %w", r.policy.Purpose, err)
	}
	r.mu.RLock()
	held := make(map[string][]byte, len(r.keys))
	for _, k := range r.keys {
		held[k.KeyID] = k.Secret
	}
	r.mu.RUnlock()
	keys := make([]Key, 0, len(recs))
	for _, rec := range recs {
		if rec.Algorithm != r.policy.Algorithm {
			return fmt.Errorf("signingkeys: %q key %s has algorithm %q, want %q",
				r.policy.Purpose, rec.KeyID, rec.Algorithm, r.policy.Algorithm)
		}
		secret, ok := held[rec.KeyID]
		if !ok {
			secret, err = r.sealer.Open(ctx, rec.WrappedSecret)
			if err != nil {
				return fmt.Errorf("signingkeys: unwrap %q key %s: %w", r.policy.Purpose, rec.KeyID, err)
			}
		}
		keys = append(keys, Key{
			KeyID:        rec.KeyID,
			Secret:       secret,
			RotationSlot: rec.RotationSlot,
			ActiveFrom:   rec.ActiveFrom,
			RetireAfter:  rec.RetireAfter,
			DeleteAfter:  rec.DeleteAfter,
		})
	}
	r.mu.Lock()
	r.keys = keys
	r.lastReload = now
	r.mu.Unlock()
	return nil
}

// SigningKey returns the newest key whose activation instant has passed
// and whose retirement instant has not. A retired key is skipped even when
// the row of the key that replaced it is gone, so deleting the newest row
// does not make replicas sign with the key it replaced. It returns
// ErrSigningUnavailable when the last successful reload is older than the
// stale bound, and ErrNoActiveKey when no key is active.
//
// spec: §7.1 (Normal Flow)
func (r *Ring) SigningKey() (Key, error) {
	now := r.clock()
	r.mu.RLock()
	defer r.mu.RUnlock()
	if now.Sub(r.lastReload) > r.stale {
		return Key{}, ErrSigningUnavailable
	}
	for i := len(r.keys) - 1; i >= 0; i-- {
		k := r.keys[i]
		if k.ActiveFrom.After(now) || k.retiredAt(now) {
			continue
		}
		return k, nil
	}
	return Key{}, ErrNoActiveKey
}

// VerificationKeys returns every held key whose verification time has not
// passed, newest first, including a key that is not active yet. A key past
// its DeleteAfter is left out even before the sweep deletes its row.
//
// spec: §7.1 (Normal Flow)
func (r *Ring) VerificationKeys() []Key {
	now := r.clock()
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Key, 0, len(r.keys))
	for i := len(r.keys) - 1; i >= 0; i-- {
		k := r.keys[i]
		if !k.DeleteAfter.IsZero() && !k.DeleteAfter.After(now) {
			continue
		}
		out = append(out, k)
	}
	return out
}
