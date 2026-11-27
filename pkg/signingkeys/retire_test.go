// SPDX-License-Identifier: MIT

package signingkeys_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/signingkeys"
)

// rotateOnce advances the clock to the active key's rotation and past the
// next key's activation, refreshing the rings at each step, and returns the
// stored records in slot order.
func rotateOnce(t *testing.T, store *signingkeys.MemoryStore, clock *fakeClock, rings ...*signingkeys.Ring) []signingkeys.Record {
	t.Helper()
	clock.Advance(rotation)
	refreshAll(t, rings...)
	clock.Advance(lead)
	refreshAll(t, rings...)
	recs, _ := store.List(context.Background(), signingkeys.PurposeUploadToken, time.Time{})
	return recs
}

// TestDeletingTheNewestRowDoesNotReviveTheReplacedKey pins the effect of
// deleting the newest key's row by hand while the key it replaced still
// verifies: no replica signs with the replaced key again, the replaced
// key's retirement and deletion instants do not move later when the next
// refresh writes the slot again, and the ring signs once the key it wrote
// becomes active one publish lead later.
//
// spec: §7.1 (Normal Flow), §12.3 (Postgres HA Requirements)
func TestDeletingTheNewestRowDoesNotReviveTheReplacedKey(t *testing.T) {
	ctx := context.Background()
	store := signingkeys.NewMemoryStore()
	clock := newClock()
	r := openRing(t, store, clock)
	recs := rotateOnce(t, store, clock, r)
	if len(recs) != 2 {
		t.Fatalf("stored keys after a rotation = %d, want 2", len(recs))
	}
	replaced, newest := recs[0], recs[1]

	store.DeleteKey(signingkeys.PurposeUploadToken, newest.KeyID)
	// The reload half of the refresh sees the deletion first; the next
	// refresh writes the slot again.
	if err := r.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if k, err := r.SigningKey(); err == nil {
		t.Fatalf("the ring signs with %s after the newest row was deleted, want no signing key", k.KeyID)
	} else if !errors.Is(err, signingkeys.ErrNoActiveKey) {
		t.Fatalf("SigningKey = %v, want ErrNoActiveKey", err)
	}
	refreshAll(t, r)
	recs, _ = store.List(ctx, signingkeys.PurposeUploadToken, time.Time{})
	if len(recs) != 2 || recs[1].RotationSlot != newest.RotationSlot || recs[1].KeyID == newest.KeyID {
		t.Fatalf("stored keys after the slot is written again = %+v, want a new key at slot %d", recs, newest.RotationSlot)
	}
	rewritten := recs[1]
	if !rewritten.ActiveFrom.Equal(clock.Now().Add(lead)) {
		t.Errorf("rewritten key active from %s, want one publish lead ahead, %s", rewritten.ActiveFrom, clock.Now().Add(lead))
	}
	if !recs[0].RetireAfter.Equal(replaced.RetireAfter) || !recs[0].DeleteAfter.Equal(replaced.DeleteAfter) {
		t.Errorf("replaced key retire %s delete %s after the rewrite, want %s and %s unchanged",
			recs[0].RetireAfter, recs[0].DeleteAfter, replaced.RetireAfter, replaced.DeleteAfter)
	}
	if _, err := r.SigningKey(); !errors.Is(err, signingkeys.ErrNoActiveKey) {
		t.Errorf("SigningKey before the rewritten key is active = %v, want ErrNoActiveKey", err)
	}
	clock.Advance(lead)
	if k, err := r.SigningKey(); err != nil || k.KeyID != rewritten.KeyID {
		t.Errorf("SigningKey once the rewritten key is active = %q, %v; want %q", k.KeyID, err, rewritten.KeyID)
	}
}

// TestInsertKeepsAnEarlierRetirementOfThePreviousKey pins the store half of
// the rule on both stores' shared contract, through the memory store: a
// key written at a slot whose predecessor already carries a retirement and
// a deletion instant leaves both in place when they are earlier, and sets
// them when they are unset or later.
//
// spec: §7.1 (Normal Flow), §12.3 (Postgres HA Requirements)
func TestInsertKeepsAnEarlierRetirementOfThePreviousKey(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	rec := func(id string, slot int64, active time.Time) signingkeys.Record {
		return signingkeys.Record{Purpose: "p", KeyID: id, Algorithm: signingkeys.AlgorithmHMACSHA256, WrappedSecret: []byte("w"), RotationSlot: slot, ActiveFrom: active}
	}
	for _, c := range []struct {
		name       string
		second     time.Time
		third      time.Time
		wantRetire time.Time
	}{
		{"a later rewrite keeps the earlier instants", t0.Add(time.Hour), t0.Add(2 * time.Hour), t0.Add(time.Hour)},
		{"an earlier rewrite moves them earlier", t0.Add(2 * time.Hour), t0.Add(time.Hour), t0.Add(time.Hour)},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := signingkeys.NewMemoryStore()
			for _, r := range []signingkeys.Record{rec("k1", 1, t0), rec("k2", 2, c.second)} {
				if ok, err := m.Insert(ctx, r, time.Minute); !ok || err != nil {
					t.Fatalf("insert %s = %v, %v", r.KeyID, ok, err)
				}
			}
			m.DeleteKey("p", "k2")
			if ok, err := m.Insert(ctx, rec("k3", 2, c.third), time.Minute); !ok || err != nil {
				t.Fatalf("insert at the freed slot = %v, %v", ok, err)
			}
			got, _ := m.List(ctx, "p", time.Time{})
			if !got[0].RetireAfter.Equal(c.wantRetire) || !got[0].DeleteAfter.Equal(c.wantRetire.Add(time.Minute)) {
				t.Errorf("k1 retire %s delete %s, want %s and %s", got[0].RetireAfter, got[0].DeleteAfter, c.wantRetire, c.wantRetire.Add(time.Minute))
			}
		})
	}
}

// TestRotateNowWritesTheNextKeyOnePublishLeadAhead pins the forced
// rotation: RotateNow writes the next key before the rotation interval
// elapses, active one publish lead ahead, so a peer that has not reloaded
// keeps signing with the active key, loads the new key on its next refresh
// before the key is active, and every token signed with either key keeps
// verifying on both replicas through the rotation.
//
// spec: §7.1 (Normal Flow), §15.1 (REST API)
func TestRotateNowWritesTheNextKeyOnePublishLeadAhead(t *testing.T) {
	ctx := context.Background()
	store := signingkeys.NewMemoryStore()
	clock := newClock()
	a := openRing(t, store, clock)
	b := openRing(t, store, clock)
	first, _ := a.SigningKey()
	clock.Advance(time.Hour)
	refreshAll(t, a, b)

	rot, err := a.RotateNow(ctx)
	if err != nil {
		t.Fatalf("RotateNow: %v", err)
	}
	if rot.RotationSlot != first.RotationSlot+1 || !rot.ActiveFrom.Equal(clock.Now().Add(lead)) {
		t.Errorf("rotation = slot %d active %s, want slot %d active %s", rot.RotationSlot, rot.ActiveFrom, first.RotationSlot+1, clock.Now().Add(lead))
	}
	for name, r := range map[string]*signingkeys.Ring{"a": a, "b": b} {
		if k, _ := r.SigningKey(); k.KeyID != first.KeyID {
			t.Errorf("replica %s signs with %q during the publish lead, want %q", name, k.KeyID, first.KeyID)
		}
	}
	clock.Advance(refresh)
	refreshAll(t, b)
	for name, r := range map[string]*signingkeys.Ring{"a": a, "b": b} {
		if got := keyIDs(r.VerificationKeys()); len(got) != 2 || got[0] != rot.KeyID {
			t.Errorf("replica %s verifies with %v before the new key is active, want it and the active key", name, got)
		}
	}
	clock.Advance(lead - refresh)
	for name, r := range map[string]*signingkeys.Ring{"a": a, "b": b} {
		if k, _ := r.SigningKey(); k.KeyID != rot.KeyID {
			t.Errorf("replica %s signs with %q once the new key is active, want %q", name, k.KeyID, rot.KeyID)
		}
		if got := keyIDs(r.VerificationKeys()); len(got) != 2 {
			t.Errorf("replica %s verifies with %v after the rotation, want the replaced key too", name, got)
		}
	}
}

// TestRotateNowTwiceReplacesAPendingKey pins the second rotation of the
// incident procedure: a forced rotation while the previous forced rotation
// is still pending writes the slot after it, the pending key is retired at
// the new key's activation instant, and the key that was active at the
// first rotation is retired at the pending key's activation instant.
//
// spec: §7.1 (Normal Flow), §15.1 (REST API)
func TestRotateNowTwiceReplacesAPendingKey(t *testing.T) {
	ctx := context.Background()
	store := signingkeys.NewMemoryStore()
	clock := newClock()
	r := openRing(t, store, clock)
	first, err := r.RotateNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	second, err := r.RotateNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.RotationSlot != first.RotationSlot+1 {
		t.Fatalf("second rotation at slot %d, want %d", second.RotationSlot, first.RotationSlot+1)
	}
	recs, _ := store.List(ctx, signingkeys.PurposeUploadToken, time.Time{})
	if len(recs) != 3 {
		t.Fatalf("stored keys = %d, want 3", len(recs))
	}
	if !recs[0].RetireAfter.Equal(first.ActiveFrom) || !recs[1].RetireAfter.Equal(second.ActiveFrom) {
		t.Errorf("retirements = %s, %s; want %s, %s", recs[0].RetireAfter, recs[1].RetireAfter, first.ActiveFrom, second.ActiveFrom)
	}
	clock.Advance(lead)
	if k, _ := r.SigningKey(); k.KeyID != second.KeyID {
		t.Errorf("signing key after both activations = %q, want %q", k.KeyID, second.KeyID)
	}
}

// TestConcurrentForcedRotationsEachWriteAKey runs forced rotations on two
// replicas at once: the slot race is settled by the store, the loser writes
// the following slot, and every call reports a distinct key.
//
// spec: §7.1 (Normal Flow), §15.1 (REST API)
func TestConcurrentForcedRotationsEachWriteAKey(t *testing.T) {
	ctx := context.Background()
	store := signingkeys.NewMemoryStore()
	clock := newClock()
	rings := []*signingkeys.Ring{openRing(t, store, clock), openRing(t, store, clock)}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[string]bool{}
	)
	for i := 0; i < 4; i++ {
		for _, r := range rings {
			wg.Add(1)
			go func(r *signingkeys.Ring) {
				defer wg.Done()
				rot, err := r.RotateNow(ctx)
				if errors.Is(err, signingkeys.ErrRotationContended) {
					return
				}
				if err != nil {
					t.Errorf("RotateNow: %v", err)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if seen[rot.KeyID] {
					t.Errorf("key %s reported by two rotations", rot.KeyID)
				}
				seen[rot.KeyID] = true
			}(r)
		}
	}
	wg.Wait()
	recs, _ := store.List(ctx, signingkeys.PurposeUploadToken, time.Time{})
	if len(recs) != len(seen)+1 {
		t.Errorf("stored keys = %d, want the first key plus the %d reported rotations", len(recs), len(seen))
	}
	for i := 1; i < len(recs); i++ {
		if recs[i].RotationSlot != recs[i-1].RotationSlot+1 {
			t.Errorf("slots %d and %d are not consecutive", recs[i-1].RotationSlot, recs[i].RotationSlot)
		}
	}
}

// TestRotateNowReportsAStoreFailure pins that a forced rotation that cannot
// read or write the store fails and writes nothing.
//
// spec: §7.1 (Normal Flow)
func TestRotateNowReportsAStoreFailure(t *testing.T) {
	store := &failingStore{Store: signingkeys.NewMemoryStore()}
	clock := newClock()
	r := openRing(t, store, clock)
	store.setDown(true)
	if _, err := r.RotateNow(context.Background()); err == nil {
		t.Fatal("RotateNow with the store down: want an error")
	}
	store.setDown(false)
	recs, _ := store.List(context.Background(), signingkeys.PurposeUploadToken, time.Time{})
	if len(recs) != 1 {
		t.Errorf("stored keys = %d, want the first key alone", len(recs))
	}
}

// TestOpenAfterTheNewestRowWasDeletedStartsWithoutASigningKey pins the
// startup half: a replica that opens while the newest key's row is gone
// writes the slot again one publish lead ahead and starts, signing nothing
// until that key is active, rather than refusing to start or signing with
// the replaced key.
//
// spec: §7.1 (Normal Flow)
func TestOpenAfterTheNewestRowWasDeletedStartsWithoutASigningKey(t *testing.T) {
	store := signingkeys.NewMemoryStore()
	clock := newClock()
	r := openRing(t, store, clock)
	recs := rotateOnce(t, store, clock, r)
	store.DeleteKey(signingkeys.PurposeUploadToken, recs[1].KeyID)

	fresh := openRing(t, store, clock)
	if _, err := fresh.SigningKey(); !errors.Is(err, signingkeys.ErrNoActiveKey) {
		t.Fatalf("SigningKey right after Open = %v, want ErrNoActiveKey", err)
	}
	clock.Advance(lead)
	if k, err := fresh.SigningKey(); err != nil || k.KeyID == recs[0].KeyID || k.KeyID == recs[1].KeyID {
		t.Errorf("SigningKey after the publish lead = %q, %v; want the rewritten key", k.KeyID, err)
	}
}
