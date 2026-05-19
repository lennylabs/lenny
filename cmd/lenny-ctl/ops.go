// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/ctl"
)

// withOps resolves the §25.14 lenny-ops endpoint and runs fn against a
// client pointed at it. The ops URL comes from the --ops-server flag
// when set; otherwise lenny-ctl auto-discovers it from the gateway's
// GET /v1/admin/platform/version response (the opsServiceURL field).
// When neither is available, withOps reports a clear error and returns
// exit code 2 without invoking fn.
func withOps(ctx context.Context, flags globalFlags, gateway *ctl.Client, stderr io.Writer, fn func(*ctl.Client) int) int {
	opsURL := flags.opsServer
	if opsURL == "" {
		discovered, err := discoverOpsURL(ctx, gateway)
		if err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: %v\n", err)
			return 2
		}
		opsURL = discovered
	}
	if !looksLikeURL(opsURL) {
		fmt.Fprintf(stderr, "lenny-ctl: ops server URL %q is not a valid URL\n", opsURL)
		return 2
	}
	ops := ctl.New(ctl.Options{
		BaseURL:   opsURL,
		Bearer:    flags.bearer,
		DevTenant: flags.devTenant,
		DevRoles:  flags.devRoles,
		Timeout:   30 * time.Second,
	})
	return fn(ops)
}

// discoverOpsURL implements the §25.14 auto-discovery: it reads the
// gateway's GET /v1/admin/platform/version response and returns the
// opsServiceURL field. When that field is empty — the deployment did
// not configure an ops Ingress — it returns an error instructing the
// operator to pass --ops-server explicitly.
func discoverOpsURL(ctx context.Context, gateway *ctl.Client) (string, error) {
	var v struct {
		OpsServiceURL string `json:"opsServiceURL"`
	}
	if err := gateway.Do(ctx, "GET", "/v1/admin/platform/version", nil, &v); err != nil {
		return "", fmt.Errorf("could not auto-discover the lenny-ops URL from the gateway: %w; pass --ops-server explicitly", err)
	}
	if v.OpsServiceURL == "" {
		return "", fmt.Errorf("the gateway did not advertise an opsServiceURL (no ops Ingress configured); pass --ops-server explicitly")
	}
	return v.OpsServiceURL, nil
}

// looksLikeURL reports whether s parses as an absolute http(s) URL.
func looksLikeURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// cmdRunbooks dispatches the §25.14 `runbooks` group, which maps to the
// §25.7 runbook index on lenny-ops.
func cmdRunbooks(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: runbooks requires a subcommand (list|get)")
		return 2
	}
	switch args[0] {
	case "list":
		// `runbooks list --alert <name>` maps to ?alert=<name>.
		path := "/v1/admin/runbooks"
		if alert := flagValue(args[1:], "--alert"); alert != "" {
			path += "?alert=" + url.QueryEscape(alert)
		}
		return opsGet(ctx, c, path, stdout, stderr)
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: runbooks get requires <name>")
			return 2
		}
		return opsGet(ctx, c, "/v1/admin/runbooks/"+url.PathEscape(args[1]), stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown runbooks subcommand %q\n", args[0])
		return 2
	}
}

// cmdLocks dispatches the §25.14 `locks` group, which maps to the
// §25.4 remediation-lock endpoints on lenny-ops.
func cmdLocks(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: locks requires a subcommand (list|acquire|release)")
		return 2
	}
	switch args[0] {
	case "list":
		return opsGet(ctx, c, "/v1/admin/remediation-locks", stdout, stderr)
	case "acquire":
		scope := flagValue(args[1:], "--scope")
		op := flagValue(args[1:], "--op")
		if scope == "" || op == "" {
			fmt.Fprintln(stderr, "lenny-ctl: locks acquire requires --scope and --op")
			return 2
		}
		body := map[string]any{"scope": scope, "operation": op}
		return opsSend(ctx, c, "POST", "/v1/admin/remediation-locks", body, stdout, stderr)
	case "release":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: locks release requires <id>")
			return 2
		}
		return opsSend(ctx, c, "DELETE", "/v1/admin/remediation-locks/"+url.PathEscape(args[1]), nil, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown locks subcommand %q\n", args[0])
		return 2
	}
}

// cmdEscalations dispatches the §25.14 `escalations` group, which maps
// to the §25.4 escalation endpoints on lenny-ops.
func cmdEscalations(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: escalations requires a subcommand (list|create|resolve)")
		return 2
	}
	switch args[0] {
	case "list":
		return opsGet(ctx, c, "/v1/admin/escalations", stdout, stderr)
	case "create":
		severity := flagValue(args[1:], "--severity")
		summary := flagValue(args[1:], "--summary")
		if severity == "" || summary == "" {
			fmt.Fprintln(stderr, "lenny-ctl: escalations create requires --severity and --summary")
			return 2
		}
		body := map[string]any{"severity": severity, "summary": summary}
		return opsSend(ctx, c, "POST", "/v1/admin/escalations", body, stdout, stderr)
	case "resolve":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: escalations resolve requires <id>")
			return 2
		}
		// §25.14: resolve maps to PUT /v1/admin/escalations/{id} with the
		// resolved status.
		body := map[string]any{"status": "resolved"}
		return opsSend(ctx, c, "PUT", "/v1/admin/escalations/"+url.PathEscape(args[1]), body, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown escalations subcommand %q\n", args[0])
		return 2
	}
}

// cmdDiagnose dispatches the §25.14 `diagnose` group, which maps to the
// §25.6 diagnostic endpoints on lenny-ops.
func cmdDiagnose(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: diagnose requires a subcommand (session|pool|connectivity|credential-pool)")
		return 2
	}
	switch args[0] {
	case "session":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: diagnose session requires <id>")
			return 2
		}
		return opsGet(ctx, c, "/v1/admin/diagnostics/sessions/"+url.PathEscape(args[1]), stdout, stderr)
	case "pool":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: diagnose pool requires <name>")
			return 2
		}
		return opsGet(ctx, c, "/v1/admin/diagnostics/pools/"+url.PathEscape(args[1]), stdout, stderr)
	case "connectivity":
		return opsGet(ctx, c, "/v1/admin/diagnostics/connectivity", stdout, stderr)
	case "credential-pool":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: diagnose credential-pool requires <name>")
			return 2
		}
		return opsGet(ctx, c, "/v1/admin/diagnostics/credential-pools/"+url.PathEscape(args[1]), stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown diagnose subcommand %q\n", args[0])
		return 2
	}
}

// cmdDrift dispatches the §25.14 `drift` group, which maps to the
// §25.10 configuration-drift endpoints on lenny-ops.
func cmdDrift(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: drift requires a subcommand (report|validate|reconcile)")
		return 2
	}
	switch args[0] {
	case "report":
		// `drift report` maps to GET /v1/admin/drift with the optional
		// --scope and --against query parameters.
		q := url.Values{}
		if scope := flagValue(args[1:], "--scope"); scope != "" {
			q.Set("scope", scope)
		}
		if against := flagValue(args[1:], "--against"); against != "" {
			q.Set("against", against)
		}
		path := "/v1/admin/drift"
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		return opsGet(ctx, c, path, stdout, stderr)
	case "validate":
		desired := flagValue(args[1:], "--desired")
		if desired == "" {
			fmt.Fprintln(stderr, "lenny-ctl: drift validate requires --desired <file>")
			return 2
		}
		body, err := readJSONFile(desired)
		if err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: %v\n", err)
			return 1
		}
		return opsSend(ctx, c, "POST", "/v1/admin/drift/validate", body, stdout, stderr)
	case "reconcile":
		// §25.14: reconcile is a mutation; --confirm guards it.
		if !hasFlag(args[1:], "--confirm") {
			fmt.Fprintln(stderr, "lenny-ctl: drift reconcile requires --confirm")
			return 2
		}
		body := map[string]any{"confirm": true}
		if scope := flagValue(args[1:], "--scope"); scope != "" {
			body["scope"] = scope
		}
		return opsSend(ctx, c, "POST", "/v1/admin/drift/reconcile", body, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown drift subcommand %q\n", args[0])
		return 2
	}
}

// opsGet issues a GET to lenny-ops and prints the JSON response.
func opsGet(ctx context.Context, c *ctl.Client, path string, stdout, stderr io.Writer) int {
	var out map[string]any
	if err := c.Do(ctx, "GET", path, nil, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

// opsSend issues a mutating request to lenny-ops and prints the JSON
// response.
func opsSend(ctx context.Context, c *ctl.Client, method, path string, body any, stdout, stderr io.Writer) int {
	var out map[string]any
	if err := c.Do(ctx, method, path, body, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

// flagValue returns the value following the named flag in args, or the
// empty string when the flag is absent or has no value.
func flagValue(args []string, name string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// hasFlag reports whether the named boolean flag appears in args.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

// readJSONFile reads path and decodes it as JSON, returning the decoded
// value ready to be sent as a request body. It is used by the
// commands that take a --desired or similar file argument.
func readJSONFile(path string) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	return body, nil
}
