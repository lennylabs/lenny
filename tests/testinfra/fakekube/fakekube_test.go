// SPDX-License-Identifier: MIT

package fakekube

import (
	"testing"
	"time"
)

func TestSurfacePutGetDelete(t *testing.T) {
	s := New()
	if s.Len() != 0 {
		t.Fatalf("Len=%d want 0", s.Len())
	}
	s.Put("a", []byte("hello"))
	body, ok := s.Get("a")
	if !ok || string(body) != "hello" {
		t.Fatalf("Get: ok=%v body=%q", ok, body)
	}
	if s.Len() != 1 {
		t.Fatalf("Len=%d want 1", s.Len())
	}
	s.Delete("a")
	if _, ok := s.Get("a"); ok {
		t.Fatal("Get after Delete: still present")
	}
	if s.Len() != 0 {
		t.Fatalf("Len=%d want 0 after Delete", s.Len())
	}
}

func TestSurfaceWatchLagPersists(t *testing.T) {
	s := New()
	s.SetWatchLag(50 * time.Millisecond)
	if got := s.WatchLag(); got != 50*time.Millisecond {
		t.Errorf("WatchLag=%v want 50ms", got)
	}
}
