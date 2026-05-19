// SPDX-License-Identifier: MIT

package stack

import (
	"strings"
	"testing"
)

// argValue returns the value following flag in args, or "" when the
// flag is absent.
func argValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func TestGatewayArgsBaseFlags(t *testing.T) {
	args := gatewayArgs(gatewaySpec{HTTPAddr: "127.0.0.1:8080"})
	joined := strings.Join(args, " ")
	for _, want := range []string{"-dev-mode", "-multi-tenant", "-addr 127.0.0.1:8080"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
}

func TestGatewayArgsOmitsBearerTrustWhenNoKeyFile(t *testing.T) {
	args := gatewayArgs(gatewaySpec{HTTPAddr: "127.0.0.1:8080"})
	if _, ok := argValue(args, "-bearer-trust-hmac-key-file"); ok {
		t.Error("gatewayArgs passed -bearer-trust-hmac-key-file without an OIDC key file")
	}
}

func TestGatewayArgsPassesBearerTrustKeyFile(t *testing.T) {
	const keyFile = "/home/alice/.lenny/oidc/signing.key"
	args := gatewayArgs(gatewaySpec{HTTPAddr: "127.0.0.1:8080", OIDCKeyFile: keyFile})
	got, ok := argValue(args, "-bearer-trust-hmac-key-file")
	if !ok {
		t.Fatal("gatewayArgs did not pass -bearer-trust-hmac-key-file")
	}
	if got != keyFile {
		t.Errorf("-bearer-trust-hmac-key-file = %q, want %q", got, keyFile)
	}
}
