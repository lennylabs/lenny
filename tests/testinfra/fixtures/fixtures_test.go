// SPDX-License-Identifier: MIT

package fixtures_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/fixtures"
)

// spec: 18.1 (reference fixtures use the cryptography convention)
// diagnosis: A fixture identifier drifted from the §17 / §18.1
//
//	cryptography convention. Tenant names must be acme,
//	globex, initech; user names must use alice/bob/carol.
func TestFixtureNamingConvention(t *testing.T) {
	t.Parallel()

	if fixtures.TenantAcme != "acme" {
		t.Errorf("TenantAcme drift: %q", fixtures.TenantAcme)
	}
	if !strings.HasPrefix(fixtures.UserAlice, "alice@") {
		t.Errorf("UserAlice should start with alice@; got %q", fixtures.UserAlice)
	}
	for _, runtime := range []string{
		fixtures.RuntimeEcho,
		fixtures.RuntimeStreamingEcho,
		fixtures.RuntimeDelegationEcho,
	} {
		if !strings.Contains(runtime, "echo") {
			t.Errorf("bundled runtime should mention echo; got %q", runtime)
		}
	}
}
