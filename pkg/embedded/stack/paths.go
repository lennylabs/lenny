// SPDX-License-Identifier: MIT

package stack

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths is the §17.4 Embedded Mode state-directory layout. ~/.lenny/
// is the sole state directory; lenny down --purge removes Root.
type Paths struct {
	// Root is the Embedded Mode state directory, ~/.lenny/ by default.
	Root string
	// Postgres holds the embedded Postgres data directory and binary
	// bundle.
	Postgres string
	// K3s holds the downloaded k3s binary, its data directory, and the
	// generated kubeconfig.
	K3s string
	// KMS holds the file-backed soft-HSM master key.
	KMS string
	// OIDC holds the persisted embedded OIDC signing key. lenny token
	// print loads it to mint a bearer the running stack accepts.
	OIDC string
	// Artifacts holds the local-filesystem artifact store.
	Artifacts string
	// TLS holds the self-signed CA and leaf certificate rotated per
	// lenny up.
	TLS string
	// Logs holds per-component log files.
	Logs string
	// Run holds the runtime state file that records the active stack's
	// component endpoints and child PIDs.
	Run string
}

// DefaultRoot returns the default Embedded Mode state directory,
// $HOME/.lenny. The LENNY_HOME environment variable overrides it,
// which keeps tests off the real home directory.
func DefaultRoot() (string, error) {
	if v := os.Getenv("LENNY_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("embedded: resolve home directory: %w", err)
	}
	return filepath.Join(home, ".lenny"), nil
}

// NewPaths derives the state-directory layout under root.
func NewPaths(root string) Paths {
	return Paths{
		Root:      root,
		Postgres:  filepath.Join(root, "postgres"),
		K3s:       filepath.Join(root, "k3s"),
		KMS:       filepath.Join(root, "kms"),
		OIDC:      filepath.Join(root, "oidc"),
		Artifacts: filepath.Join(root, "artifacts"),
		TLS:       filepath.Join(root, "tls"),
		Logs:      filepath.Join(root, "logs"),
		Run:       filepath.Join(root, "run"),
	}
}

// EnsureDirs creates every state subdirectory. It is idempotent.
func (p Paths) EnsureDirs() error {
	for _, d := range []string{p.Root, p.Postgres, p.K3s, p.KMS, p.OIDC, p.Artifacts, p.TLS, p.Logs, p.Run} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("embedded: create %s: %w", d, err)
		}
	}
	return nil
}

// StateFile returns the path of the runtime state file that records
// the active stack's endpoints and child PIDs.
func (p Paths) StateFile() string {
	return filepath.Join(p.Run, "stack.json")
}

// RestartRequestFile returns the path of the §24.19 restart-request
// file. `lenny restart <component>` writes the component name here and
// signals the supervisor with SIGHUP; the supervisor reads it to learn
// which component to restart.
func (p Paths) RestartRequestFile() string {
	return filepath.Join(p.Run, "restart.request")
}

// RestartResultFile returns the path of the §24.19 restart-result file.
// The supervisor writes the outcome of a restart request here so the
// `lenny restart` process, which runs separately, can report success or
// the failure reason.
func (p Paths) RestartResultFile() string {
	return filepath.Join(p.Run, "restart.result")
}

// KMSMasterKey returns the path of the file-backed soft-HSM master
// key. §17.4 warns operators not to reuse this key in production.
func (p Paths) KMSMasterKey() string {
	return filepath.Join(p.KMS, "master.key")
}

// OIDCKeyFile returns the path of the persisted embedded OIDC signing
// key. lenny up rotates it; lenny token print reads it.
func (p Paths) OIDCKeyFile() string {
	return filepath.Join(p.OIDC, "signing.key")
}
