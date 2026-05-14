// SPDX-License-Identifier: MIT

package denylist

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAddAndContains(t *testing.T) {
	d := New()
	uri := "spiffe://td/agent/p/pod-1"
	d.Add(uri, time.Now().Add(time.Hour))
	if !d.Contains(uri) {
		t.Errorf("Contains: want true for fresh entry")
	}
	if d.Contains("spiffe://td/agent/p/pod-2") {
		t.Errorf("Contains: want false for unknown URI")
	}
}

func TestAddIgnoresAlreadyExpired(t *testing.T) {
	d := New()
	uri := "spiffe://td/agent/p/pod-1"
	d.Add(uri, time.Now().Add(-time.Hour))
	if d.Contains(uri) {
		t.Errorf("already-expired entry should not be added")
	}
}

func TestAddIgnoresEmptyURI(t *testing.T) {
	d := New()
	d.Add("", time.Now().Add(time.Hour))
	if d.Size() != 0 {
		t.Errorf("empty URI should be ignored, Size=%d", d.Size())
	}
}

func TestContainsExpiresEntries(t *testing.T) {
	now := time.Now()
	clock := &fakeClock{t: atomicTime{}}
	clock.t.Store(now)
	d := New(WithClock(clock.Now))

	uri := "spiffe://td/agent/p/pod-1"
	d.Add(uri, now.Add(time.Minute))
	if !d.Contains(uri) {
		t.Fatalf("Contains: want true before expiry")
	}
	clock.t.Store(now.Add(2 * time.Minute))
	if d.Contains(uri) {
		t.Errorf("Contains: want false after expiry")
	}
	if d.Size() != 0 {
		t.Errorf("Contains should evict expired entry, Size=%d", d.Size())
	}
}

func TestAddIgnoresEarlierExpiryRepublish(t *testing.T) {
	now := time.Now()
	clock := &fakeClock{t: atomicTime{}}
	clock.t.Store(now)
	d := New(WithClock(clock.Now))

	uri := "spiffe://td/agent/p/pod-1"
	later := now.Add(time.Hour)
	earlier := now.Add(time.Minute)
	d.Add(uri, later)
	d.Add(uri, earlier) // republished with earlier TTL — must not shrink lifetime
	clock.t.Store(now.Add(30 * time.Minute))
	if !d.Contains(uri) {
		t.Errorf("republish with earlier expiry should not shrink lifetime")
	}
}

func TestAddExtendsLifetimeOnLaterExpiry(t *testing.T) {
	now := time.Now()
	clock := &fakeClock{t: atomicTime{}}
	clock.t.Store(now)
	d := New(WithClock(clock.Now))

	uri := "spiffe://td/agent/p/pod-1"
	d.Add(uri, now.Add(time.Minute))
	d.Add(uri, now.Add(time.Hour))
	clock.t.Store(now.Add(30 * time.Minute))
	if !d.Contains(uri) {
		t.Errorf("Add with later expiry should extend lifetime")
	}
}

func TestSweepEvictsAllExpired(t *testing.T) {
	now := time.Now()
	clock := &fakeClock{t: atomicTime{}}
	clock.t.Store(now)
	d := New(WithClock(clock.Now))

	for i := 0; i < 5; i++ {
		d.Add(fakeURI(i), now.Add(time.Duration(i+1)*time.Minute))
	}
	clock.t.Store(now.Add(3 * time.Minute))
	n := d.Sweep()
	if n != 3 {
		t.Errorf("Sweep evicted %d, want 3", n)
	}
	if d.Size() != 2 {
		t.Errorf("Size after sweep: want 2, got %d", d.Size())
	}
}

func TestRemove(t *testing.T) {
	d := New()
	uri := "spiffe://td/agent/p/pod-1"
	if d.Remove(uri) {
		t.Errorf("Remove of missing entry should return false")
	}
	d.Add(uri, time.Now().Add(time.Hour))
	if !d.Remove(uri) {
		t.Errorf("Remove of present entry should return true")
	}
	if d.Contains(uri) {
		t.Errorf("Contains after Remove should be false")
	}
}

func TestConcurrentAddContainsSafe(t *testing.T) {
	d := New()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		i := i
		wg.Add(2)
		go func() {
			defer wg.Done()
			d.Add(fakeURI(i), time.Now().Add(time.Hour))
		}()
		go func() {
			defer wg.Done()
			_ = d.Contains(fakeURI(i))
		}()
	}
	wg.Wait()
}

// --- helpers ------------------------------------------------------

type atomicTime struct{ v atomic.Pointer[time.Time] }

func (a *atomicTime) Store(t time.Time) { a.v.Store(&t) }
func (a *atomicTime) Load() time.Time   { return *a.v.Load() }

type fakeClock struct{ t atomicTime }

func (c *fakeClock) Now() time.Time { return c.t.Load() }

func fakeURI(i int) string {
	return "spiffe://td/agent/default/pod-" + string(rune('a'+i%26)) + "-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	const digits = "0123456789"
	var buf [20]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = digits[i%10]
		i /= 10
	}
	return string(buf[n:])
}
