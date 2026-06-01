// SPDX-License-Identifier: MIT

package localcli

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/embedded/oidc"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
)

// seedOIDCKey writes a persisted embedded OIDC signing key under the
// given Embedded Mode home directory, the way lenny up does. It lets a
// token-print test run without bringing the full stack up.
func seedOIDCKey(t *testing.T, home string) {
	t.Helper()
	paths := stack.NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("seedOIDCKey: EnsureDirs: %v", err)
	}
	if _, err := oidc.NewWithPersistedKey(paths.OIDCKeyFile(), true); err != nil {
		t.Fatalf("seedOIDCKey: write key: %v", err)
	}
}
