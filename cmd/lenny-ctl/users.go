// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/lennylabs/lenny/pkg/ctl"
)

// users.go implements the §24.9 `lenny-ctl admin users` resource. For v1
// the only subcommand is `rotate-token`, which rotates the admin token
// via an RFC 8693 token-exchange against POST /v1/oauth/token and writes
// the new token to the `lenny-admin-token` Kubernetes Secret.

// RFC 8693 grant and token type for the §24.9 token-exchange rotation.
// They match the constants the token service enforces in
// pkg/tokenservice.
const (
	grantTypeTokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange"
	tokenTypeJWT           = "urn:ietf:params:oauth:token-type:jwt"
)

// adminTokenSecretName is the §17.4 Kubernetes Secret holding the
// bootstrap admin token.
const adminTokenSecretName = "lenny-admin-token"

// adminTokenSecretPatcher writes a rotated token into the
// lenny-admin-token Kubernetes Secret. It is a package var so tests can
// substitute a recording stub for the kubectl shell-out.
var adminTokenSecretPatcher = kubectlPatchAdminTokenSecret

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

// cmdUsersRotateToken implements `admin users rotate-token --user <name>`
// (§24.9). It exchanges the caller's current admin token for a fresh one
// (RFC 8693 token-exchange, subject_token = current, requested_token_type
// = same) and patches the lenny-admin-token Kubernetes Secret with the
// new value. The server-side exchange invalidates the old token
// immediately (§17.4), so when the Secret write fails the new token is
// printed prominently with the manual patch command rather than lost.
//
// spec: §24.9 line 119; §17.4 line 472 (rotation procedure).
func cmdUsersRotateToken(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	var user, namespace string
	namespace = "lenny-system"
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
		case "--namespace", "-n":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "lenny-ctl: admin users rotate-token --namespace requires a value")
				return 2
			}
			namespace, i = args[i+1], i+1
		default:
			fmt.Fprintf(stderr, "lenny-ctl: admin users rotate-token: unknown flag %q\n", args[i])
			return 2
		}
	}
	if user == "" {
		fmt.Fprintln(stderr, "lenny-ctl: admin users rotate-token requires --user <name>")
		return 2
	}

	// §24.9 / §17.4: rotation exchanges the caller's current admin token.
	// Without a bearer there is no subject_token to exchange, and the
	// platform-admin register step would 401 server-side anyway, so fail
	// fast with a CLI diagnostic.
	current := c.Bearer()
	if current == "" {
		fmt.Fprintf(stderr,
			"lenny-ctl: admin users rotate-token: no admin token configured for --api-url %s; "+
				"set --token (or LENNY_API_TOKEN) — rotation requires a platform-admin token to exchange\n",
			c.BaseURL())
		return 2
	}

	// spec: §24.9 line 119 — POST /v1/oauth/token with the token-exchange
	// grant; subject_token is the current token, requested_token_type
	// echoes the subject type.
	body := map[string]any{
		"grant_type":           grantTypeTokenExchange,
		"subject_token":        current,
		"subject_token_type":   tokenTypeJWT,
		"requested_token_type": tokenTypeJWT,
	}
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.Do(ctx, "POST", "/v1/oauth/token", body, &resp); err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: admin users rotate-token: token exchange failed: %v\n", err)
		return 1
	}
	if resp.AccessToken == "" {
		fmt.Fprintln(stderr, "lenny-ctl: admin users rotate-token: the token service returned no access_token")
		return 1
	}

	// The exchange already invalidated the old token. If the Secret write
	// fails, the operator must not lose the new token: print it and the
	// manual patch command.
	if err := adminTokenSecretPatcher(namespace, adminTokenSecretName, resp.AccessToken); err != nil {
		fmt.Fprintf(stderr,
			"lenny-ctl: admin users rotate-token: token rotated for %q but the Secret write failed: %v\n",
			user, err)
		fmt.Fprintf(stderr, "New admin token (save it now — the old token is already invalid):\n%s\n", resp.AccessToken)
		fmt.Fprintf(stderr,
			"Patch the Secret manually:\n  kubectl patch secret %s -n %s --type merge -p '{\"stringData\":{\"token\":\"<token>\"}}'\n",
			adminTokenSecretName, namespace)
		return 1
	}

	fmt.Fprintf(stdout,
		"Rotated admin token for %q and patched Secret %s/%s. The old token is now invalid.\n",
		user, namespace, adminTokenSecretName)
	return 0
}

// kubectlPatchAdminTokenSecret patches the lenny-admin-token Secret's
// `token` key with the rotated value via a strategic-merge kubectl patch.
// stringData is used so the API server base64-encodes the value.
func kubectlPatchAdminTokenSecret(namespace, secret, token string) error {
	if _, err := exec.LookPath("kubectl"); err != nil {
		return fmt.Errorf("kubectl not found on PATH: %w", err)
	}
	patch := fmt.Sprintf(`{"stringData":{"token":%q}}`, token)
	out, err := exec.Command("kubectl", "patch", "secret", secret,
		"-n", namespace, "--type", "merge", "-p", patch).CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl patch: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

const usersUsage = `lenny-ctl admin users — admin user management (§24.9)

Usage:
  lenny-ctl admin users rotate-token --user <name> [--namespace <ns>]

rotate-token exchanges the caller's current admin token for a fresh one
(RFC 8693 token-exchange against POST /v1/oauth/token) and patches the
lenny-admin-token Kubernetes Secret with the new value. The old token is
invalidated immediately. Requires --api-url and a platform-admin token.

Flags:
  --user <name>        The admin user whose token is rotated (required)
  --namespace <ns>     Namespace of the lenny-admin-token Secret (default lenny-system)`
