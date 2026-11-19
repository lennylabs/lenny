// SPDX-License-Identifier: MIT

package admintoken

import (
	"flag"
	"os"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the admin token provisioner and its reclaimer
// read.
type Flags struct {
	// Disabled is the --admin-token-disabled flag.
	Disabled *bool
	// Namespace is the --admin-token-namespace flag.
	Namespace *string
	// SecretName is the --admin-token-secret-name flag.
	SecretName *string
	// Tenant is the --admin-token-tenant flag.
	Tenant *string
	// ReclaimIntervalSeconds is the --admin-token-reclaim-interval-seconds flag.
	ReclaimIntervalSeconds *int
}

// RegisterFlags registers these flags on fs and returns their values, which
// fs fills when it parses. Each flag's default is read from its environment
// variable, as the composition root read it before the flags moved here.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	// §17.6 — the initial admin credential. The
	// bootstrap flow provisions a platform-admin user (lenny-admin) and
	// writes its generated token to a Secret in the gateway namespace.
	// F-17.6.3.
	f.Disabled = fs.Bool("admin-token-disabled", envdefault.ParseBool("LENNY_ADMIN_TOKEN_DISABLED", false),
		"§17.6 disable initial-admin-credential provisioning (no lenny-admin user or lenny-admin-token Secret is created on bootstrap). Override via LENNY_ADMIN_TOKEN_DISABLED.")
	f.Namespace = fs.String("admin-token-namespace", envdefault.String("LENNY_ADMIN_TOKEN_NAMESPACE", os.Getenv("POD_NAMESPACE")),
		"§17.6 namespace for the lenny-admin-token Secret (defaults to the gateway's own namespace / POD_NAMESPACE). Override via LENNY_ADMIN_TOKEN_NAMESPACE.")
	f.SecretName = fs.String("admin-token-secret-name", envdefault.String("LENNY_ADMIN_TOKEN_SECRET_NAME", "lenny-admin-token"),
		"§17.6 name of the initial-admin-token Secret. Override via LENNY_ADMIN_TOKEN_SECRET_NAME.")
	f.Tenant = fs.String("admin-token-tenant", envdefault.String("LENNY_ADMIN_TOKEN_TENANT", "default"),
		"§17.6 tenant the initial lenny-admin user and token are scoped to. Override via LENNY_ADMIN_TOKEN_TENANT.")
	f.ReclaimIntervalSeconds = fs.Int("admin-token-reclaim-interval-seconds", envdefault.Int("LENNY_ADMIN_TOKEN_RECLAIM_INTERVAL_SECONDS", 300),
		"§13.3 cadence in seconds of the leader-gated admin-token reclaimer sweep, which durably revokes a lenny-admin-token predecessor orphaned by a crash between the Secret patch and the in-request revoke (default 300 / 5 minutes). A non-positive value falls back to the default. This bounds the residual crash-window exposure of a superseded admin credential. Override via LENNY_ADMIN_TOKEN_RECLAIM_INTERVAL_SECONDS.")
	return f
}
