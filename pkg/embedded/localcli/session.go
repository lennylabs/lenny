// SPDX-License-Identifier: MIT

package localcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/embedded/devauth"
	"github.com/lennylabs/lenny/pkg/embedded/stack"
	"github.com/lennylabs/lenny/pkg/workspacepack"
	lenny "github.com/lennylabs/lenny/sdks/client/go/lenny"
)

// sessionUsage is the §24.17 session command summary.
const sessionUsage = `usage: lenny session <subcommand> [flags]

Subcommands (§24.17):
  lenny session new --runtime <name> [--attach] [--workspace <dir>] [--file <path>]... ["prompt"]   Create a session
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
		return cmdSessionAttach(ctx, rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny session: unknown subcommand %q\n", sub)
		fmt.Fprintln(stderr, sessionUsage)
		return 2
	}
}

// sessionFlags is the parsed flag set shared across the session verbs.
type sessionFlags struct {
	apiURL    string
	token     string
	user      string
	runtime   string
	status    string
	reason    string
	since     string
	limit     int
	attach    bool
	workspace string   // --workspace <dir>: local repo to stage as uploadArchive
	files     []string // --file <path> (repeatable): files to stage as uploadFile
	pos       []string // positional arguments in order
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
		case "--workspace":
			v, ok := next()
			if !ok {
				return f, fmt.Errorf("%s requires a value", a)
			}
			f.workspace = v
		case "--file":
			v, ok := next()
			if !ok {
				return f, fmt.Errorf("%s requires a value", a)
			}
			f.files = append(f.files, v)
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
// persisted dev key — the same key `lenny token print` uses and the
// in-cluster gateway trusts as an additional §10.2 verifier through
// --bearer-trust-hmac-key-file.
func mintEmbeddedBearer() (string, error) {
	root, err := stack.DefaultRoot()
	if err != nil {
		return "", err
	}
	paths := stack.NewPaths(root)
	signer, err := devauth.NewWithPersistedKey(paths.OIDCKeyFile(), false)
	if err != nil {
		return "", fmt.Errorf("no embedded dev key found; run 'lenny up' or pass --token (%w)", err)
	}
	return signer.Issue(devauth.DefaultTokenTTL)
}

// cmdSessionNew implements `lenny session new` per §24.17 line 213. With
// no `--workspace`/`--file` it routes through the §15.2 MCP
// lenny/create_session tool. With `--workspace <dir>` or `--file <path>`
// it runs the §26.2 lines 95-114 staging flow: create over REST, tar the
// workspace honoring `.lennyignore`/`.gitignore`, upload it, bind the
// resulting `uploadArchive` plan at finalize, then start. A positional
// argument is delivered as the session's opening prompt. When `--attach`
// is set (the §24.17 line 213 default in an interactive TTY) it then opens
// the §15.1 event stream and renders output, elicitation prompts, and
// lifecycle transitions inline until the session terminates.
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

	prompt := strings.TrimSpace(strings.Join(f.pos, " "))

	var sessionID string
	if f.workspace != "" || len(f.files) > 0 {
		// spec: §26.2 lines 95-114 — stage the local workspace into the
		// session before it starts.
		id, werr := createSessionWithWorkspace(ctx, client, f, stderr)
		if werr != nil {
			fmt.Fprintf(stderr, "lenny session new: %v\n", werr)
			return 1
		}
		sessionID = id
	} else {
		created, cerr := client.MCP().CreateSession(ctx, f.runtime, f.user)
		if cerr != nil {
			fmt.Fprintf(stderr, "lenny session new: %v\n", cerr)
			return 1
		}
		sessionID = created.SessionID
	}
	if sessionID == "" {
		fmt.Fprintln(stderr, "lenny session new: gateway returned no session id")
		return 1
	}

	// spec: §24.17 line 213 / §26.2 line 126 — deliver the positional
	// prompt as the session's opening message so the agent starts working.
	if prompt != "" {
		if _, err := client.SendMessages(ctx, sessionID, lenny.SendMessagesRequest{
			Messages: []lenny.MessagePayload{{Role: "user", Content: prompt}},
		}); err != nil {
			fmt.Fprintf(stderr, "lenny session new: deliver prompt: %v\n", err)
			return 1
		}
	}

	fmt.Fprintln(stdout, sessionID)
	if attachWanted(f, stdout) {
		// spec: §24.17 line 213 — render the session inline until it
		// terminates. The created id is already on stdout so a caller
		// that captured it can still drive the session even if the
		// attach stream is interrupted.
		return streamSession(ctx, client, sessionID, stdout, stderr)
	}
	return 0
}

// createSessionWithWorkspace runs the §26.2 lines 95-114 workspace-staging
// flow: it creates the session over REST (to obtain the §7.1 uploadToken),
// tars the `--workspace` directory honoring `.lennyignore`/`.gitignore`
// and uploads it as an `uploadArchive` source, uploads each `--file` as an
// `uploadFile` source, binds the resulting §14 plan at finalize, and
// starts the session. It returns the created session id.
//
// spec: §26.2 lines 95-114; §7.1 (decomposed create → upload → finalize →
// start); §14 uploadArchive / uploadFile.
func createSessionWithWorkspace(ctx context.Context, client *lenny.Client, f sessionFlags, stderr io.Writer) (string, error) {
	created, err := client.CreateSession(ctx, lenny.CreateSessionRequest{
		RuntimeRef: f.runtime,
		UserID:     f.user,
	})
	if err != nil {
		return "", err
	}
	id := created.ID
	token := created.UploadToken

	var sources []map[string]any
	if f.workspace != "" {
		packed, perr := workspacepack.Pack(f.workspace)
		if perr != nil {
			return "", perr
		}
		ignoreNote := ""
		if packed.IgnoreFile != "" {
			ignoreNote = fmt.Sprintf(" (honoring %s)", packed.IgnoreFile)
		}
		fmt.Fprintf(stderr, "staging %d file(s) from %s%s\n", packed.Files, f.workspace, ignoreNote)
		up, uerr := client.UploadArchive(ctx, id, token, packed.Data, lenny.UploadArchiveOptions{})
		if uerr != nil {
			return "", uerr
		}
		sources = append(sources, map[string]any{
			"type":       "uploadArchive",
			"pathPrefix": ".",
			"uploadRef":  up.UploadRef,
			"format":     "tar.gz",
		})
	}
	for _, fp := range f.files {
		data, rerr := os.ReadFile(fp)
		if rerr != nil {
			return "", fmt.Errorf("read --file %q: %w", fp, rerr)
		}
		up, uerr := client.UploadFile(ctx, id, token, data, lenny.UploadArchiveOptions{})
		if uerr != nil {
			return "", uerr
		}
		sources = append(sources, map[string]any{
			"type":      "uploadFile",
			"path":      filepath.Base(fp),
			"uploadRef": up.UploadRef,
		})
	}

	plan, err := json.Marshal(map[string]any{
		"schemaVersion": 1,
		"sources":       sources,
	})
	if err != nil {
		return "", err
	}
	if _, err := client.FinalizeWorkspace(ctx, id, plan); err != nil {
		return "", err
	}
	if _, err := client.Start(ctx, id); err != nil {
		return "", err
	}
	return id, nil
}

// cmdSessionAttach implements `lenny session attach <sessionId>` per
// §24.17 line 214. It opens the §15.1 event stream from the retained
// backlog and renders the session inline until it terminates or the
// caller interrupts. Reconnect-with-cursor is handled by the SDK's
// StreamEvents (Last-Event-ID resume).
func cmdSessionAttach(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f, err := parseSessionFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "lenny session attach: %v\n", err)
		return 2
	}
	if len(f.pos) < 1 {
		fmt.Fprintln(stderr, "lenny session attach: usage: session attach <sessionId>")
		return 2
	}
	client, ok, code := sessionClient(f, "attach", stderr)
	if !ok {
		return code
	}
	return streamSession(ctx, client, f.pos[0], stdout, stderr)
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
