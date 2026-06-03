// SPDX-License-Identifier: MIT

package localcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/embedded/oidc"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
	lenny "github.com/lennylabs/lenny/sdks/client/go/lenny"
)

// sessionUsage is the §24.17 session command summary.
const sessionUsage = `usage: lenny session <subcommand> [flags]

Subcommands (§24.17):
  lenny session new --runtime <name> [--user <subject>] [--attach]   Create a session
  lenny session attach <sessionId>                                   Stream an existing session
  lenny session send <sessionId> <message>                           Send a message
  lenny session interrupt <sessionId>                                Interrupt a running session
  lenny session cancel <sessionId> [--reason <r>]                    Cancel a session
  lenny session list [--runtime <name>] [--status <state>]           List the caller's sessions
  lenny session get <sessionId>                                      Fetch a session's state
  lenny session logs <sessionId> [--since <RFC3339>] [--limit <n>]   Fetch session logs

Discovery (§24.17 line 222): --api-url / LENNY_API_URL select the gateway;
--token / LENNY_API_TOKEN supply the bearer. Without them the command
targets the running Embedded Mode stack.`

// cmdSession implements `lenny session ...` per §24.17. Session commands
// route through the Lenny Go client SDK (§15.6): the create/send/
// interrupt/cancel verbs exercise the §15.2 MCP surface (the same code
// path any MCP client uses), and list/get/logs use the REST surface the
// spec maps each to. Gateway discovery follows the §24.17 line 222 rules
// (--api-url / LENNY_API_URL, then the Embedded Mode stack); the bearer
// follows §24 line 8 (--token / LENNY_API_TOKEN, then the embedded OIDC
// key).
func cmdSession(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny session: a subcommand is required")
		fmt.Fprintln(stderr, sessionUsage)
		return 2
	}
	sub := args[0]
	rest := args[1:]
	ctx := context.Background()
	switch sub {
	case "new":
		return cmdSessionNew(ctx, rest, stdout, stderr)
	case "send":
		return cmdSessionSend(ctx, rest, stdout, stderr)
	case "interrupt":
		return cmdSessionInterrupt(ctx, rest, stdout, stderr)
	case "cancel":
		return cmdSessionCancel(ctx, rest, stdout, stderr)
	case "list":
		return cmdSessionList(ctx, rest, stdout, stderr)
	case "get":
		return cmdSessionGet(ctx, rest, stdout, stderr)
	case "logs":
		return cmdSessionLogs(ctx, rest, stdout, stderr)
	case "attach":
		// spec: §24.17 line 214 — attach opens an MCP stream with cursor
		// resume. The interactive streaming channel (§15.1 Streamable-HTTP
		// SSE on /mcp) is not yet wired, so attach is deferred. Report it
		// honestly and point at the logs tail, which is the available way
		// to follow a session's output today.
		fmt.Fprintln(stderr, "lenny session attach: interactive streaming is not yet available")
		fmt.Fprintln(stderr, "follow output with: lenny session logs <sessionId>")
		return 2
	default:
		fmt.Fprintf(stderr, "lenny session: unknown subcommand %q\n", sub)
		fmt.Fprintln(stderr, sessionUsage)
		return 2
	}
}

// sessionFlags is the parsed flag set shared across the session verbs.
type sessionFlags struct {
	apiURL  string
	token   string
	user    string
	runtime string
	status  string
	reason  string
	since   string
	limit   int
	attach  bool
	pos     []string // positional arguments in order
}

// parseSessionFlags splits args into the recognized session flags and the
// positional arguments. An unknown flag is an error so a typo fails fast
// instead of being silently swallowed.
func parseSessionFlags(args []string) (sessionFlags, error) {
	var f sessionFlags
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, bool) {
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch a {
		case "--api-url":
			v, ok := next()
			if !ok {
				return f, fmt.Errorf("%s requires a value", a)
			}
			f.apiURL = v
		case "--token":
			v, ok := next()
			if !ok {
				return f, fmt.Errorf("%s requires a value", a)
			}
			f.token = v
		case "--user":
			v, ok := next()
			if !ok {
				return f, fmt.Errorf("%s requires a value", a)
			}
			f.user = v
		case "--runtime":
			v, ok := next()
			if !ok {
				return f, fmt.Errorf("%s requires a value", a)
			}
			f.runtime = v
		case "--status":
			v, ok := next()
			if !ok {
				return f, fmt.Errorf("%s requires a value", a)
			}
			f.status = v
		case "--reason":
			v, ok := next()
			if !ok {
				return f, fmt.Errorf("%s requires a value", a)
			}
			f.reason = v
		case "--since":
			v, ok := next()
			if !ok {
				return f, fmt.Errorf("%s requires a value", a)
			}
			f.since = v
		case "--limit":
			v, ok := next()
			if !ok {
				return f, fmt.Errorf("%s requires a value", a)
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return f, fmt.Errorf("--limit must be a non-negative integer, got %q", v)
			}
			f.limit = n
		case "--attach":
			f.attach = true
		default:
			if len(a) > 2 && a[0] == '-' && a[1] == '-' {
				return f, fmt.Errorf("unknown flag %q", a)
			}
			f.pos = append(f.pos, a)
		}
	}
	return f, nil
}

// sessionClient builds the SDK client the §24.17 verbs share. It resolves
// the gateway URL (--api-url, then LENNY_API_URL, then the Embedded Mode
// stack) and the bearer (--token, then LENNY_API_TOKEN, then the embedded
// OIDC key), so a single binary reaches both a local stack and a remote /
// clustered gateway. On failure it writes a diagnostic to stderr and
// returns ok=false with the exit code the caller should return.
func sessionClient(f sessionFlags, verb string, stderr io.Writer) (*lenny.Client, bool, int) {
	gatewayURL, err := resolveGatewayURL(f)
	if err != nil {
		if errors.Is(err, stack.ErrNoRunningStack) {
			fmt.Fprintf(stderr, "lenny session %s: no --api-url, no LENNY_API_URL, and no running stack; run 'lenny up' or pass --api-url\n", verb)
		} else {
			fmt.Fprintf(stderr, "lenny session %s: %v\n", verb, err)
		}
		return nil, false, 1
	}
	bearer, err := resolveBearer(f)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session %s: %v\n", verb, err)
		return nil, false, 1
	}
	client, err := lenny.New(gatewayURL, lenny.WithAuth(lenny.BearerToken(bearer)))
	if err != nil {
		fmt.Fprintf(stderr, "lenny session %s: %v\n", verb, err)
		return nil, false, 1
	}
	return client, true, 0
}

// resolveGatewayURL applies the §24.17 line 222 discovery precedence:
// --api-url, then LENNY_API_URL, then the running Embedded Mode stack.
func resolveGatewayURL(f sessionFlags) (string, error) {
	if f.apiURL != "" {
		return f.apiURL, nil
	}
	if v := os.Getenv("LENNY_API_URL"); v != "" {
		return v, nil
	}
	return stack.RunningGateway("")
}

// resolveBearer applies the §24 line 8 token precedence: --token, then
// LENNY_API_TOKEN, then a token minted from the embedded OIDC key. The
// embedded path requires a running stack; the explicit flag/env paths do
// not, so the CLI authenticates against a remote gateway without one.
func resolveBearer(f sessionFlags) (string, error) {
	if f.token != "" {
		return f.token, nil
	}
	if v := os.Getenv("LENNY_API_TOKEN"); v != "" {
		return v, nil
	}
	return mintEmbeddedBearer()
}

// mintEmbeddedBearer mints a bearer from the running Embedded Mode stack's
// persisted OIDC key — the same key `lenny token print` uses and the
// gateway trusts as an additional §10.2 verifier.
func mintEmbeddedBearer() (string, error) {
	root, err := stack.DefaultRoot()
	if err != nil {
		return "", err
	}
	paths := stack.NewPaths(root)
	provider, err := oidc.NewWithPersistedKey(paths.OIDCKeyFile(), false)
	if err != nil {
		return "", fmt.Errorf("no embedded OIDC key found; run 'lenny up' or pass --token (%w)", err)
	}
	return provider.Issue(oidc.DefaultTokenTTL)
}

// cmdSessionNew implements `lenny session new` over the §15.2 MCP
// lenny/create_session tool (the §24.17 line 213 mapping). It prints the
// created session id. The --attach flag is accepted but interactive
// streaming is deferred; a notice points at `session logs`.
func cmdSessionNew(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f, err := parseSessionFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session new: %v\n", err)
		return 2
	}
	if f.runtime == "" {
		fmt.Fprintln(stderr, "lenny session new: --runtime <name> is required")
		return 2
	}
	client, ok, code := sessionClient(f, "new", stderr)
	if !ok {
		return code
	}
	created, err := client.MCP().CreateSession(ctx, f.runtime, f.user)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session new: %v\n", err)
		return 1
	}
	if created.SessionID == "" {
		fmt.Fprintln(stderr, "lenny session new: gateway returned no session id")
		return 1
	}
	fmt.Fprintln(stdout, created.SessionID)
	if f.attach {
		fmt.Fprintln(stderr, "lenny session new: --attach streaming is not yet available; follow output with 'lenny session logs "+created.SessionID+"'")
	}
	return 0
}

// cmdSessionSend implements `lenny session send <id> <message>` over the
// §15.2 MCP lenny/send_message tool (the §24.17 line 215 mapping).
func cmdSessionSend(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f, err := parseSessionFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session send: %v\n", err)
		return 2
	}
	if len(f.pos) < 2 {
		fmt.Fprintln(stderr, "lenny session send: usage: session send <sessionId> <message>")
		return 2
	}
	client, ok, code := sessionClient(f, "send", stderr)
	if !ok {
		return code
	}
	reply, err := client.MCP().SendMessage(ctx, f.pos[0], f.pos[1])
	if err != nil {
		fmt.Fprintf(stderr, "lenny session send: %v\n", err)
		return 1
	}
	if reply != "" {
		fmt.Fprintln(stdout, reply)
	}
	return 0
}

// cmdSessionInterrupt implements `lenny session interrupt <id>` over the
// §15.2 MCP interrupt tool (the §24.17 line 216 mapping).
func cmdSessionInterrupt(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f, err := parseSessionFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session interrupt: %v\n", err)
		return 2
	}
	if len(f.pos) < 1 {
		fmt.Fprintln(stderr, "lenny session interrupt: usage: session interrupt <sessionId>")
		return 2
	}
	client, ok, code := sessionClient(f, "interrupt", stderr)
	if !ok {
		return code
	}
	res, err := client.MCP().InterruptSession(ctx, f.pos[0])
	if err != nil {
		fmt.Fprintf(stderr, "lenny session interrupt: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s %s\n", res.SessionID, res.State)
	return 0
}

// cmdSessionCancel implements `lenny session cancel <id>` over the §15.2
// MCP lenny/cancel_session tool (the §24.17 line 217 mapping).
func cmdSessionCancel(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f, err := parseSessionFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session cancel: %v\n", err)
		return 2
	}
	if len(f.pos) < 1 {
		fmt.Fprintln(stderr, "lenny session cancel: usage: session cancel <sessionId>")
		return 2
	}
	client, ok, code := sessionClient(f, "cancel", stderr)
	if !ok {
		return code
	}
	res, err := client.MCP().CancelSession(ctx, f.pos[0], f.reason)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session cancel: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s %s\n", res.SessionID, res.State)
	return 0
}

// cmdSessionList implements `lenny session list` over REST GET
// /v1/sessions (the §24.17 line 218 mapping; the one session command that
// works over REST for non-interactive callers).
func cmdSessionList(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f, err := parseSessionFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session list: %v\n", err)
		return 2
	}
	client, ok, code := sessionClient(f, "list", stderr)
	if !ok {
		return code
	}
	page, err := client.ListSessions(ctx, lenny.ListOptions{
		Runtime: f.runtime,
		State:   lenny.State(f.status),
		Limit:   f.limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "lenny session list: %v\n", err)
		return 1
	}
	for _, s := range page.Sessions {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", s.ID, s.State, s.RuntimeRef)
	}
	return 0
}

// cmdSessionGet implements `lenny session get <id>` over REST GET
// /v1/sessions/{id} (the §24.17 line 219 mapping).
func cmdSessionGet(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f, err := parseSessionFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session get: %v\n", err)
		return 2
	}
	if len(f.pos) < 1 {
		fmt.Fprintln(stderr, "lenny session get: usage: session get <sessionId>")
		return 2
	}
	client, ok, code := sessionClient(f, "get", stderr)
	if !ok {
		return code
	}
	sess, err := client.GetSession(ctx, f.pos[0])
	if err != nil {
		fmt.Fprintf(stderr, "lenny session get: %v\n", err)
		return 1
	}
	out, _ := json.MarshalIndent(sess, "", "  ")
	fmt.Fprintln(stdout, string(out))
	return 0
}

// cmdSessionLogs implements `lenny session logs <id>` over REST GET
// /v1/sessions/{id}/logs (the §24.17 line 220 mapping), honoring the
// --since and --limit filters.
func cmdSessionLogs(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f, err := parseSessionFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session logs: %v\n", err)
		return 2
	}
	if len(f.pos) < 1 {
		fmt.Fprintln(stderr, "lenny session logs: usage: session logs <sessionId> [--since <RFC3339>]")
		return 2
	}
	opt := lenny.LogsOptions{Limit: f.limit}
	if f.since != "" {
		t, err := time.Parse(time.RFC3339, f.since)
		if err != nil {
			fmt.Fprintf(stderr, "lenny session logs: --since must be an RFC3339 timestamp: %v\n", err)
			return 2
		}
		opt.Since = t
	}
	client, ok, code := sessionClient(f, "logs", stderr)
	if !ok {
		return code
	}
	page, err := client.SessionLogs(ctx, f.pos[0], opt)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session logs: %v\n", err)
		return 1
	}
	for _, e := range page.Items {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", e.Timestamp, e.Type, string(e.Data))
	}
	return 0
}
