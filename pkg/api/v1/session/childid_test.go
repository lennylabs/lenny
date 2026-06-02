// SPDX-License-Identifier: MIT

package session

import (
	"encoding/hex"
	"testing"
)

// spec: §12.6 line 577 — a delegated child copies the root session's 32-bit
// routing prefix so the whole tree consistent-hashes to one shard. Two ids
// with the same routing prefix select the same SessionShard, so asserting
// prefix equality is the meaningful single-shard-agnostic form of
// "SessionShard(child) == SessionShard(root)".
func TestNewChildID_CopiesRoutingPrefix_spec_12_6_577(t *testing.T) {
	root := NewID()
	child, err := NewChildID(root)
	if err != nil {
		t.Fatalf("NewChildID(%q): %v", root, err)
	}
	if child == root {
		t.Fatalf("child id equals root id %q", root)
	}
	rootPrefix, err := RoutingPrefix(root)
	if err != nil {
		t.Fatalf("RoutingPrefix(root): %v", err)
	}
	childPrefix, err := RoutingPrefix(child)
	if err != nil {
		t.Fatalf("RoutingPrefix(child): %v", err)
	}
	if rootPrefix != childPrefix {
		t.Errorf("child routing prefix %q != root %q (tree would split across shards)", childPrefix, rootPrefix)
	}
	// The remaining bits are random, so the suffix after the prefix differs.
	if child[8:] == root[8:] {
		t.Errorf("child suffix matches root; the non-prefix bits must be random")
	}
	assertUUIDv8(t, child)
}

// spec: §12.6 line 577 — a grandchild built from a child still shares the
// apex root's prefix, so an N-deep tree co-locates.
func TestNewChildID_GrandchildSharesRootPrefix_spec_12_6_577(t *testing.T) {
	root := NewID()
	child, err := NewChildID(root)
	if err != nil {
		t.Fatalf("NewChildID(child): %v", err)
	}
	grandchild, err := NewChildID(child)
	if err != nil {
		t.Fatalf("NewChildID(grandchild): %v", err)
	}
	rp, _ := RoutingPrefix(root)
	gp, _ := RoutingPrefix(grandchild)
	if rp != gp {
		t.Errorf("grandchild prefix %q != root %q", gp, rp)
	}
}

// A malformed or empty root id has no parseable routing prefix and is
// rejected rather than silently producing a tree-splitting random prefix.
func TestNewChildID_MalformedRootRejected(t *testing.T) {
	for _, bad := range []string{"", "nope", "sess_child", "abcd-1234", "abcdefghij-..."} {
		if _, err := NewChildID(bad); err == nil {
			t.Errorf("NewChildID(%q) = nil error, want rejection", bad)
		}
	}
}

// RoutingPrefix returns the leading 32 bits as 8 hex characters.
func TestRoutingPrefix(t *testing.T) {
	id := NewID()
	got, err := RoutingPrefix(id)
	if err != nil {
		t.Fatalf("RoutingPrefix: %v", err)
	}
	if got != id[:8] {
		t.Errorf("RoutingPrefix = %q, want leading group %q", got, id[:8])
	}
}

// assertUUIDv8 checks the RFC 9562 version nibble (8) and variant bits (10)
// the §12.6 layout requires.
func assertUUIDv8(t *testing.T, id string) {
	t.Helper()
	raw, err := hex.DecodeString(stripDashes(id))
	if err != nil || len(raw) != 16 {
		t.Fatalf("id %q is not 16 hex bytes: %v", id, err)
	}
	if raw[6]&0xf0 != 0x80 {
		t.Errorf("version nibble = %#x, want 0x8", raw[6]>>4)
	}
	if raw[8]&0xc0 != 0x80 {
		t.Errorf("variant bits = %#b, want 0b10", raw[8]>>6)
	}
}

func stripDashes(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '-' {
			out = append(out, s[i])
		}
	}
	return string(out)
}
