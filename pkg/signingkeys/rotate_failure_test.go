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

// scriptedStore forwards to a MemoryStore and lets a test make Insert lose
// every slot, fail, or make List fail after a number of successful calls.
type scriptedStore struct {
	*signingkeys.MemoryStore
	mu          sync.Mutex
	loseSlots   bool
	insertErr   error
	listOKLeft  int // List calls that still succeed; negative means unlimited
	listErr     error
	insertCalls int
}

func newScriptedStore() *scriptedStore {
	return &scriptedStore{MemoryStore: signingkeys.NewMemoryStore(), listOKLeft: -1}
}

func (s *scriptedStore) List(ctx context.Context, purpose string, now time.Time) ([]signingkeys.Record, error) {
	s.mu.Lock()
	if s.listOKLeft == 0 {
		s.mu.Unlock()
		return nil, s.listErr
	}
	if s.listOKLeft > 0 {
		s.listOKLeft--
	}
	s.mu.Unlock()
	return s.MemoryStore.List(ctx, purpose, now)
}

func (s *scriptedStore) Insert(ctx context.Context, rec signingkeys.Record, verifyFor time.Duration) (bool, error) {
	s.mu.Lock()
	s.insertCalls++
	lose, err := s.loseSlots, s.insertErr
	s.mu.Unlock()
	if err != nil {
		return false, err
	}
	if lose {
		return false, nil
	}
	return s.MemoryStore.Insert(ctx, rec, verifyFor)
}

func (s *scriptedStore) set(f func(*scriptedStore)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// TestRotateNowReportsContendedSlots pins that a forced rotation whose
// every slot is taken by another writer gives up after a bounded number of
// tries with ErrRotationContended and writes nothing.
//
// spec: §7.1 (Normal Flow), §15.1 (REST API)
func TestRotateNowReportsContendedSlots(t *testing.T) {
	store := newScriptedStore()
	r := openRing(t, store, newClock())
	store.set(func(s *scriptedStore) { s.loseSlots = true; s.insertCalls = 0 })
	if _, err := r.RotateNow(context.Background()); !errors.Is(err, signingkeys.ErrRotationContended) {
		t.Fatalf("RotateNow = %v, want ErrRotationContended", err)
	}
	if store.insertCalls != 3 {
		t.Errorf("slots tried = %d, want 3", store.insertCalls)
	}
}

// TestRotateNowReportsAFailedWrite pins that a forced rotation whose key
// write fails returns the failure.
//
// spec: §7.1 (Normal Flow)
func TestRotateNowReportsAFailedWrite(t *testing.T) {
	store := newScriptedStore()
	r := openRing(t, store, newClock())
	store.set(func(s *scriptedStore) { s.insertErr = errors.New("insert refused") })
	if _, err := r.RotateNow(context.Background()); err == nil {
		t.Fatal("RotateNow with a failing insert: want an error")
	}
}

// TestRotateNowSucceedsWhenTheReloadAfterTheWriteFails pins that a forced
// rotation whose key is written reports the key even when the reload that
// follows fails: the key is in the store, and every replica, this one
// included, loads it on its next refresh before it is active.
//
// spec: §7.1 (Normal Flow), §15.1 (REST API)
func TestRotateNowSucceedsWhenTheReloadAfterTheWriteFails(t *testing.T) {
	store := newScriptedStore()
	clock := newClock()
	r := openRing(t, store, clock)
	// RotateNow's own reload succeeds; the reload after the write fails.
	store.set(func(s *scriptedStore) { s.listOKLeft = 1; s.listErr = errors.New("list refused") })
	rot, err := r.RotateNow(context.Background())
	if err != nil {
		t.Fatalf("RotateNow: %v", err)
	}
	store.set(func(s *scriptedStore) { s.listOKLeft = -1 })
	recs, _ := store.MemoryStore.List(context.Background(), signingkeys.PurposeUploadToken, time.Time{})
	if len(recs) != 2 || recs[1].KeyID != rot.KeyID {
		t.Fatalf("stored keys = %+v, want the first key and %s", recs, rot.KeyID)
	}
	refreshAll(t, r)
	clock.Advance(lead)
	if k, err := r.SigningKey(); err != nil || k.KeyID != rot.KeyID {
		t.Errorf("SigningKey after the next refresh and the lead = %q, %v; want %s", k.KeyID, err, rot.KeyID)
	}
}

// TestOpenRefusesARingWithNoKeyItCanSignWith pins Open's refusal when the
// newest key is retired and the slot after it cannot be written: the ring
// would have no key to sign with and none awaiting activation, so Open
// fails and the gateway does not start.
//
// spec: §7.1 (Normal Flow)
func TestOpenRefusesARingWithNoKeyItCanSignWith(t *testing.T) {
	store := newScriptedStore()
	clock := newClock()
	r := openRing(t, store, clock)
	recs := rotateOnce(t, store.MemoryStore, clock, r)
	store.DeleteKey(signingkeys.PurposeUploadToken, recs[1].KeyID)

	for name, f := range map[string]func(*scriptedStore){
		"the slot write fails":   func(s *scriptedStore) { s.insertErr = errors.New("insert refused") },
		"the slot write is lost": func(s *scriptedStore) { s.insertErr = nil; s.loseSlots = true },
	} {
		t.Run(name, func(t *testing.T) {
			store.set(f)
			_, err := signingkeys.Open(context.Background(), signingkeys.UploadTokenPolicy(rotation, overlap, tokenTTL), store, &xorSealer{},
				signingkeys.Options{RefreshInterval: refresh, PublishLead: lead, StaleAfter: overlap, Clock: clock.Now})
			if err == nil {
				t.Fatal("Open with a retired newest key and no writable slot: want an error")
			}
		})
	}
}
