// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/lennylabs/lenny/pkg/ctl"
)

// withOps resolves the §25.14 lenny-ops endpoint and runs fn against a
// client pointed at it. The §24.16 routing rule is applied in order:
//
//  1. --ops-server (or LENNY_OPS_URL) when set.
//  2. Otherwise auto-discover from the gateway's
//     GET /v1/admin/platform/version response (the opsServiceURL field).
//  3. When auto-discovery fails (gateway unreachable, or opsServiceURL
//     absent because the cluster is mid-upgrade), fall back to the
//     gateway host on the assumption that its operability endpoints still
//     serve the request, and surface a warning for the ops command.
//
// spec: §24.16 line 201 (routing rules 1-3).
func withOps(ctx context.Context, flags globalFlags, gateway *ctl.Client, stderr io.Writer, fn func(*ctl.Client) int) int {
	opsURL := flags.opsServer
	if opsURL == "" {
		discovered, err := discoverOpsURL(ctx, gateway)
		if err != nil {
			// spec: §24.16 line 201 routing rule 3 — auto-discovery failed,
			// so fall back to the gateway host rather than aborting. The
			// command proceeds against the gateway; the operator is warned
			// that only gateway-hosted operability endpoints will answer.
			opsURL = gateway.BaseURL()
			fmt.Fprintf(stderr,
				"lenny-ctl: WARN: %v; falling back to the gateway host %s — only gateway-hosted operability endpoints will answer\n",
				err, opsURL)
		} else {
			opsURL = discovered
		}
	}
	if !looksLikeURL(opsURL) {
		fmt.Fprintf(stderr, "lenny-ctl: ops server URL %q is not a valid URL\n", opsURL)
		return 2
	}
	ops := ctl.New(ctl.Options{
		BaseURL:            opsURL,
		Bearer:             flags.bearer,
		DevTenant:          flags.devTenant,
		DevRoles:           flags.devRoles,
		Timeout:            flags.timeout,
		InsecureSkipVerify: flags.insecure,
	})
	return fn(ops)
}

// discoverOpsURL implements the §24.16 rule-2 auto-discovery: it reads
// the gateway's GET /v1/admin/platform/version response and returns the
// opsServiceURL field. It returns an error when the gateway is
// unreachable or the field is empty (no ops Ingress configured, or the
// cluster is mid-upgrade); withOps applies the rule-3 gateway-host
// fallback on that error.
func discoverOpsURL(ctx context.Context, gateway *ctl.Client) (string, error) {
	var v struct {
		OpsServiceURL string `json:"opsServiceURL"`
	}
	if err := gateway.Do(ctx, "GET", "/v1/admin/platform/version", nil, &v); err != nil {
		return "", fmt.Errorf("could not auto-discover the lenny-ops URL from the gateway: %w", err)
	}
	if v.OpsServiceURL == "" {
		return "", fmt.Errorf("the gateway did not advertise an opsServiceURL (no ops Ingress configured)")
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
// §25.4 remediation-lock endpoints on lenny-ops. spec: §25.14 lines
// 4877-4879 (list/acquire/release); §24.15 line 190 names inspect, steal,
// and release, so `get` and `steal` round out the group. F-24.15.12.
func cmdLocks(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: locks requires a subcommand (list|get|acquire|release|steal)")
		return 2
	}
	switch args[0] {
	case "list":
		return opsGet(ctx, c, "/v1/admin/remediation-locks", stdout, stderr)
	case "get":
		// §24.15 line 190 calls for an explicit per-id inspect; it maps to
		// GET /v1/admin/remediation-locks/{id}. F-24.15.12.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: locks get requires <id>")
			return 2
		}
		return opsGet(ctx, c, "/v1/admin/remediation-locks/"+url.PathEscape(args[1]), stdout, stderr)
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
	case "steal":
		// §25.4 line 2106: steal takes over an existing lock and requires
		// confirm:true and a reason. Without --confirm the server returns
		// the §25.2 dry-run preview, so --reason is enforced client-side
		// only when --confirm is present. F-24.15.12.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: locks steal requires <id>")
			return 2
		}
		confirm := hasFlag(args[2:], "--confirm")
		reason := flagValue(args[2:], "--reason")
		if confirm && reason == "" {
			fmt.Fprintln(stderr, "lenny-ctl: locks steal --confirm requires --reason <text>")
			return 2
		}
		body := map[string]any{"confirm": confirm}
		if reason != "" {
			body["reason"] = reason
		}
		if ttl := flagValue(args[2:], "--ttl"); ttl != "" {
			secs, err := strconv.Atoi(ttl)
			if err != nil || secs <= 0 {
				fmt.Fprintf(stderr, "lenny-ctl: locks steal --ttl must be a positive integer, got %q\n", ttl)
				return 2
			}
			body["ttlSeconds"] = secs
		}
		return opsSend(ctx, c, "POST", "/v1/admin/remediation-locks/"+url.PathEscape(args[1])+"/steal", body, stdout, stderr)
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

// cmdDoctor runs the §25.6 diagnostic suite via
// POST /v1/admin/diagnostics/run on lenny-ops. Without --fix the run is
// read-only and prints a remediation report; with --fix it applies the
// §25.6 auto-remediations and prints the per-finding outcomes plus the
// §25.2 operation envelope. An optional --findings <a,b,c> narrows the
// run to specific finding codes. spec: §24.2 lines 44-45; §25.6 lines
// 2941-2982. F-24.2.3.
func cmdDoctor(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	fix := hasFlag(args, "--fix")
	path := "/v1/admin/diagnostics/run"
	if fix {
		path += "?fix=true"
	}
	body := map[string]any{}
	if list := flagValue(args, "--findings"); list != "" {
		findings := []string{}
		for _, f := range strings.Split(list, ",") {
			if f = strings.TrimSpace(f); f != "" {
				findings = append(findings, f)
			}
		}
		body["findings"] = findings
	}
	return opsSend(ctx, c, "POST", path, body, stdout, stderr)
}

// cmdDrift dispatches the §25.14 `drift` group, which maps to the
// §25.10 configuration-drift endpoints on lenny-ops.
func cmdDrift(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: drift requires a subcommand (report|validate|snapshot|reconcile)")
		return 2
	}
	switch args[0] {
	case "snapshot":
		// §25.14 line 4943: `drift snapshot refresh --desired <file>`
		// replaces the stored desired-state snapshot via
		// POST /v1/admin/drift/snapshot/refresh. §25.10 keeps refresh an
		// explicit operator action: without --confirm the server returns
		// the §25.2 dry-run preview and no snapshot is replaced. F-24.15.11.
		if len(args) < 2 || args[1] != "refresh" {
			fmt.Fprintln(stderr, "lenny-ctl: drift snapshot requires the refresh subcommand")
			return 2
		}
		desired := flagValue(args[2:], "--desired")
		if desired == "" {
			fmt.Fprintln(stderr, "lenny-ctl: drift snapshot refresh requires --desired <file>")
			return 2
		}
		snapshot, err := readJSONFile(desired)
		if err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: %v\n", err)
			return 1
		}
		body := map[string]any{"desired": snapshot}
		if hasFlag(args[2:], "--confirm") {
			body["confirm"] = true
		}
		return opsSend(ctx, c, "POST", "/v1/admin/drift/snapshot/refresh", body, stdout, stderr)
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

// cmdBackup dispatches the §25.14 `backup` group, which maps to the
// §25.11 backup endpoints on lenny-ops. spec: §25.14 lines 4950-4955.
// F-24.15.6.
func cmdBackup(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: backup requires a subcommand (list|get|create|verify|schedule|policy)")
		return 2
	}
	switch args[0] {
	case "list":
		return opsGet(ctx, c, "/v1/admin/backups", stdout, stderr)
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: backup get requires <id>")
			return 2
		}
		return opsGet(ctx, c, "/v1/admin/backups/"+url.PathEscape(args[1]), stdout, stderr)
	case "create":
		// §25.14 line 4952: --type is required; --confirm guards the
		// production-side §25.2 confirm pattern.
		typ := flagValue(args[1:], "--type")
		if typ == "" {
			fmt.Fprintln(stderr, "lenny-ctl: backup create requires --type <full|postgres|config>")
			return 2
		}
		body := map[string]any{"type": typ}
		if hasFlag(args[1:], "--confirm") {
			body["confirm"] = true
		}
		return opsSend(ctx, c, "POST", "/v1/admin/backups", body, stdout, stderr)
	case "verify":
		// §25.14 line 4953: `backup verify <id> [--mode test-restore]`.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: backup verify requires <id>")
			return 2
		}
		path := "/v1/admin/backups/" + url.PathEscape(args[1]) + "/verify"
		if mode := flagValue(args[2:], "--mode"); mode != "" {
			path += "?mode=" + url.QueryEscape(mode)
		}
		return opsSend(ctx, c, "POST", path, nil, stdout, stderr)
	case "schedule":
		// §25.14 line 4954: `backup schedule get|set` → GET/PUT
		// /v1/admin/backups/schedule.
		return cmdBackupSubresource(ctx, c, "schedule", args[1:], stdout, stderr)
	case "policy":
		// §25.14 line 4955: `backup policy get|set` → GET/PUT
		// /v1/admin/backups/policy.
		return cmdBackupSubresource(ctx, c, "policy", args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown backup subcommand %q\n", args[0])
		return 2
	}
}

// cmdBackupSubresource implements the get/set verbs shared by the §25.14
// `backup schedule` and `backup policy` subcommands. get maps to a GET;
// set maps to a PUT whose body is read from --from-file. F-24.15.6.
func cmdBackupSubresource(ctx context.Context, c *ctl.Client, name string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "lenny-ctl: backup %s requires a verb (get|set)\n", name)
		return 2
	}
	path := "/v1/admin/backups/" + name
	switch args[0] {
	case "get":
		return opsGet(ctx, c, path, stdout, stderr)
	case "set":
		file := flagValue(args[1:], "--from-file")
		if file == "" {
			fmt.Fprintf(stderr, "lenny-ctl: backup %s set requires --from-file <%s.json>\n", name, name)
			return 2
		}
		body, err := readJSONFile(file)
		if err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: %v\n", err)
			return 1
		}
		return opsSend(ctx, c, "PUT", path, body, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown backup %s verb %q\n", name, args[0])
		return 2
	}
}

// cmdRestore dispatches the §25.14 `restore` group, which maps to the
// §25.11 restore endpoints on lenny-ops. spec: §25.14 lines 4956-4961.
// F-24.15.7.
func cmdRestore(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: restore requires a subcommand (safety-check|preview|execute|status|resume|confirm-legal-hold-ledger)")
		return 2
	}
	switch args[0] {
	case "safety-check":
		// §25.14 line 4956: GET /v1/admin/restore/safety-check?backupId=.
		backupID := flagValue(args[1:], "--backup")
		if backupID == "" {
			fmt.Fprintln(stderr, "lenny-ctl: restore safety-check requires --backup <id>")
			return 2
		}
		return opsGet(ctx, c, "/v1/admin/restore/safety-check?backupId="+url.QueryEscape(backupID), stdout, stderr)
	case "preview":
		// §25.14 line 4957: POST /v1/admin/restore/preview {backupId}.
		backupID := flagValue(args[1:], "--backup")
		if backupID == "" {
			fmt.Fprintln(stderr, "lenny-ctl: restore preview requires --backup <id>")
			return 2
		}
		return opsSend(ctx, c, "POST", "/v1/admin/restore/preview", map[string]any{"backupId": backupID}, stdout, stderr)
	case "execute":
		// §25.14 line 4958: POST /v1/admin/restore/execute. Without
		// --confirm the server returns the §25.2 dry-run preview; the
		// destructive run additionally requires --acknowledge-data-loss.
		backupID := flagValue(args[1:], "--backup")
		if backupID == "" {
			fmt.Fprintln(stderr, "lenny-ctl: restore execute requires --backup <id>")
			return 2
		}
		body := map[string]any{"backupId": backupID}
		if hasFlag(args[1:], "--confirm") {
			body["confirm"] = true
		}
		if hasFlag(args[1:], "--acknowledge-data-loss") {
			body["acknowledgeDataLoss"] = true
		}
		return opsSend(ctx, c, "POST", "/v1/admin/restore/execute", body, stdout, stderr)
	case "status":
		// §25.14 line 4959: GET /v1/admin/restore/{id}/status.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: restore status requires <id>")
			return 2
		}
		return opsGet(ctx, c, "/v1/admin/restore/"+url.PathEscape(args[1])+"/status", stdout, stderr)
	case "resume":
		// §25.14 line 4960: POST /v1/admin/restore/resume?restoreId=.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: restore resume requires <id>")
			return 2
		}
		return opsSend(ctx, c, "POST", "/v1/admin/restore/resume?restoreId="+url.QueryEscape(args[1]), nil, stdout, stderr)
	case "confirm-legal-hold-ledger":
		// §25.14 line 4961: POST /v1/admin/restore/{id}/confirm-legal-hold-
		// ledger {justification}.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: restore confirm-legal-hold-ledger requires <id>")
			return 2
		}
		justification := flagValue(args[2:], "--justification")
		if justification == "" {
			fmt.Fprintln(stderr, "lenny-ctl: restore confirm-legal-hold-ledger requires --justification <text>")
			return 2
		}
		return opsSend(ctx, c, "POST",
			"/v1/admin/restore/"+url.PathEscape(args[1])+"/confirm-legal-hold-ledger",
			map[string]any{"justification": justification}, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown restore subcommand %q\n", args[0])
		return 2
	}
}

// cmdMCPManagement implements the §24.15 mcp-management group: a thin
// JSON-RPC client for the lenny-ops management MCP server mounted at
// /mcp/management. `mcp-management tools` issues tools/list; `mcp-management
// call <tool> [--params <json>]` issues tools/call. The raw JSON-RPC
// envelope is printed so operators can script against the §25.12 tool
// surface for local testing. spec: §24.15 line 193; §25.12.
func cmdMCPManagement(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: mcp-management requires a subcommand (tools|call)")
		return 2
	}
	switch args[0] {
	case "tools":
		req := map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/list",
			"params": map[string]any{},
		}
		return opsSend(ctx, c, "POST", "/mcp/management", req, stdout, stderr)
	case "call":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: mcp-management call requires <tool>")
			return 2
		}
		tool := args[1]
		fs := flag.NewFlagSet("mcp-management call", flag.ContinueOnError)
		fs.SetOutput(stderr)
		params := fs.String("params", "", "tool arguments as a JSON object")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		arguments := json.RawMessage("{}")
		if strings.TrimSpace(*params) != "" {
			if !json.Valid([]byte(*params)) {
				fmt.Fprintln(stderr, "lenny-ctl: --params must be a JSON object")
				return 2
			}
			arguments = json.RawMessage(*params)
		}
		req := map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": tool, "arguments": arguments},
		}
		return opsSend(ctx, c, "POST", "/mcp/management", req, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown mcp-management subcommand %q\n", args[0])
		return 2
	}
}

// cmdLogs implements the §24.15 `lenny-ctl logs pods <namespace> <name>`
// pod-log proxy. It streams the §25.4 log-proxy endpoint
// (GET /v1/admin/logs/pods/{namespace}/{name} on lenny-ops) to stdout as
// raw text. The args slice is everything after `logs pods`: the namespace,
// the pod name, then the optional query flags. spec: §24.15 line 192;
// §25.4 lines 2528-2534.
func cmdLogs(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "lenny-ctl: logs pods requires <namespace> <name>")
		return 2
	}
	namespace, name := args[0], args[1]
	fs := flag.NewFlagSet("logs pods", flag.ContinueOnError)
	fs.SetOutput(stderr)
	container := fs.String("container", "", "container name in a multi-container pod")
	since := fs.String("since", "", "return logs newer than a duration (e.g. 5m) or a number of seconds")
	tail := fs.Int("tail", -1, "return only the last N lines")
	previous := fs.Bool("previous", false, "return logs from the previously terminated container instance")
	if err := fs.Parse(args[2:]); err != nil {
		return 2
	}
	q := url.Values{}
	if *container != "" {
		q.Set("container", *container)
	}
	if *since != "" {
		q.Set("since", *since)
	}
	if *tail >= 0 {
		q.Set("tail", strconv.Itoa(*tail))
	}
	if *previous {
		q.Set("previous", "true")
	}
	path := "/v1/admin/logs/pods/" + url.PathEscape(namespace) + "/" + url.PathEscape(name)
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	if err := c.Get(ctx, path, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
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
