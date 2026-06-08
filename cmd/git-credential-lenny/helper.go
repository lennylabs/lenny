// SPDX-License-Identifier: MIT

// git-credential-lenny is the §26.2 in-pod Git credential helper. Git
// inside a coding-agent pod invokes it for every HTTPS clone, fetch, or
// push; the helper mints a short-lived VCS token from the gateway and
// hands it to Git as an HTTP Basic username/password. The runtime never
// holds a long-lived credential.
//
// The helper reaches the gateway over the pod's §9.1 platform MCP socket
// (the same channel the runtime uses for lenny/delegate_task): it reads
// the socket path and the §15.4.3 nonce from the adapter manifest
// (/run/lenny/adapter-manifest.json), performs the MCP initialize
// nonce handshake, and calls the lenny/vcs_token platform tool. The
// gateway resolves the host against the calling session's tenant VCS
// credential pool and returns the token, binding the lease to the
// originating session id for audit traceability.
//
// Git's credential-helper protocol: `get` reads `protocol`/`host`
// attributes on stdin and writes `username`/`password` on stdout;
// `store`/`erase` are no-ops because the gateway, not the pod, is the
// authoritative credential store.
//
// spec: §26.2 line 119; §9.1; §15.4.3. F-26.2.5.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// defaultManifestPath is the §15.4 adapter-manifest location inside the
// pod. LENNY_ADAPTER_MANIFEST overrides it (tests, alternate mounts).
const defaultManifestPath = "/run/lenny/adapter-manifest.json"

// manifestVCS is the subset of the §15.4 adapter manifest the helper
// reads: the §15.4.3 intra-pod MCP nonce and the §4.7 platform MCP
// server socket. Field names track pkg/adapter.Manifest. F-26.2.5.
type manifestVCS struct {
	MCPNonce          string `json:"mcpNonce"`
	PlatformMcpServer *struct {
		Socket string `json:"socket"`
	} `json:"platformMcpServer"`
}

// minterFunc mints a VCS credential for host at the given access mode
// (read|write). It is the seam between the Git-protocol layer (Run) and
// the gateway transport (socketMinter), so Run is testable without a
// socket.
type minterFunc func(ctx context.Context, host, mode string) (username, token string, err error)

// Run implements the Git credential-helper protocol. args[0] is the
// operation (get|store|erase). Only `get` produces output; the gateway
// is authoritative, so `store`/`erase` are no-ops. A non-HTTPS protocol
// or an absent host declines silently (no output, exit 0) so Git falls
// back to its other helpers. spec: §26.2 line 119. F-26.2.5.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, mode string, mint minterFunc) error {
	op := ""
	if len(args) > 0 {
		op = args[0]
	}
	if op != "get" {
		// store / erase / unknown: nothing to persist or revoke locally.
		return nil
	}

	attrs, err := parseGitAttributes(stdin)
	if err != nil {
		return fmt.Errorf("git-credential-lenny: read request: %w", err)
	}
	// spec: §26.2 line 119 — gitClone.url is HTTPS-only in v1; decline
	// anything else and let Git try another helper.
	if proto := attrs["protocol"]; proto != "" && proto != "https" {
		return nil
	}
	host := attrs["host"]
	if host == "" {
		return nil
	}

	username, token, err := mint(ctx, host, mode)
	if err != nil {
		return fmt.Errorf("git-credential-lenny: mint token for %s: %w", host, err)
	}
	if token == "" {
		// No credential available; decline rather than feeding Git an
		// empty password.
		return nil
	}

	var b strings.Builder
	if username == "" {
		username = "x-access-token"
	}
	fmt.Fprintf(&b, "protocol=%s\n", firstNonEmpty(attrs["protocol"], "https"))
	fmt.Fprintf(&b, "host=%s\n", host)
	fmt.Fprintf(&b, "username=%s\n", username)
	fmt.Fprintf(&b, "password=%s\n", token)
	fmt.Fprint(&b, "\n")
	_, err = io.WriteString(stdout, b.String())
	return err
}

// parseGitAttributes reads the Git credential key=value attribute lines
// terminated by a blank line or EOF. spec: gitcredentials(7). F-26.2.5.
func parseGitAttributes(r io.Reader) (map[string]string, error) {
	attrs := make(map[string]string)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		attrs[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return attrs, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// readManifest reads the §15.4 adapter manifest and returns the platform
// MCP socket and the §15.4.3 nonce the helper needs to reach the
// gateway. F-26.2.5.
func readManifest(path string) (socket, nonce string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read adapter manifest %s: %w", path, err)
	}
	var m manifestVCS
	if err := json.Unmarshal(data, &m); err != nil {
		return "", "", fmt.Errorf("decode adapter manifest %s: %w", path, err)
	}
	if m.PlatformMcpServer == nil || m.PlatformMcpServer.Socket == "" {
		return "", "", fmt.Errorf("adapter manifest %s carries no platform MCP server socket", path)
	}
	if m.MCPNonce == "" {
		return "", "", fmt.Errorf("adapter manifest %s carries no MCP nonce", path)
	}
	return m.PlatformMcpServer.Socket, m.MCPNonce, nil
}

// socketMinter returns a minterFunc that dials the platform MCP socket,
// performs the §15.4.3 nonce handshake, and calls the lenny/vcs_token
// platform tool. The §9.1 forwarding path carries the call to the
// gateway scoped to this pod's session. spec: §26.2 line 119; §9.1.
// F-26.2.5.
func socketMinter(socket, nonce string, dialTimeout time.Duration) minterFunc {
	return func(ctx context.Context, host, mode string) (string, string, error) {
		return mintOverSocket(ctx, socket, nonce, host, mode, dialTimeout)
	}
}

func mintOverSocket(ctx context.Context, socket, nonce, host, mode string, dialTimeout time.Duration) (string, string, error) {
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return "", "", fmt.Errorf("dial platform MCP socket %s: %w", socket, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	// §15.4.3 nonce handshake.
	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"_lennyNonce": nonce, "protocolVersion": "2025-03-26"},
	}); err != nil {
		return "", "", fmt.Errorf("send initialize: %w", err)
	}
	var initResp rpcResponse
	if err := dec.Decode(&initResp); err != nil {
		return "", "", fmt.Errorf("read initialize response: %w", err)
	}
	if initResp.Error != nil {
		return "", "", fmt.Errorf("initialize rejected: %s", initResp.Error.Message)
	}

	if err := enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "lenny/vcs_token",
			"arguments": map[string]any{"host": host, "mode": mode},
		},
	}); err != nil {
		return "", "", fmt.Errorf("send tools/call: %w", err)
	}
	var callResp rpcResponse
	if err := dec.Decode(&callResp); err != nil {
		return "", "", fmt.Errorf("read tools/call response: %w", err)
	}
	if callResp.Error != nil {
		return "", "", fmt.Errorf("vcs_token rejected: %s", callResp.Error.Message)
	}
	return parseToolResult(callResp.Result)
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// parseToolResult extracts the username/token from an MCP tool result.
// An isError result (the gateway's §15.2 error envelope) is surfaced as
// an error carrying the lenny code. F-26.2.5.
func parseToolResult(raw json.RawMessage) (string, string, error) {
	var res struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", "", fmt.Errorf("decode tool result: %w", err)
	}
	if res.IsError {
		for _, c := range res.Content {
			if c.Type == "lenny/error" {
				var env struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				}
				if json.Unmarshal([]byte(c.Text), &env) == nil {
					return "", "", fmt.Errorf("%s: %s", env.Code, env.Message)
				}
			}
		}
		return "", "", fmt.Errorf("vcs_token returned an error result")
	}
	for _, c := range res.Content {
		if c.Type != "text" {
			continue
		}
		var out struct {
			Username string `json:"username"`
			Token    string `json:"token"`
		}
		if err := json.Unmarshal([]byte(c.Text), &out); err != nil {
			return "", "", fmt.Errorf("decode vcs token: %w", err)
		}
		return out.Username, out.Token, nil
	}
	return "", "", fmt.Errorf("vcs_token result carried no token content")
}
