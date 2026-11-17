// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// These tests exercise contributor tooling and carry no spec annotation.

type tokenReader struct {
	tok string
	err error
}

func (r tokenReader) AdminToken(context.Context) (string, error) { return r.tok, r.err }

const internalKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster: {server: "https://lenny-journeys-control-plane:6443"}
  name: kind-lenny-journeys
users:
- name: kind-lenny-journeys
  user:
    client-certificate-data: Y2VydGlmaWNhdGUtZGF0YQ==
    client-key-data: a2V5LWRhdGEtc2VjcmV0
`

func TestGrantCredentialsWritesReadOnlyKubeconfigsAndNamesOnlyGrantedValues(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journey")
	ep := AgentEndpoint{Network: KindNetwork, Kubeconfig: []byte(internalKubeconfig)}

	g, err := GrantCredentials(context.Background(), nil, ep, dir, tokenReader{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Env["KUBECONFIG"] != ContainerEmptyConfig || len(g.Env) != 1 || g.GatewayForward {
		t.Errorf("without credentials the agent gets %v", g.Env)
	}
	if _, err := os.Stat(filepath.Join(dir, "kubeconfig")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an ungranted kubeconfig was written: %v", err)
	}

	dir2 := filepath.Join(t.TempDir(), "journey")
	g, err = GrantCredentials(context.Background(), []string{CredKubeconfig, CredGatewayURL, CredAdminToken}, ep, dir2, tokenReader{tok: "admin-token-value"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Env["KUBECONFIG"] != ContainerKubeconfig || g.Env["LENNY_API_URL"] != ContainerGatewayURL || g.Env["LENNY_API_TOKEN"] != "admin-token-value" || !g.GatewayForward {
		t.Errorf("grant = %+v", g)
	}
	for _, f := range []string{"kubeconfig", "harness-kubeconfig", "empty-kubeconfig"} {
		fi, err := os.Stat(filepath.Join(dir2, f))
		if err != nil || fi.Mode().Perm() != 0o400 {
			t.Errorf("%s: mode %v, %v; want 0400", f, fi, err)
		}
	}
	secrets := map[string]bool{}
	for _, s := range g.Secrets {
		secrets[s] = true
	}
	for _, want := range []string{"admin-token-value", "a2V5LWRhdGEtc2VjcmV0", "key-data-secret"} {
		if !secrets[want] {
			t.Errorf("the grant does not report %q as a secret", want)
		}
	}
	if _, err := GrantCredentials(context.Background(), []string{CredAdminToken}, ep, t.TempDir(), tokenReader{err: errors.New("no secret")}); err == nil {
		t.Error("a missing admin token was granted")
	}
	if _, err := GrantCredentials(context.Background(), []string{"aws"}, ep, t.TempDir(), tokenReader{}); err == nil {
		t.Error("an unknown credential was granted")
	}
}

func TestLockDirMakesTheKubeconfigUnwritable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("not-yet-applicable: root ignores file modes, so the write refusal cannot be observed")
	}
	dir := filepath.Join(t.TempDir(), "journey")
	if _, err := GrantCredentials(context.Background(), []string{CredKubeconfig}, AgentEndpoint{Kubeconfig: []byte(internalKubeconfig)}, dir, tokenReader{}); err != nil {
		t.Fatal(err)
	}
	if err := LockDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kubeconfig"), []byte("x"), 0o600); err == nil {
		t.Error("the kubeconfig is writable")
	}
	if err := os.WriteFile(filepath.Join(dir, "new"), []byte("x"), 0o600); err == nil {
		t.Error("the directory is writable")
	}
	if err := UnlockDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Errorf("the unlocked directory cannot be removed: %v", err)
	}
}
