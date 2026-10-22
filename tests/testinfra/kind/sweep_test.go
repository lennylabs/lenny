// SPDX-License-Identifier: MIT

package kind

import (
	"reflect"
	"regexp"
	"testing"
)

// spec: 5.2 (Pool Configuration and Execution Modes), 4.6.1 (Warm Pool Controller (Pod Lifecycle))
//
// The start-time sweep of a name-selected fixture deletes only the objects
// whose whole name is the fixture's prefix plus its fixed run suffix, skips
// this run's own names, and reports each selected name once.
func TestStaleNamesSelectsOnlyWholeNameMatchesOfTheFixturePrefix(t *testing.T) {
	pattern := regexp.MustCompile(regexp.QuoteMeta("chaos-nonce-only-pool") + `-[0-9a-f]{8}`)
	cases := []struct {
		name  string
		names []string
		keep  []string
		want  []string
	}{
		{name: "empty listing", names: nil, want: nil},
		{
			name:  "leftovers selected in listing order",
			names: []string{"chaos-nonce-only-pool-0a1b2c3d", "echo-pool-sidecar", "chaos-nonce-only-pool-ffffffff"},
			want:  []string{"chaos-nonce-only-pool-0a1b2c3d", "chaos-nonce-only-pool-ffffffff"},
		},
		{
			name:  "current run skipped",
			names: []string{"chaos-nonce-only-pool-0a1b2c3d", "chaos-nonce-only-pool-12345678"},
			keep:  []string{"chaos-nonce-only-pool-12345678"},
			want:  []string{"chaos-nonce-only-pool-0a1b2c3d"},
		},
		{
			name:  "keep that matches nothing is harmless",
			names: []string{"chaos-nonce-only-pool-0a1b2c3d"},
			keep:  []string{"chaos-nonce-only-pool-99999999", ""},
			want:  []string{"chaos-nonce-only-pool-0a1b2c3d"},
		},
		{
			name:  "duplicates reported once",
			names: []string{"chaos-nonce-only-pool-0a1b2c3d", "chaos-nonce-only-pool-0a1b2c3d"},
			want:  []string{"chaos-nonce-only-pool-0a1b2c3d"},
		},
		{
			name: "partial matches rejected",
			names: []string{
				"chaos-nonce-only-pool",                // bare prefix, no run suffix
				"chaos-nonce-only-pool-0a1b2c3d-x7k2p", // member Sandbox named after its pool
				"x-chaos-nonce-only-pool-0a1b2c3d",     // another suite sharing the stem
				"chaos-nonce-only-pool-0A1B2C3D",       // suffix outside the minted alphabet
				"chaos-nonce-only-pool-0a1b2c3",        // suffix too short
				"chaos-nonce-only-pool-0a1b2c3d0",      // suffix too long
				"chaos-nonce-only-runtime-0a1b2c3d",    // the fixture's other prefix
			},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StaleNames(tc.names, pattern, tc.keep...)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("StaleNames(%q, keep=%q) = %q, want %q", tc.names, tc.keep, got, tc.want)
			}
		})
	}
}

// spec: 5.2 (Pool Configuration and Execution Modes)
//
// An empty namespace addresses a cluster-scoped resource type, so the sweep
// passes no -n flag; a namespace is passed ahead of the verb otherwise.
func TestNamespaceArgsOmitsTheFlagForClusterScopedResources(t *testing.T) {
	if got, want := namespaceArgs("", "get", "runtimes"), []string{"get", "runtimes"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("namespaceArgs(\"\") = %q, want %q", got, want)
	}
	if got, want := namespaceArgs("lenny-agents", "get", "sandboxtemplates"),
		[]string{"-n", "lenny-agents", "get", "sandboxtemplates"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("namespaceArgs(lenny-agents) = %q, want %q", got, want)
	}
}
