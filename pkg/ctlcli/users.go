// SPDX-License-Identifier: MIT

package ctlcli

import (
	"context"
	"fmt"
	"io"

	"github.com/lennylabs/lenny/pkg/ctl"
)

// users.go implements the §24.9 `lenny-ctl admin users` resource. For v1
// the only subcommand is `rotate-token`, the §17.6 admin-token
// rotation. The CLI calls the gateway's rotate-token endpoint, which
// mints a fresh token, patches the lenny-admin-token Secret, and revokes
// the prior token. The gateway is the actor because it holds the signer,
// the Kubernetes client, and the issued-token store; the CLI is a thin
// trigger so it does not need kubectl or cluster credentials of its own.

// cmdUsers dispatches the §24.9 `admin users` resource.
func cmdUsers(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: admin users requires a subcommand (rotate-token)")
		return 2
	}
	switch args[0] {
	case "-h", "--help":
		fmt.Fprintln(stdout, usersUsage)
		return 0
	case "rotate-token":
		return cmdUsersRotateToken(ctx, c, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown admin users subcommand %q\n", args[0])
		return 2
	}
}

// rotateTokenResponse mirrors the gateway's rotate-token reply (the §17.6
// admin-token section). The token value is not returned: the credential
// lives only in the Secret, which the operator reads after rotation.
type rotateTokenResponse struct {
	SecretCreated   bool   `json:"secretCreated"`
	SecretNamespace string `json:"secretNamespace"`
	SecretName      string `json:"secretName"`
	Username        string `json:"username"`
}

// cmdUsersRotateToken implements `admin users rotate-token --user <name>`
// (§24.9 line 119). It POSTs to the gateway's
// POST /v1/admin/users/{user}/rotate-token, which mints a new token,
// patches the lenny-admin-token Secret, and immediately revokes the prior
// token (§17.6, no grace period). Requires a platform-admin
// bearer.
//
// spec: §24.9 line 119; §17.6 (rotation procedure).
func cmdUsersRotateToken(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	var user string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			fmt.Fprintln(stdout, usersUsage)
			return 0
		case "--user":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "lenny-ctl: admin users rotate-token --user requires a value")
				return 2
			}
			user, i = args[i+1], i+1
		default:
			fmt.Fprintf(stderr, "lenny-ctl: admin users rotate-token: unknown flag %q\n", args[i])
			return 2
		}
	}
	if user == "" {
		fmt.Fprintln(stderr, "lenny-ctl: admin users rotate-token requires --user <name>")
		return 2
	}

	// §24.9 / §17.6: rotation requires a platform-admin bearer to call the
	// admin API. Without one the gateway would reject with 401, so fail
	// fast with a CLI diagnostic.
	if c.Bearer() == "" {
		fmt.Fprintf(stderr,
			"lenny-ctl: admin users rotate-token: no admin token configured for --api-url %s; "+
				"set --token (or LENNY_API_TOKEN) — rotation requires a platform-admin token\n",
			c.BaseURL())
		return 2
	}

	var resp rotateTokenResponse
	path := "/v1/admin/users/" + user + "/rotate-token"
	if err := c.Do(ctx, "POST", path, nil, &resp); err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: admin users rotate-token: %v\n", err)
		return 1
	}

	ns, name := resp.SecretNamespace, resp.SecretName
	fmt.Fprintf(stdout,
		"Rotated admin token for %q and patched Secret %s/%s. The old token is now invalid.\n",
		user, ns, name)
	if ns != "" && name != "" {
		fmt.Fprintf(stdout,
			"Retrieve the new token with: kubectl get secret %s -n %s -o jsonpath='{.data.token}' | base64 -d\n",
			name, ns)
	}
	return 0
}

const usersUsage = `lenny-ctl admin users — admin user management (§24.9)

Usage:
  lenny-ctl admin users rotate-token --user <name>

rotate-token asks the gateway to mint a fresh admin token, patch the
lenny-admin-token Kubernetes Secret with the new value, and revoke the
prior token immediately. Requires --api-url and a platform-admin token.

Flags:
  --user <name>        The admin user whose token is rotated (required)`
