// SPDX-License-Identifier: MIT

package breakerstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore"
)

func TestOpenAndGet(t *testing.T) {
	s := breakerstore.NewMemory()
	b := circuitbreaker.Breaker{
		Name:      "rt-emergency",
		State:     circuitbreaker.StateOpen,
		Reason:    "incident",
		LimitTier: circuitbreaker.TierRuntime,
		Scope:     circuitbreaker.Scope{Runtime: "echo"},
	}
	stored, err := s.Open(context.Background(), b)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if stored.State != circuitbreaker.StateOpen {
		t.Errorf("State: %q", stored.State)
	}
	got, err := s.Get(context.Background(), "rt-emergency")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Reason != "incident" {
		t.Errorf("Reason: %q", got.Reason)
	}
}

func TestOpenRejectsScopeChange(t *testing.T) {
	s := breakerstore.NewMemory()
	_, err := s.Open(context.Background(), circuitbreaker.Breaker{
		Name: "rt", State: circuitbreaker.StateOpen,
		LimitTier: circuitbreaker.TierRuntime,
		Scope:     circuitbreaker.Scope{Runtime: "echo"},
	})
	if err != nil {
		t.Fatalf("Open initial: %v", err)
	}
	_, err = s.Open(context.Background(), circuitbreaker.Breaker{
		Name: "rt", State: circuitbreaker.StateOpen,
		LimitTier: circuitbreaker.TierRuntime,
		Scope:     circuitbreaker.Scope{Runtime: "different"},
	})
	if !errors.Is(err, breakerstore.ErrScopeImmutable) {
		t.Errorf("scope change: got %v, want ErrScopeImmutable", err)
	}
}

func TestOpenRejectsInvalidScope(t *testing.T) {
	s := breakerstore.NewMemory()
	_, err := s.Open(context.Background(), circuitbreaker.Breaker{
		Name: "rt", State: circuitbreaker.StateOpen,
		LimitTier: circuitbreaker.TierRuntime,
		Scope:     circuitbreaker.Scope{Pool: "wrong-field"},
	})
	if err == nil {
		t.Error("invalid scope should fail")
	}
}

func TestClose(t *testing.T) {
	s := breakerstore.NewMemory()
	_, _ = s.Open(context.Background(), circuitbreaker.Breaker{
		Name: "rt", State: circuitbreaker.StateOpen,
		LimitTier: circuitbreaker.TierRuntime,
		Scope:     circuitbreaker.Scope{Runtime: "echo"},
	})
	closed, err := s.Close(context.Background(), "rt")
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closed.State != circuitbreaker.StateClosed {
		t.Errorf("State: %q, want closed", closed.State)
	}
}

func TestCloseMissing(t *testing.T) {
	s := breakerstore.NewMemory()
	_, err := s.Close(context.Background(), "missing")
	if !errors.Is(err, breakerstore.ErrNotFound) {
		t.Errorf("Close missing: %v", err)
	}
}

func TestList(t *testing.T) {
	s := breakerstore.NewMemory()
	for _, name := range []string{"b", "a", "c"} {
		_, _ = s.Open(context.Background(), circuitbreaker.Breaker{
			Name: name, State: circuitbreaker.StateOpen,
			LimitTier: circuitbreaker.TierRuntime,
			Scope:     circuitbreaker.Scope{Runtime: "r"},
		})
	}
	rows, _ := s.List(context.Background())
	if len(rows) != 3 || rows[0].Name != "a" || rows[2].Name != "c" {
		t.Errorf("List order: %+v", rows)
	}
}

func TestSnapshotIncludesOnlyOpenBreakers(t *testing.T) {
	s := breakerstore.NewMemory()
	_, _ = s.Open(context.Background(), circuitbreaker.Breaker{
		Name: "open-rt", State: circuitbreaker.StateOpen,
		LimitTier: circuitbreaker.TierRuntime,
		Scope:     circuitbreaker.Scope{Runtime: "r"},
	})
	_, _ = s.Open(context.Background(), circuitbreaker.Breaker{
		Name: "closed-rt", State: circuitbreaker.StateOpen,
		LimitTier: circuitbreaker.TierRuntime,
		Scope:     circuitbreaker.Scope{Runtime: "r2"},
	})
	_, _ = s.Close(context.Background(), "closed-rt")

	snap, _ := s.Snapshot(context.Background())
	if len(snap) != 1 || snap[0].Name != "open-rt" {
		t.Errorf("Snapshot should only contain open breakers: %+v", snap)
	}
}
