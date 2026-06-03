// SPDX-License-Identifier: MIT

package opsservice

import "testing"

// TestSecretCachePutGet is the §25.5 lines 2715-2733 reveal-cache
// contract: a secret stored on create/rotate is recoverable for signing.
func TestSecretCachePutGet_spec_25_5_2715(t *testing.T) {
	c := NewSecretCache()
	if _, ok := c.Secret("sub-1"); ok {
		t.Fatal("Secret on an empty cache returned ok=true")
	}
	c.Put("sub-1", "whsec_abc", 0)
	got, ok := c.Secret("sub-1")
	if !ok || string(got) != "whsec_abc" {
		t.Fatalf("Secret = %q,%v, want whsec_abc,true", got, ok)
	}
	// The returned slice is a copy: mutating it must not corrupt the cache.
	got[0] = 'X'
	again, _ := c.Secret("sub-1")
	if string(again) != "whsec_abc" {
		t.Errorf("cache mutated through returned slice: %q", again)
	}
}

// TestSecretCacheRotateOverwrites confirms a rotated secret replaces the
// prior one. spec: §25.5 line 2729.
func TestSecretCacheRotateOverwrites(t *testing.T) {
	c := NewSecretCache()
	c.Put("sub-1", "old", 0)
	c.Put("sub-1", "new", 1)
	got, _ := c.Secret("sub-1")
	if string(got) != "new" {
		t.Errorf("Secret = %q, want new after rotation", got)
	}
}

// TestSecretCacheRemove confirms a deleted subscription's secret is
// dropped. spec: §25.5 line 2730.
func TestSecretCacheRemove(t *testing.T) {
	c := NewSecretCache()
	c.Put("sub-1", "x", 0)
	c.Remove("sub-1")
	if _, ok := c.Secret("sub-1"); ok {
		t.Error("Secret returned ok=true after Remove")
	}
}

// TestSecretCacheRetainPrunes is the §25.5 line 2752 prune-on-refresh
// contract: Retain drops every cached secret not in the active set.
func TestSecretCacheRetainPrunes(t *testing.T) {
	c := NewSecretCache()
	c.Put("keep", "a", 0)
	c.Put("drop", "b", 0)
	c.Retain([]string{"keep"})
	if _, ok := c.Secret("keep"); !ok {
		t.Error("Retain dropped an active subscription's secret")
	}
	if _, ok := c.Secret("drop"); ok {
		t.Error("Retain kept a removed subscription's secret")
	}
	// Retaining nothing clears the cache.
	c.Retain(nil)
	if _, ok := c.Secret("keep"); ok {
		t.Error("Retain(nil) left a secret cached")
	}
}
