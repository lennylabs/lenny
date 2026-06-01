// SPDX-License-Identifier: MIT

package localcli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/embedded/oidc"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
)

// cmdToken implements `lenny token print`. §17.4: lenny token print
// emits a short-lived bearer for the embedded built-in user. The token
// is signed by the embedded OIDC provider's persisted key, which the
// running stack rotates on every lenny up.
//
// The embedded stack starts the gateway with
// --bearer-trust-hmac-key-file pointed at the embedded OIDC provider's
// persisted key, so the gateway trusts this provider's key as an
// additional §10.2 Bearer verifier. The printed token verifies on the
// gateway's Authorization header:
//
//	curl -H "Authorization: Bearer $(lenny token print)" https://localhost:8443/...
//
// spec: §24.9 line 120 — exits 0 on success; exits
// 3 EMBEDDED_MODE_REQUIRED when invoked outside Embedded Mode (no
// `lenny up` has written the persisted signing key, or the binary is
// run as lenny-ctl against a remote gateway).
func cmdToken(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "print" {
		fmt.Fprintln(stderr, "lenny token: the only subcommand is 'print'")
		fmt.Fprintln(stderr, "usage: lenny token print [--ttl <duration>]")
		return 2
	}
	ttl := oidc.DefaultTokenTTL
	for i := 1; i < len(args); i++ {
		if args[i] == "--ttl" && i+1 < len(args) {
			d, err := parseTTL(args[i+1])
			if err != nil {
				fmt.Fprintf(stderr, "lenny token: %v\n", err)
				return 2
			}
			ttl = d
			i++
		}
	}

	root, err := stack.DefaultRoot()
	if err != nil {
		fmt.Fprintf(stderr, "lenny token: %v\n", err)
		return 1
	}
	paths := stack.NewPaths(root)
	keyFile := paths.OIDCKeyFile()
	// The persisted signing key exists only after `lenny up`. Its
	// absence is the §24.9 "outside Embedded Mode" condition, which
	// exits 3 EMBEDDED_MODE_REQUIRED. Probing for the file first also
	// avoids minting a token against a freshly generated key that no
	// running gateway trusts.
	if _, err := os.Stat(keyFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(stderr, "lenny token: no embedded stack found; run 'lenny up' first (EMBEDDED_MODE_REQUIRED)")
			return exitEmbeddedModeRequired
		}
		fmt.Fprintf(stderr, "lenny token: %v\n", err)
		return 1
	}
	// Load the running stack's persisted signing key. rotate=false
	// reuses the key lenny up wrote.
	provider, err := oidc.NewWithPersistedKey(keyFile, false)
	if err != nil {
		fmt.Fprintf(stderr, "lenny token: %v\n", err)
		return 1
	}
	tok, err := provider.Issue(ttl)
	if err != nil {
		fmt.Fprintf(stderr, "lenny token: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, tok)
	return 0
}

// parseTTL parses a --ttl value as a Go duration. A non-positive
// duration is rejected.
func parseTTL(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --ttl %q: %w", s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("--ttl must be positive, got %q", s)
	}
	return d, nil
}
