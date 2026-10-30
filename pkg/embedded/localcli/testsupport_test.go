// SPDX-License-Identifier: MIT

package localcli

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/lennylabs/lenny/pkg/embedded/devauth"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
)

// seedOIDCKey writes a persisted embedded dev signing key under the given
// Embedded Mode home directory, the way lenny up does. It lets a
// token-print test run without bringing the full stack up.
func seedOIDCKey(t *testing.T, home string) {
	t.Helper()
	paths := stack.NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("seedOIDCKey: EnsureDirs: %v", err)
	}
	if _, err := devauth.NewWithPersistedKey(paths.OIDCKeyFile(), true); err != nil {
		t.Fatalf("seedOIDCKey: write key: %v", err)
	}
}

// seedRunningStackKubeconfig writes a running-stack state record under home
// carrying kubeconfig, so a command that resolves the embedded cluster through
// stack.RunningKubeconfig (the runtime-apply verb) sees a running stack. The
// record is written in the stack.State JSON layout the stack package reads; the
// fields beyond the kubeconfig and the running markers are immaterial to the
// resolution.
func seedRunningStackKubeconfig(t *testing.T, home, kubeconfig string) {
	t.Helper()
	paths := stack.NewPaths(home)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("seedRunningStackKubeconfig: EnsureDirs: %v", err)
	}
	record := map[string]any{
		"k3sEnabled":     true,
		"kubeconfigPath": kubeconfig,
	}
	b, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("seedRunningStackKubeconfig: marshal: %v", err)
	}
	if err := os.WriteFile(paths.StateFile(), b, 0o600); err != nil {
		t.Fatalf("seedRunningStackKubeconfig: write state: %v", err)
	}
}
