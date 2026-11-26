// SPDX-License-Identifier: MIT

package signingkeys_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/signingkeys"
)

// fakeClock is a settable clock shared by the rings of one test.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// xorSealer wraps by prefixing a marker and flipping every byte, so a
// wrapped secret never equals its plaintext and Open refuses a value it
// did not produce.
type xorSealer struct{ opens int }

var errNotSealed = errors.New("not sealed by xorSealer")

func (s *xorSealer) Seal(_ context.Context, plain []byte) ([]byte, error) {
	out := append([]byte("xs:"), plain...)
	for i := 3; i < len(out); i++ {
		out[i] ^= 0xff
	}
	return out, nil
}

func (s *xorSealer) Open(_ context.Context, wrapped []byte) ([]byte, error) {
	s.opens++
	if !bytes.HasPrefix(wrapped, []byte("xs:")) {
		return nil, errNotSealed
	}
	out := append([]byte(nil), wrapped[3:]...)
	for i := range out {
		out[i] ^= 0xff
	}
	return out, nil
}

const (
	rotation = 24 * time.Hour
	overlap  = 5 * time.Minute
	refresh  = 30 * time.Second
	lead     = 2 * time.Minute
	tokenTTL = 5 * time.Minute
	// verifyFor is how long a replaced key verifies at these settings: the
	// stale bound (overlap) plus the longer of overlap and tokenTTL.
	verifyFor = overlap + tokenTTL
)

func openRing(t *testing.T, store signingkeys.Store, clock *fakeClock) *signingkeys.Ring {
	t.Helper()
	ring, err := signingkeys.Open(context.Background(), signingkeys.UploadTokenPolicy(rotation, overlap, tokenTTL), store, &xorSealer{},
		signingkeys.Options{RefreshInterval: refresh, PublishLead: lead, StaleAfter: overlap, Clock: clock.Now})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return ring
}

func keyIDs(keys []signingkeys.Key) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k.KeyID
	}
	return out
}

// TestOpenWritesTheFirstKeyAndASecondReplicaLoadsIt checks that the first
// replica to open an empty store writes one key, active at once, and that a
// second replica over the same store loads that key rather than writing its
// own, so both sign with the same secret.
//
// spec: §7.1 (Normal Flow)
func TestOpenWritesTheFirstKeyAndASecondReplicaLoadsIt(t *testing.T) {
	store := signingkeys.NewMemoryStore()
	clock := newClock()
	a := openRing(t, store, clock)
	b := openRing(t, store, clock)
	ka, err := a.SigningKey()
	if err != nil {
		t.Fatal(err)
	}
	kb, err := b.SigningKey()
	if err != nil {
		t.Fatal(err)
	}
	if ka.KeyID != kb.KeyID || !bytes.Equal(ka.Secret, kb.Secret) {
		t.Fatalf("replicas sign with different keys: %s and %s", ka.KeyID, kb.KeyID)
	}
	if len(ka.Secret) != 32 {
		t.Errorf("secret length = %d, want 32", len(ka.Secret))
	}
	recs, _ := store.List(context.Background(), signingkeys.PurposeUploadToken, clock.Now())
	if len(recs) != 1 {
		t.Fatalf("stored keys = %d, want 1", len(recs))
	}
	if bytes.Contains(recs[0].WrappedSecret, ka.Secret) {
		t.Errorf("the stored row carries the plaintext secret")
	}
	if recs[0].KEKAlias != signingkeys.AliasUploadTokenSigning || recs[0].Algorithm != signingkeys.AlgorithmHMACSHA256 {
		t.Errorf("stored key = %+v, want the upload-token alias and algorithm", recs[0])
	}
}

// TestOpenRacingOnAnEmptyStoreProducesOneKey opens several rings at once
// over an empty store: the unique rotation slot lets one write the first
// key, and every ring signs with it.
//
// spec: §7.1 (Normal Flow)
func TestOpenRacingOnAnEmptyStoreProducesOneKey(t *testing.T) {
	store := signingkeys.NewMemoryStore()
	clock := newClock()
	const replicas = 8
	rings := make([]*signingkeys.Ring, replicas)
	var wg sync.WaitGroup
	for i := range rings {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rings[i] = openRing(t, store, clock)
		}()
	}
	wg.Wait()
	recs, _ := store.List(context.Background(), signingkeys.PurposeUploadToken, clock.Now())
	if len(recs) != 1 {
		t.Fatalf("stored keys = %d, want 1", len(recs))
	}
	for i, r := range rings {
		k, err := r.SigningKey()
		if err != nil || k.KeyID != recs[0].KeyID {
			t.Errorf("ring %d signs with %q (%v), want %q", i, k.KeyID, err, recs[0].KeyID)
		}
	}
}

// TestRotationPublishesTheNextKeyAheadOfItsActivation walks one rotation
// across two replicas: when the active key is due, a refresh writes the
// next key one publish lead ahead; both replicas load it and verify with it
// while they still sign with the old key; once it is active both sign with
// it; the old key verifies for verifyFor after that, and is then left out
// and deleted by the sweep.
//
// spec: §7.1 (Normal Flow)
func TestRotationPublishesTheNextKeyAheadOfItsActivation(t *testing.T) {
	ctx := context.Background()
	store := signingkeys.NewMemoryStore()
	clock := newClock()
	a := openRing(t, store, clock)
	b := openRing(t, store, clock)
	first, _ := a.SigningKey()

	// The first key is backdated by the publish lead, so it is due one lead
	// early.
	clock.Advance(rotation - lead - time.Second)
	refreshAll(t, a, b)
	if recs, _ := store.List(ctx, signingkeys.PurposeUploadToken, clock.Now()); len(recs) != 1 {
		t.Fatalf("a key was written before the rotation interval elapsed: %d keys", len(recs))
	}

	clock.Advance(time.Second)
	refreshAll(t, a, b)
	recs, _ := store.List(ctx, signingkeys.PurposeUploadToken, clock.Now())
	if len(recs) != 2 {
		t.Fatalf("stored keys after the rotation is due = %d, want 2", len(recs))
	}
	next := recs[1]
	if !next.ActiveFrom.Equal(clock.Now().Add(lead)) || next.RotationSlot != 2 {
		t.Errorf("next key active from %s at slot %d, want %s at slot 2", next.ActiveFrom, next.RotationSlot, clock.Now().Add(lead))
	}
	if !recs[0].RetireAfter.Equal(next.ActiveFrom) || !recs[0].DeleteAfter.Equal(next.ActiveFrom.Add(verifyFor)) {
		t.Errorf("retired key = retire %s delete %s, want %s and %s", recs[0].RetireAfter, recs[0].DeleteAfter, next.ActiveFrom, next.ActiveFrom.Add(verifyFor))
	}
	for name, r := range map[string]*signingkeys.Ring{"a": a, "b": b} {
		k, err := r.SigningKey()
		if err != nil || k.KeyID != first.KeyID {
			t.Errorf("replica %s signs with %q before the next key is active, want %q", name, k.KeyID, first.KeyID)
		}
		if got := keyIDs(r.VerificationKeys()); len(got) != 2 || got[0] != next.KeyID {
			t.Errorf("replica %s verifies with %v, want the pending key first and the active key", name, got)
		}
	}

	// A refresh while the next key is pending writes nothing more.
	clock.Advance(lead - time.Second)
	refreshAll(t, a, b)
	if recs, _ := store.List(ctx, signingkeys.PurposeUploadToken, clock.Now()); len(recs) != 2 {
		t.Fatalf("a pending rotation was published twice: %d keys", len(recs))
	}

	clock.Advance(time.Second)
	for name, r := range map[string]*signingkeys.Ring{"a": a, "b": b} {
		if k, _ := r.SigningKey(); k.KeyID != next.KeyID {
			t.Errorf("replica %s signs with %q once the next key is active, want %q", name, k.KeyID, next.KeyID)
		}
	}

	clock.Advance(verifyFor - time.Second)
	if got := keyIDs(a.VerificationKeys()); len(got) != 2 {
		t.Errorf("verification keys inside the verification time = %v, want both", got)
	}
	clock.Advance(time.Second)
	if got := keyIDs(a.VerificationKeys()); len(got) != 1 || got[0] != next.KeyID {
		t.Errorf("verification keys after the verification time = %v, want the next key alone", got)
	}
	refreshAll(t, a)
	all, _ := store.List(ctx, signingkeys.PurposeUploadToken, time.Time{})
	if len(all) != 1 || all[0].KeyID != next.KeyID {
		t.Errorf("stored keys after the sweep = %v, want the next key alone", all)
	}
}

func refreshAll(t *testing.T, rings ...*signingkeys.Ring) {
	t.Helper()
	for _, r := range rings {
		if err := r.Refresh(context.Background()); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
	}
}

// TestConcurrentRotationByTwoReplicasProducesOneKey refreshes two replicas
// at once, many times over, at the instant the active key is due: the
// unique rotation slot lets one write succeed, and both replicas load the
// same next key.
//
// spec: §7.1 (Normal Flow)
func TestConcurrentRotationByTwoReplicasProducesOneKey(t *testing.T) {
	store := signingkeys.NewMemoryStore()
	clock := newClock()
	a := openRing(t, store, clock)
	b := openRing(t, store, clock)
	clock.Advance(rotation)
	var wg sync.WaitGroup
	for range 16 {
		for _, r := range []*signingkeys.Ring{a, b} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := r.Refresh(context.Background()); err != nil {
					t.Errorf("Refresh: %v", err)
				}
			}()
		}
	}
	wg.Wait()
	recs, _ := store.List(context.Background(), signingkeys.PurposeUploadToken, clock.Now())
	if len(recs) != 2 {
		t.Fatalf("stored keys = %d, want 2", len(recs))
	}
	if ga, gb := keyIDs(a.VerificationKeys()), keyIDs(b.VerificationKeys()); strings.Join(ga, ",") != strings.Join(gb, ",") {
		t.Errorf("replicas hold different keys: %v and %v", ga, gb)
	}
}

// failingStore is a Store whose reads and writes fail while down is set.
type failingStore struct {
	signingkeys.Store
	mu   sync.Mutex
	down bool
}

var errStoreDown = errors.New("store down")

func (f *failingStore) setDown(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = v
}

func (f *failingStore) isDown() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.down
}

func (f *failingStore) List(ctx context.Context, purpose string, now time.Time) ([]signingkeys.Record, error) {
	if f.isDown() {
		return nil, errStoreDown
	}
	return f.Store.List(ctx, purpose, now)
}

func (f *failingStore) Insert(ctx context.Context, rec signingkeys.Record, verifyFor time.Duration) (bool, error) {
	if f.isDown() {
		return false, errStoreDown
	}
	return f.Store.Insert(ctx, rec, verifyFor)
}

func (f *failingStore) DeleteExpired(ctx context.Context, purpose string, now time.Time) (int, error) {
	if f.isDown() {
		return 0, errStoreDown
	}
	return f.Store.DeleteExpired(ctx, purpose, now)
}

// TestARingThatCannotRefreshStopsSigningAfterTheOverlapWindow checks the
// failure behavior: while the store is unreachable, the ring keeps signing
// until its last successful reload is older than the overlap window, then
// refuses to sign while it keeps verifying, does not rotate, and signs
// again once a reload succeeds.
//
// spec: §7.1 (Normal Flow)
func TestARingThatCannotRefreshStopsSigningAfterTheOverlapWindow(t *testing.T) {
	store := &failingStore{Store: signingkeys.NewMemoryStore()}
	clock := newClock()
	r := openRing(t, store, clock)
	active, _ := r.SigningKey()
	store.setDown(true)

	clock.Advance(overlap)
	if err := r.Refresh(context.Background()); !errors.Is(err, errStoreDown) {
		t.Fatalf("Refresh with the store down = %v, want the store error", err)
	}
	if k, err := r.SigningKey(); err != nil || k.KeyID != active.KeyID {
		t.Fatalf("signing at the overlap window = %q, %v; want the active key", k.KeyID, err)
	}
	clock.Advance(time.Second)
	if _, err := r.SigningKey(); !errors.Is(err, signingkeys.ErrSigningUnavailable) {
		t.Fatalf("signing past the overlap window = %v, want ErrSigningUnavailable", err)
	}
	if got := keyIDs(r.VerificationKeys()); len(got) != 1 || got[0] != active.KeyID {
		t.Errorf("verification keys while stale = %v, want the loaded key", got)
	}

	// A due rotation while the store is down writes nothing.
	clock.Advance(rotation)
	_ = r.Refresh(context.Background())
	store.setDown(false)
	if recs, _ := store.List(context.Background(), signingkeys.PurposeUploadToken, clock.Now()); len(recs) != 1 {
		t.Fatalf("stored keys after the outage = %d, want 1", len(recs))
	}
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh after recovery: %v", err)
	}
	if k, err := r.SigningKey(); err != nil || k.KeyID != active.KeyID {
		t.Errorf("signing after recovery = %q, %v; want the active key until the next key is active", k.KeyID, err)
	}
}

// TestOpenRefusesWhatItCannotLoad checks the startup refusals: a store that
// cannot be read, a stored key that cannot be unwrapped or carries another
// algorithm, a publish lead not longer than the refresh interval, and an
// incomplete policy.
//
// spec: §7.1 (Normal Flow), §12.3 (Postgres HA Requirements)
func TestOpenRefusesWhatItCannotLoad(t *testing.T) {
	ctx := context.Background()
	clock := newClock()
	policy := signingkeys.UploadTokenPolicy(rotation, overlap, tokenTTL)
	opts := signingkeys.Options{RefreshInterval: refresh, PublishLead: lead, StaleAfter: overlap, Clock: clock.Now}

	down := &failingStore{Store: signingkeys.NewMemoryStore(), down: true}
	if _, err := signingkeys.Open(ctx, policy, down, &xorSealer{}, opts); !errors.Is(err, errStoreDown) {
		t.Errorf("unreadable store: %v, want the store error", err)
	}

	foreign := signingkeys.NewMemoryStore()
	if _, err := foreign.Insert(ctx, signingkeys.Record{
		Purpose: signingkeys.PurposeUploadToken, KeyID: "k", Algorithm: signingkeys.AlgorithmHMACSHA256,
		KEKAlias: signingkeys.AliasUploadTokenSigning, WrappedSecret: []byte("plaintext"), RotationSlot: 1, ActiveFrom: clock.Now(),
	}, overlap); err != nil {
		t.Fatal(err)
	}
	if _, err := signingkeys.Open(ctx, policy, foreign, &xorSealer{}, opts); !errors.Is(err, errNotSealed) {
		t.Errorf("unwrappable key: %v, want the unwrap error", err)
	}

	otherAlg := signingkeys.NewMemoryStore()
	sealed, _ := (&xorSealer{}).Seal(ctx, make([]byte, 32))
	if _, err := otherAlg.Insert(ctx, signingkeys.Record{
		Purpose: signingkeys.PurposeUploadToken, KeyID: "k", Algorithm: "ES256",
		KEKAlias: signingkeys.AliasUploadTokenSigning, WrappedSecret: sealed, RotationSlot: 1, ActiveFrom: clock.Now(),
	}, overlap); err != nil {
		t.Fatal(err)
	}
	if _, err := signingkeys.Open(ctx, policy, otherAlg, &xorSealer{}, opts); err == nil || !strings.Contains(err.Error(), "algorithm") {
		t.Errorf("key of another algorithm: %v, want an algorithm error", err)
	}

	short := opts
	short.PublishLead = refresh
	if _, err := signingkeys.Open(ctx, policy, signingkeys.NewMemoryStore(), &xorSealer{}, short); err == nil || !strings.Contains(err.Error(), "publish lead") {
		t.Errorf("publish lead equal to the refresh interval: %v, want a refusal", err)
	}
	noStale := opts
	noStale.StaleAfter = 0
	if _, err := signingkeys.Open(ctx, policy, signingkeys.NewMemoryStore(), &xorSealer{}, noStale); err == nil {
		t.Errorf("no stale bound: want a refusal")
	}
	for name, bad := range map[string]signingkeys.Policy{
		"no purpose":      {Algorithm: signingkeys.AlgorithmHMACSHA256, KEKAlias: "a", SecretSize: 32, RotationInterval: rotation, VerifyFor: overlap},
		"no alias":        {Purpose: "p", Algorithm: signingkeys.AlgorithmHMACSHA256, SecretSize: 32, RotationInterval: rotation, VerifyFor: overlap},
		"no secret size":  {Purpose: "p", Algorithm: signingkeys.AlgorithmHMACSHA256, KEKAlias: "a", RotationInterval: rotation, VerifyFor: overlap},
		"no rotation":     {Purpose: "p", Algorithm: signingkeys.AlgorithmHMACSHA256, KEKAlias: "a", SecretSize: 32, VerifyFor: overlap},
		"no verification": {Purpose: "p", Algorithm: signingkeys.AlgorithmHMACSHA256, KEKAlias: "a", SecretSize: 32, RotationInterval: rotation},
		"asymmetric":      {Purpose: "p", Algorithm: "ES256", KEKAlias: "a", SecretSize: 32, RotationInterval: rotation, VerifyFor: overlap},
	} {
		if _, err := signingkeys.Open(ctx, bad, signingkeys.NewMemoryStore(), &xorSealer{}, opts); err == nil {
			t.Errorf("%s: Open accepted the policy", name)
		}
	}
	if _, err := signingkeys.Open(ctx, policy, nil, &xorSealer{}, opts); err == nil {
		t.Errorf("nil store: want a refusal")
	}
}

// TestAKeyOfOnePurposeNeverServesAnother stores keys of two purposes in one
// store and checks that each ring holds only its own purpose's keys.
//
// spec: §12.3 (Postgres HA Requirements)
func TestAKeyOfOnePurposeNeverServesAnother(t *testing.T) {
	store := signingkeys.NewMemoryStore()
	clock := newClock()
	upload := openRing(t, store, clock)
	other := signingkeys.UploadTokenPolicy(rotation, overlap, tokenTTL)
	other.Purpose = "other"
	otherRing, err := signingkeys.Open(context.Background(), other, store, &xorSealer{},
		signingkeys.Options{PublishLead: lead, StaleAfter: overlap, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := upload.SigningKey()
	o, _ := otherRing.SigningKey()
	if u.KeyID == o.KeyID || bytes.Equal(u.Secret, o.Secret) {
		t.Fatalf("the purposes share a key")
	}
	for _, k := range upload.VerificationKeys() {
		if k.KeyID == o.KeyID {
			t.Errorf("the upload-token ring verifies with the other purpose's key")
		}
	}
	if !strings.HasPrefix(u.KeyID, signingkeys.PurposeUploadToken+"-1-") {
		t.Errorf("key id %q does not name its purpose and slot", u.KeyID)
	}
}

// TestReloadUnwrapsOnlyNewKeys checks that a reload reuses the secret of a
// key the ring already holds, so a refresh costs a KMS unwrap only for a
// key published since the last reload.
//
// spec: §7.1 (Normal Flow)
func TestReloadUnwrapsOnlyNewKeys(t *testing.T) {
	store := signingkeys.NewMemoryStore()
	clock := newClock()
	sealer := &xorSealer{}
	r, err := signingkeys.Open(context.Background(), signingkeys.UploadTokenPolicy(rotation, overlap, tokenTTL), store, sealer,
		signingkeys.Options{RefreshInterval: refresh, PublishLead: lead, StaleAfter: overlap, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	opens := sealer.opens
	for range 3 {
		clock.Advance(refresh)
		refreshAll(t, r)
	}
	if sealer.opens != opens {
		t.Errorf("unwraps during refreshes with no new key = %d, want 0", sealer.opens-opens)
	}
}

// TestRunRefreshesUntilTheContextEnds runs the refresh loop with a short
// interval against a store that counts reads, and waits for the reads
// rather than for a fixed time. It checks the loop's mechanics, which no
// requirement states, so it carries no specification annotation.
func TestRunRefreshesUntilTheContextEnds(t *testing.T) {
	store := &countingStore{Store: signingkeys.NewMemoryStore(), listed: make(chan struct{}, 16)}
	r, err := signingkeys.Open(context.Background(), signingkeys.UploadTokenPolicy(rotation, overlap, tokenTTL), store, &xorSealer{},
		signingkeys.Options{RefreshInterval: time.Millisecond, PublishLead: time.Second, StaleAfter: overlap})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	for range 3 {
		select {
		case <-store.listed:
		case <-time.After(10 * time.Second):
			t.Fatal("the refresh loop did not reload")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// countingStore signals each List on listed without blocking.
type countingStore struct {
	signingkeys.Store
	listed chan struct{}
}

func (c *countingStore) List(ctx context.Context, purpose string, now time.Time) ([]signingkeys.Record, error) {
	select {
	case c.listed <- struct{}{}:
	default:
	}
	return c.Store.List(ctx, purpose, now)
}

// TestEnvelopeSealerRoundTripsUnderTheLocalKMS seals a secret under the
// upload-token alias with the local KMS provider and opens it again; a
// second provider with another master key cannot open it.
//
// spec: §12.3 (Postgres HA Requirements), §12.9 (Data Classification)
func TestEnvelopeSealerRoundTripsUnderTheLocalKMS(t *testing.T) {
	ctx := context.Background()
	provider, err := kms.NewLocal(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := signingkeys.NewEnvelopeSealer(provider, signingkeys.AliasUploadTokenSigning)
	if err != nil {
		t.Fatal(err)
	}
	secret := bytes.Repeat([]byte{0x42}, 32)
	wrapped, err := sealer.Seal(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wrapped, secret) {
		t.Fatal("the wrapped form carries the plaintext")
	}
	got, err := sealer.Open(ctx, wrapped)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("Open = %x, %v; want the secret", got, err)
	}
	other, _ := kms.NewLocal(bytes.Repeat([]byte{9}, 32))
	otherSealer, _ := signingkeys.NewEnvelopeSealer(other, signingkeys.AliasUploadTokenSigning)
	if _, err := otherSealer.Open(ctx, wrapped); err == nil {
		t.Error("a provider with another master key opened the secret")
	}
	if _, err := sealer.Open(ctx, []byte("garbage")); err == nil {
		t.Error("Open accepted a value that is no sealed secret")
	}
}

// TestMemoryStoreKeepsTheFirstWriterOfASlot checks the store contract the
// ring relies on: a second insert at a held slot changes nothing, a
// duplicate key id is refused, and an incomplete record is refused.
//
// spec: §12.3 (Postgres HA Requirements)
func TestMemoryStoreKeepsTheFirstWriterOfASlot(t *testing.T) {
	ctx := context.Background()
	m := signingkeys.NewMemoryStore()
	now := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	rec := signingkeys.Record{Purpose: "p", KeyID: "k1", Algorithm: signingkeys.AlgorithmHMACSHA256, WrappedSecret: []byte("w1"), RotationSlot: 1, ActiveFrom: now}
	if ok, err := m.Insert(ctx, rec, time.Minute); !ok || err != nil {
		t.Fatalf("first insert = %v, %v", ok, err)
	}
	dup := rec
	dup.KeyID = "k2"
	if ok, err := m.Insert(ctx, dup, time.Minute); ok || err != nil {
		t.Errorf("insert at a held slot = %v, %v; want false, nil", ok, err)
	}
	sameID := rec
	sameID.RotationSlot = 2
	if _, err := m.Insert(ctx, sameID, time.Minute); err == nil {
		t.Errorf("duplicate key id: want an error")
	}
	if _, err := m.Insert(ctx, signingkeys.Record{Purpose: "p"}, time.Minute); err == nil {
		t.Errorf("incomplete record: want an error")
	}
	got, _ := m.List(ctx, "p", now)
	got[0].WrappedSecret[0] = 'X'
	again, _ := m.List(ctx, "p", now)
	if again[0].WrappedSecret[0] == 'X' {
		t.Error("List returned the stored bytes rather than a copy")
	}
}

// swappableStore forwards to a store the test can replace, standing for a
// table whose rows were deleted out from under the replicas.
type swappableStore struct {
	mu    sync.Mutex
	inner signingkeys.Store
}

func (s *swappableStore) store() signingkeys.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner
}

func (s *swappableStore) wipe() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inner = signingkeys.NewMemoryStore()
}

func (s *swappableStore) List(ctx context.Context, purpose string, now time.Time) ([]signingkeys.Record, error) {
	return s.store().List(ctx, purpose, now)
}

func (s *swappableStore) Insert(ctx context.Context, rec signingkeys.Record, verifyFor time.Duration) (bool, error) {
	return s.store().Insert(ctx, rec, verifyFor)
}

func (s *swappableStore) DeleteExpired(ctx context.Context, purpose string, now time.Time) (int, error) {
	return s.store().DeleteExpired(ctx, purpose, now)
}

// TestARingWhoseKeysWereDeletedWritesANewFirstKey checks that a ring whose
// reload finds no key, because every row was deleted, writes a new first
// key on its next refresh and signs again, rather than refusing to sign
// until a restart.
//
// spec: §7.1 (Normal Flow)
func TestARingWhoseKeysWereDeletedWritesANewFirstKey(t *testing.T) {
	store := &swappableStore{inner: signingkeys.NewMemoryStore()}
	clock := newClock()
	r := openRing(t, store, clock)
	old, _ := r.SigningKey()
	store.wipe()
	refreshAll(t, r)
	if _, err := r.SigningKey(); !errors.Is(err, signingkeys.ErrNoActiveKey) {
		t.Fatalf("signing with no key held = %v, want ErrNoActiveKey", err)
	}
	refreshAll(t, r)
	k, err := r.SigningKey()
	if err != nil {
		t.Fatalf("signing after the next refresh: %v", err)
	}
	if k.KeyID == old.KeyID {
		t.Errorf("the ring signs with the deleted key %s", k.KeyID)
	}
}
