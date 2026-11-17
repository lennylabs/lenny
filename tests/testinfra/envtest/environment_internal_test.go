// SPDX-License-Identifier: MIT

package envtest

import (
	"slices"
	"testing"
)

// TestEnvironmentAdvertisesLoopback pins the API server's advertise
// address to 127.0.0.1, where envtest serves it. Without it the API
// server derives the address from the host's default route and refuses
// to start when that address is IPv6 while the service range is IPv4,
// which is the network the merge gate's container shares under Docker
// Desktop. The helper is test tooling with no specification section, so
// the test carries no spec annotation.
func TestEnvironmentAdvertisesLoopback(t *testing.T) {
	env := newEnvironment(t, t.TempDir())
	got := env.ControlPlane.GetAPIServer().Configure().Get("advertise-address").Get(nil)
	if !slices.Equal(got, []string{"127.0.0.1"}) {
		t.Errorf("advertise-address = %v, want [127.0.0.1]", got)
	}
}
