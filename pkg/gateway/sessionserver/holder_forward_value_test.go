// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"regexp"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// holderValueShape is `<identity>.<16 hex digits>@<address>`.
var holderValueShape = regexp.MustCompile(`^(.+)\.([0-9a-f]{16})@(.+)$`)

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address
func TestHolderValueCarriesIdentityTokenAndAddress_spec_10_1_1(t *testing.T) {
	cases := []struct {
		name, addr, wantAddr string
	}{
		{"ipv4", "10.0.0.7:50054", "10.0.0.7:50054"},
		{"unbracketed ipv6 is bracketed", "fd00::7:50054", "[fd00::7]:50054"},
		{"bracketed ipv6 is unchanged", "[fd00::7]:50054", "[fd00::7]:50054"},
		{"dns name", "gateway:50054", "gateway:50054"},
		{"leading zero port is normalized", "10.0.0.7:050054", "10.0.0.7:50054"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sessionserver.HolderValue("gw-a", c.addr)
			if err != nil {
				t.Fatalf("HolderValue(%q) error: %v", c.addr, err)
			}
			m := holderValueShape.FindStringSubmatch(got)
			if m == nil || m[1] != "gw-a" || m[3] != c.wantAddr {
				t.Fatalf("HolderValue(%q) = %q, want gw-a.<token>@%s", c.addr, got, c.wantAddr)
			}
			addr, ok := sessionserver.HolderAddress(got)
			if !ok || addr != c.wantAddr {
				t.Errorf("HolderAddress(%q) = %q, %v; want %q, true", got, addr, ok, c.wantAddr)
			}
		})
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address
func TestHolderValueRejectsInvalidAdvertiseAddress_spec_10_1_1(t *testing.T) {
	for _, addr := range []string{
		"", "10.0.0.7", "10.0.0.7:", "10.0.0.7:0", "10.0.0.7:65536", ":50054",
		"[fd00::7:50054", "fd00::7]:50054", "[]:50054", "10.0.0.7:+80", "10.0.0.7:http",
	} {
		if got, err := sessionserver.HolderValue("gw-a", addr); err == nil {
			t.Errorf("HolderValue(%q) = %q, want an error", addr, got)
		}
	}
	if _, err := sessionserver.HolderValue("", "10.0.0.7:50054"); err == nil {
		t.Error("HolderValue with an empty identity succeeded, want an error")
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address
func TestHolderValueDrawsAFreshProcessTokenPerCall_spec_10_1_1(t *testing.T) {
	a, errA := sessionserver.HolderValue("gw-a", "10.0.0.7:50054")
	b, errB := sessionserver.HolderValue("gw-a", "10.0.0.7:50054")
	if errA != nil || errB != nil {
		t.Fatalf("HolderValue errors: %v, %v", errA, errB)
	}
	if a == b {
		t.Fatalf("two calls returned the same holder value %q; the process token must be drawn per call", a)
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address
func TestHolderAddressSplitsAtTheLastAt_spec_10_1_1(t *testing.T) {
	cases := []struct {
		holder, want string
		ok           bool
	}{
		{"gw-a", "", false},
		{"gw-a.0011223344556677", "", false},
		{"gw-a.0011223344556677@", "", false},
		{"gw-a.0011223344556677@10.0.0.7", "", false},
		{"gw-a.0011223344556677@:50054", "", false},
		{"ops@acme.0011223344556677@10.0.0.7:50054", "10.0.0.7:50054", true},
		{"gw-a.0011223344556677@[fd00::7]:50054", "[fd00::7]:50054", true},
	}
	for _, c := range cases {
		got, ok := sessionserver.HolderAddress(c.holder)
		if ok != c.ok || got != c.want {
			t.Errorf("HolderAddress(%q) = %q, %v; want %q, %v", c.holder, got, ok, c.want, c.ok)
		}
	}
}
