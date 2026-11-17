// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AgentGrant is what a scenario's credentials give the agent container.
type AgentGrant struct {
	// Env is the container environment the credentials add.
	Env map[string]string
	// GatewayForward asks the entrypoint to forward the gateway into the
	// container through the cluster's API server.
	GatewayForward bool
	// Secrets are the values the records must never show.
	Secrets []string
}

// AdminTokenReader reads the gateway's initial admin token.
type AdminTokenReader interface {
	AdminToken(ctx context.Context) (string, error)
}

// GrantCredentials writes the kubeconfig files of the agent container's
// read-only /journey mount into journeyDir and resolves the scenario's
// credentials into the container's environment.
//
//   - kubeconfig: KUBECONFIG names the endpoint's kubeconfig, whose server
//     is reachable from the container's network only.
//   - gateway-url: LENNY_API_URL names 127.0.0.1:8080 inside the
//     container, which the entrypoint forwards to the gateway Service
//     through the API server, using a harness copy of the kubeconfig.
//   - admin-token: LENNY_API_TOKEN holds the token of the lenny-admin-token
//     Secret.
//
// Without the kubeconfig credential, KUBECONFIG names an empty file, so
// kubectl and helm in the container hold no cluster. Every file is
// written 0400; LockDir makes the directory read-only afterwards.
func GrantCredentials(ctx context.Context, names []string, ep AgentEndpoint, journeyDir string, tokens AdminTokenReader) (AgentGrant, error) {
	g := AgentGrant{Env: map[string]string{"KUBECONFIG": ContainerEmptyConfig}, Secrets: KubeconfigSecretsFrom(ep.Kubeconfig)}
	if err := os.MkdirAll(journeyDir, 0o700); err != nil {
		return g, err
	}
	if err := writeReadOnly(filepath.Join(journeyDir, filepath.Base(ContainerEmptyConfig)), nil); err != nil {
		return g, err
	}
	for _, n := range names {
		switch n {
		case CredKubeconfig:
			if err := writeReadOnly(filepath.Join(journeyDir, filepath.Base(ContainerKubeconfig)), ep.Kubeconfig); err != nil {
				return g, err
			}
			g.Env["KUBECONFIG"] = ContainerKubeconfig
		case CredGatewayURL:
			if err := writeReadOnly(filepath.Join(journeyDir, filepath.Base(ContainerHarnessConfig)), ep.Kubeconfig); err != nil {
				return g, err
			}
			g.Env["LENNY_API_URL"] = ContainerGatewayURL
			g.GatewayForward = true
		case CredAdminToken:
			tok, err := tokens.AdminToken(ctx)
			if err != nil {
				return g, err
			}
			g.Env["LENNY_API_TOKEN"] = tok
			g.Secrets = append(g.Secrets, tok)
		default:
			return g, errors.New("unknown credential " + n)
		}
	}
	return g, nil
}

func writeReadOnly(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o400); err != nil {
		return err
	}
	return os.Chmod(path, 0o400)
}

// LockDir makes dir read-only (0500), and UnlockDir restores it (0700)
// so the trial's scratch directory can be removed.
func LockDir(dir string) error { return os.Chmod(dir, 0o500) }

// UnlockDir restores the owner's write access to dir.
func UnlockDir(dir string) error {
	if err := os.Chmod(dir, 0o700); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("restore %s: %w", dir, err)
	}
	return nil
}
