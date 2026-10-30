// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunGetEmitsCredential_spec_26_2_119 pins the §26.2 get path: the
// helper reads the protocol/host attributes, mints a token, and writes
// the Git username/password response. F-26.2.5.
func TestRunGetEmitsCredential_spec_26_2_119(t *testing.T) {
	var gotHost, gotMode string
	mint := func(_ context.Context, host, mode string) (string, string, error) {
		gotHost, gotMode = host, mode
		return "x-access-token", "ghs_secret", nil
	}
	var out strings.Builder
	in := strings.NewReader("protocol=https\nhost=github.com\npath=acme/repo.git\n\n")

	if err := Run(context.Background(), []string{"get"}, in, &out, "read", mint); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotHost != "github.com" || gotMode != "read" {
		t.Fatalf("mint called with (%q,%q), want (github.com, read)", gotHost, gotMode)
	}
	got := out.String()
	for _, want := range []string{"username=x-access-token", "password=ghs_secret", "host=github.com", "protocol=https"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q missing %q", got, want)
		}
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Errorf("output must terminate with a blank line, got %q", got)
	}
}

// TestRunDeclinesNonHTTPS_spec_26_2_119 verifies a non-HTTPS protocol
// produces no output and never calls the minter (gitClone is HTTPS-only
// in v1). F-26.2.5.
func TestRunDeclinesNonHTTPS_spec_26_2_119(t *testing.T) {
	called := false
	mint := func(context.Context, string, string) (string, string, error) { called = true; return "", "", nil }
	var out strings.Builder

	if err := Run(context.Background(), []string{"get"}, strings.NewReader("protocol=ssh\nhost=github.com\n\n"), &out, "read", mint); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("declined protocol produced output: %q", out.String())
	}
	if called {
		t.Error("minter was called for a non-HTTPS protocol")
	}
}

// TestRunDeclinesMissingHost_spec_26_2_119 verifies an absent host
// declines silently. F-26.2.5.
func TestRunDeclinesMissingHost_spec_26_2_119(t *testing.T) {
	called := false
	mint := func(context.Context, string, string) (string, string, error) { called = true; return "", "", nil }
	var out strings.Builder

	if err := Run(context.Background(), []string{"get"}, strings.NewReader("protocol=https\n\n"), &out, "read", mint); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Len() != 0 || called {
		t.Errorf("missing host should decline: out=%q called=%v", out.String(), called)
	}
}

// TestRunStoreAndEraseAreNoOps_spec_26_2_119 verifies the store and erase
// operations produce no output and never reach the gateway, since the
// gateway is the authoritative credential store. F-26.2.5.
func TestRunStoreAndEraseAreNoOps_spec_26_2_119(t *testing.T) {
	for _, op := range []string{"store", "erase", "unknown"} {
		called := false
		mint := func(context.Context, string, string) (string, string, error) { called = true; return "", "", nil }
		var out strings.Builder
		if err := Run(context.Background(), []string{op}, strings.NewReader("protocol=https\nhost=github.com\n\n"), &out, "read", mint); err != nil {
			t.Fatalf("Run %s: %v", op, err)
		}
		if out.Len() != 0 || called {
			t.Errorf("op %q: out=%q called=%v, want no-op", op, out.String(), called)
		}
	}
}

// TestRunDeclinesEmptyToken_spec_26_2_119 verifies the helper declines
// rather than feeding Git an empty password when no token resolves.
// F-26.2.5.
func TestRunDeclinesEmptyToken_spec_26_2_119(t *testing.T) {
	mint := func(context.Context, string, string) (string, string, error) { return "", "", nil }
	var out strings.Builder
	if err := Run(context.Background(), []string{"get"}, strings.NewReader("protocol=https\nhost=github.com\n\n"), &out, "read", mint); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("empty token should decline, got %q", out.String())
	}
}

func TestParseGitAttributes(t *testing.T) {
	attrs, err := parseGitAttributes(strings.NewReader("protocol=https\nhost=github.com\nusername=carol\n\nignored=after-blank\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if attrs["protocol"] != "https" || attrs["host"] != "github.com" || attrs["username"] != "carol" {
		t.Fatalf("attrs = %+v", attrs)
	}
	if _, ok := attrs["ignored"]; ok {
		t.Error("parsing continued past the terminating blank line")
	}
}

func TestReadManifest_spec_15_4_3(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "adapter-manifest.json")
	if err := os.WriteFile(path, []byte(`{"mcpNonce":"abc123","platformMcpServer":{"socket":"/run/lenny/p.sock"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	socket, nonce, err := readManifest(path)
	if err != nil {
		t.Fatalf("readManifest: %v", err)
	}
	if socket != "/run/lenny/p.sock" || nonce != "abc123" {
		t.Fatalf("got (%q,%q), want (/run/lenny/p.sock, abc123)", socket, nonce)
	}

	// Missing socket / nonce are errors.
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"mcpNonce":"abc"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readManifest(bad); err == nil {
		t.Error("expected error for manifest with no socket")
	}
	if _, _, err := readManifest(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("expected error for missing manifest file")
	}
}

func TestParseToolResult(t *testing.T) {
	u, tok, err := parseToolResult(json.RawMessage(`{"content":[{"type":"text","text":"{\"host\":\"github.com\",\"username\":\"x-access-token\",\"token\":\"ghs_x\"}"}]}`))
	if err != nil {
		t.Fatalf("parseToolResult: %v", err)
	}
	if u != "x-access-token" || tok != "ghs_x" {
		t.Fatalf("got (%q,%q)", u, tok)
	}

	// An isError result surfaces the lenny code.
	_, _, err = parseToolResult(json.RawMessage(`{"isError":true,"content":[{"type":"lenny/error","text":"{\"code\":\"GIT_CLONE_AUTH_UNSUPPORTED_HOST\",\"message\":\"no pool\"}"}]}`))
	if err == nil || !strings.Contains(err.Error(), "GIT_CLONE_AUTH_UNSUPPORTED_HOST") {
		t.Fatalf("expected unsupported-host error, got %v", err)
	}
}

// TestMintOverSocket_spec_9_1 exercises the full §9.1 transport: the
// helper performs the §15.4.3 nonce handshake and the lenny/vcs_token
// tools/call against a unix-socket MCP server, mirroring the platform
// MCP socket the adapter hosts in-pod. F-26.2.5.
func TestMintOverSocket_spec_9_1(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "p.sock")
	const nonce = "nonce-xyz"
	srvErr := make(chan error, 1)
	ready := make(chan struct{})
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close()

	go func() {
		close(ready)
		conn, aerr := lis.Accept()
		if aerr != nil {
			srvErr <- aerr
			return
		}
		defer conn.Close()
		enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)

		// initialize: assert the nonce, then ack.
		var initReq struct {
			ID     int `json:"id"`
			Params struct {
				Nonce string `json:"_lennyNonce"`
			} `json:"params"`
		}
		if derr := dec.Decode(&initReq); derr != nil {
			srvErr <- derr
			return
		}
		if initReq.Params.Nonce != nonce {
			srvErr <- errNonce
			return
		}
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": initReq.ID, "result": map[string]any{}})

		// tools/call: assert name + host, return a token.
		var callReq struct {
			ID     int `json:"id"`
			Params struct {
				Name      string `json:"name"`
				Arguments struct {
					Host string `json:"host"`
					Mode string `json:"mode"`
				} `json:"arguments"`
			} `json:"params"`
		}
		if derr := dec.Decode(&callReq); derr != nil {
			srvErr <- derr
			return
		}
		if callReq.Params.Name != "lenny/vcs_token" || callReq.Params.Arguments.Host != "github.com" {
			srvErr <- errCall
			return
		}
		_ = enc.Encode(map[string]any{
			"jsonrpc": "2.0", "id": callReq.ID,
			"result": map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": `{"host":"github.com","username":"x-access-token","token":"ghs_live"}`,
				}},
			},
		})
		srvErr <- nil
	}()
	<-ready

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u, tok, err := mintOverSocket(ctx, socket, nonce, "github.com", "read", 2*time.Second)
	if err != nil {
		t.Fatalf("mintOverSocket: %v", err)
	}
	if u != "x-access-token" || tok != "ghs_live" {
		t.Fatalf("got (%q,%q), want (x-access-token, ghs_live)", u, tok)
	}
	if serr := <-srvErr; serr != nil {
		t.Fatalf("server side: %v", serr)
	}
}

var (
	errNonce = errResp("nonce mismatch")
	errCall  = errResp("unexpected tools/call")
)

type errResp string

func (e errResp) Error() string { return string(e) }
