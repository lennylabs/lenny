// SPDX-License-Identifier: MIT

package chaos

import (
	"strings"
	"testing"
	"time"
)

// spec: §12.8 (chaos-mesh NetworkChaos manifests)
// diagnosis: the latency-injection helper must render a NetworkChaos
// CR that selects pods by `app=<label>` in the supplied namespace and
// applies the latency to every TCP packet. The cleanup deletes the CR
// by name.
func TestNetworkChaosLatencyManifest(t *testing.T) {
	m := networkChaosLatencyManifest("lenny-latency-redis", "lenny-system", "redis", 250*time.Millisecond)
	for _, want := range []string{
		"apiVersion: chaos-mesh.org/v1alpha1",
		"kind: NetworkChaos",
		"name: lenny-latency-redis",
		"namespace: lenny-system",
		"action: delay",
		"app: redis",
		`latency: "250ms"`,
	} {
		if !strings.Contains(m, want) {
			t.Errorf("manifest missing %q:\n%s", want, m)
		}
	}
}

// spec: §12.8 (chaos-mesh NetworkChaos partition)
// diagnosis: the partition helper must render a NetworkChaos CR with
// bidirectional direction and a duration so the controller heals the
// fault when the test finishes.
func TestNetworkChaosPartitionManifest(t *testing.T) {
	m := networkChaosPartitionManifest("lenny-partition-postgres", "lenny-system", "postgres", 30*time.Second)
	for _, want := range []string{
		"action: partition",
		"direction: both",
		"app: postgres",
		`duration: "30s"`,
	} {
		if !strings.Contains(m, want) {
			t.Errorf("manifest missing %q:\n%s", want, m)
		}
	}
}

// spec: §12.8 (chaos target naming convention)
// diagnosis: callers supply <namespace>/<app-label>. Malformed targets
// must be rejected so a test does not silently inject into the wrong
// namespace.
func TestParseChaosMeshTarget(t *testing.T) {
	cases := []struct {
		in   string
		ns   string
		app  string
		fail bool
	}{
		{"lenny-system/redis", "lenny-system", "redis", false},
		{"lenny-agents/echo", "lenny-agents", "echo", false},
		{"missing-slash", "", "", true},
		{"/no-namespace", "", "", true},
		{"no-app/", "", "", true},
		{"", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			ns, app, err := parseChaosMeshTarget(c.in)
			if c.fail {
				if err == nil {
					t.Errorf("expected error for %q, got ns=%q app=%q", c.in, ns, app)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error for %q: %v", c.in, err)
			}
			if ns != c.ns || app != c.app {
				t.Errorf("parseChaosMeshTarget(%q) = (%q, %q), want (%q, %q)", c.in, ns, app, c.ns, c.app)
			}
		})
	}
}

// spec: §12.8 (cleanup convention)
// diagnosis: the chaos-resource name is stable per app so a retry
// against the same target overwrites rather than accumulating.
func TestChaosResourceName(t *testing.T) {
	if got := chaosResourceName("lenny-latency", "redis"); got != "lenny-latency-redis" {
		t.Errorf("chaosResourceName = %q, want lenny-latency-redis", got)
	}
}
