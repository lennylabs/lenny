// SPDX-License-Identifier: MIT

package journeys

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// TokenFileName is the token file's name in the journeys state directory.
const TokenFileName = "claude-token"

// ReadTokenFile reads the `claude setup-token` token the agent container
// authenticates with. The file must exist, hold a token, and be readable
// by its owner alone (mode 0600 or stricter), because the token spends on
// the owner's subscription.
func ReadTokenFile(path string) (string, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("no Claude token at %s. Run `claude setup-token`, save the token it prints in that file, and run `chmod 600 %s`", path, path)
	}
	if err != nil {
		return "", err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("the Claude token file %s has mode %04o; run `chmod 600 %s`", path, fi.Mode().Perm(), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" || strings.ContainsAny(tok, " \t\r\n") {
		return "", fmt.Errorf("the Claude token file %s does not hold one token; save the single line `claude setup-token` prints", path)
	}
	return tok, nil
}
