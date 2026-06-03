// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lennylabs/lenny/pkg/ctl"
	"github.com/lennylabs/lenny/pkg/preflight/infra"
)

// cmdMe implements the §24.15 `me` group: the caller's own identity,
// authorized tools, and in-flight operations. All three subcommands
// target the gateway admin API (§25 lines 4901-4903). spec: §24.15
// line 180. F-24.15.1.
func cmdMe(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		// `lenny-ctl me` with no subcommand maps to GET /v1/admin/me.
		return gatewayGet(ctx, c, "/v1/admin/me", stdout, stderr)
	}
	switch args[0] {
	case "tools":
		// §25 line 4902 — tools the caller can actually invoke.
		return gatewayGet(ctx, c, "/v1/admin/me/authorized-tools", stdout, stderr)
	case "operations":
		// §25 line 4903 — caller's in-flight operations.
		return gatewayGet(ctx, c, "/v1/admin/me/operations", stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown me subcommand %q (want tools|operations)\n", args[0])
		return 2
	}
}

// cmdAudit implements the §24.15 `audit` group: scatter-gather query,
// single-event lookup, summary aggregation, and the three remediation
// verbs (OCSF retranslate, EventBus republish, force-drop a blocked
// partition). All target the gateway admin audit surface. spec: §24.15
// line 186; §25 lines 4930-4935; §25.9. F-24.15.5.
func cmdAudit(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: audit requires a subcommand (query|get|summary|retranslate|republish|drop-partition)")
		return 2
	}
	switch args[0] {
	case "query":
		// §25 line 4930: GET /v1/admin/audit-events. A narrowing filter
		// (typically --since) is required server-side; an unfiltered
		// query is rejected AUDIT_QUERY_TOO_BROAD.
		q := url.Values{}
		for flagName, param := range map[string]string{
			"--since": "since", "--until": "until", "--event-type": "eventType",
			"--actor": "actorId", "--resource-type": "resourceType",
			"--resource-id": "resourceId", "--severity": "severity",
			"--operation-id": "operationId", "--cursor": "cursor",
		} {
			if v := flagValue(args[1:], flagName); v != "" {
				q.Set(param, v)
			}
		}
		if v := flagValue(args[1:], "--limit"); v != "" {
			q.Set("limit", v)
		}
		path := "/v1/admin/audit-events"
		if enc := q.Encode(); enc != "" {
			path += "?" + enc
		}
		return gatewayGet(ctx, c, path, stdout, stderr)
	case "get":
		// §25 line 4931: GET /v1/admin/audit-events/{seq}.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: audit get requires <id>")
			return 2
		}
		return gatewayGet(ctx, c, "/v1/admin/audit-events/"+url.PathEscape(args[1]), stdout, stderr)
	case "summary":
		// §25 line 4932: GET /v1/admin/audit-events/summary.
		q := url.Values{}
		for flagName, param := range map[string]string{
			"--since": "since", "--until": "until", "--group-by": "groupBy",
		} {
			if v := flagValue(args[1:], flagName); v != "" {
				q.Set(param, v)
			}
		}
		path := "/v1/admin/audit-events/summary"
		if enc := q.Encode(); enc != "" {
			path += "?" + enc
		}
		return gatewayGet(ctx, c, path, stdout, stderr)
	case "retranslate":
		// §25 line 4933: POST /v1/admin/audit-events/{seq}/retranslate
		// with an optional translatorVersion.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: audit retranslate requires <id>")
			return 2
		}
		var body any
		if v := flagValue(args[2:], "--translator-version"); v != "" {
			body = map[string]any{"translatorVersion": v}
		}
		return gatewaySend(ctx, c, "POST",
			"/v1/admin/audit-events/"+url.PathEscape(args[1])+"/retranslate", body, stdout, stderr)
	case "republish":
		// §25 line 4934: POST /v1/admin/audit-events/{seq}/republish.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: audit republish requires <id>")
			return 2
		}
		return gatewaySend(ctx, c, "POST",
			"/v1/admin/audit-events/"+url.PathEscape(args[1])+"/republish", nil, stdout, stderr)
	case "drop-partition":
		// §25 line 4935: POST /v1/admin/audit-partitions/{partition}/drop
		// ?force=true with {acknowledgeDataLoss,partition}. Both --force
		// and --acknowledge-data-loss are mandatory so an unqualified
		// invocation can never discard un-forwarded audit rows.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: audit drop-partition requires <partition>")
			return 2
		}
		partition := args[1]
		if !hasFlag(args[2:], "--force") || !hasFlag(args[2:], "--acknowledge-data-loss") {
			fmt.Fprintln(stderr, "lenny-ctl: audit drop-partition requires --force --acknowledge-data-loss")
			return 2
		}
		path := "/v1/admin/audit-partitions/" + url.PathEscape(partition) + "/drop?force=true"
		body := map[string]any{"acknowledgeDataLoss": true, "partition": partition}
		return gatewaySend(ctx, c, "POST", path, body, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown audit subcommand %q\n", args[0])
		return 2
	}
}

// cmdEvents implements the §24.15 `events` group. The poll, SSE tail,
// and webhook-subscription subcommands target lenny-ops (§25.5); the
// `buffer` subcommand targets the gateway's §25.3 in-memory buffer.
// spec: §24.15 line 182; §25 lines 4920-4924. F-24.15.3.
func cmdEvents(ctx context.Context, flags globalFlags, gateway *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: events requires a subcommand (list|tail|buffer|subscriptions)")
		return 2
	}
	switch args[0] {
	case "buffer":
		// §24.15 line 182 — query the gateway's in-memory event buffer.
		return gatewayGet(ctx, gateway, "/v1/admin/events/buffer", stdout, stderr)
	case "list":
		// §25 line 4921: GET /v1/admin/events (polling) on lenny-ops.
		q := url.Values{}
		for flagName, param := range map[string]string{
			"--since": "since", "--until": "until", "--type": "eventType",
			"--severity": "severity", "--cursor": "cursor",
		} {
			if v := flagValue(args[1:], flagName); v != "" {
				q.Set(param, v)
			}
		}
		if v := flagValue(args[1:], "--limit"); v != "" {
			q.Set("limit", v)
		}
		path := "/v1/admin/events"
		if enc := q.Encode(); enc != "" {
			path += "?" + enc
		}
		return withOps(ctx, flags, gateway, stderr, func(ops *ctl.Client) int {
			return opsGet(ctx, ops, path, stdout, stderr)
		})
	case "tail":
		// §25 line 4920: GET /v1/admin/events/stream (SSE) on lenny-ops.
		// The stream runs until the server closes it or ctx is cancelled
		// (operator interrupt). Frames are written verbatim so operators
		// can pipe the raw SSE into downstream tooling.
		return withOps(ctx, flags, gateway, stderr, func(ops *ctl.Client) int {
			if err := ops.Stream(ctx, "/v1/admin/events/stream", stdout); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			return 0
		})
	case "subscriptions":
		return cmdEventSubscriptions(ctx, flags, gateway, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown events subcommand %q\n", args[0])
		return 2
	}
}

// cmdEventSubscriptions implements `events subscriptions list|get|create|
// delete`, the §25.5 webhook-subscription CRUD on lenny-ops. spec: §25
// lines 4922-4924. F-24.15.3.
func cmdEventSubscriptions(ctx context.Context, flags globalFlags, gateway *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: events subscriptions requires a verb (list|get|create|delete)")
		return 2
	}
	return withOps(ctx, flags, gateway, stderr, func(ops *ctl.Client) int {
		switch args[0] {
		case "list":
			return opsGet(ctx, ops, "/v1/admin/event-subscriptions", stdout, stderr)
		case "get":
			if len(args) < 2 {
				fmt.Fprintln(stderr, "lenny-ctl: events subscriptions get requires <id>")
				return 2
			}
			return opsGet(ctx, ops, "/v1/admin/event-subscriptions/"+url.PathEscape(args[1]), stdout, stderr)
		case "create":
			// §25 line 4923: POST {url, types[]}.
			target := flagValue(args[1:], "--url")
			if target == "" {
				fmt.Fprintln(stderr, "lenny-ctl: events subscriptions create requires --url <url>")
				return 2
			}
			body := map[string]any{"url": target}
			if types := flagValue(args[1:], "--types"); types != "" {
				body["eventTypes"] = splitCSV(types)
			}
			return opsSend(ctx, ops, "POST", "/v1/admin/event-subscriptions", body, stdout, stderr)
		case "delete":
			// §25 line 4924: DELETE /v1/admin/event-subscriptions/{id}.
			if len(args) < 2 {
				fmt.Fprintln(stderr, "lenny-ctl: events subscriptions delete requires <id>")
				return 2
			}
			return opsSend(ctx, ops, "DELETE", "/v1/admin/event-subscriptions/"+url.PathEscape(args[1]), nil, stdout, stderr)
		default:
			fmt.Fprintf(stderr, "lenny-ctl: unknown events subscriptions verb %q\n", args[0])
			return 2
		}
	})
}

// cmdUpgrade implements the §24.15 `upgrade` group and the §24.20
// answer-file replay. With --answers/--answer-file it replays an answer
// file against an existing install (preflight → values diff → `helm
// upgrade`, F-24.20.2). Otherwise it drives the platform-upgrade state
// machine on lenny-ops (check/preflight/start/proceed/pause/rollback/
// status/verify, F-24.15.4). spec: §24.15 line 185; §24.20 line 304;
// §25 lines 4967-4974.
func cmdUpgrade(ctx context.Context, flags globalFlags, gateway *ctl.Client, args []string, stdout, stderr io.Writer) int {
	// §24.20 line 304 — `upgrade --answers <file>` is the chart-level
	// replay, distinct from the platform state-machine subcommands. It is
	// selected by the presence of the answer-file flag rather than a
	// subcommand verb.
	if hasFlag(args, "--answers") || hasFlag(args, "--answer-file") {
		return cmdUpgradeReplay(args, stdout, stderr)
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: upgrade requires a subcommand (check|preflight|start|proceed|pause|rollback|status|verify) or --answers <file>")
		return 2
	}
	return withOps(ctx, flags, gateway, stderr, func(ops *ctl.Client) int {
		switch args[0] {
		case "check":
			return opsGet(ctx, ops, "/v1/admin/platform/upgrade-check", stdout, stderr)
		case "status":
			return opsGet(ctx, ops, "/v1/admin/platform/upgrade/status", stdout, stderr)
		case "preflight":
			version := flagValue(args[1:], "--version")
			if version == "" {
				fmt.Fprintln(stderr, "lenny-ctl: upgrade preflight requires --version <v>")
				return 2
			}
			return opsSend(ctx, ops, "POST", "/v1/admin/platform/upgrade/preflight",
				map[string]any{"version": version}, stdout, stderr)
		case "start":
			version := flagValue(args[1:], "--version")
			if version == "" {
				fmt.Fprintln(stderr, "lenny-ctl: upgrade start requires --version <v>")
				return 2
			}
			path := "/v1/admin/platform/upgrade/start"
			if hasFlag(args[1:], "--confirm") {
				path += "?confirm=true"
			}
			return opsSend(ctx, ops, "POST", path, map[string]any{"version": version}, stdout, stderr)
		case "proceed":
			return opsSend(ctx, ops, "POST", "/v1/admin/platform/upgrade/proceed", nil, stdout, stderr)
		case "pause":
			body := reasonBody(args[1:])
			return opsSend(ctx, ops, "POST", "/v1/admin/platform/upgrade/pause", body, stdout, stderr)
		case "rollback":
			// §25 line 4972 — rollback is destructive and takes --confirm;
			// an optional --reason is recorded on the transition.
			path := "/v1/admin/platform/upgrade/rollback"
			if hasFlag(args[1:], "--confirm") {
				path += "?confirm=true"
			}
			return opsSend(ctx, ops, "POST", path, reasonBody(args[1:]), stdout, stderr)
		case "verify":
			return opsSend(ctx, ops, "POST", "/v1/admin/platform/upgrade/verify", nil, stdout, stderr)
		default:
			fmt.Fprintf(stderr, "lenny-ctl: unknown upgrade subcommand %q\n", args[0])
			return 2
		}
	})
}

// reasonBody returns a {reason} body when --reason is present, else nil
// so the request carries no body.
func reasonBody(args []string) any {
	if r := flagValue(args, "--reason"); r != "" {
		return map[string]any{"reason": r}
	}
	return nil
}

// cmdUpgradeReplay implements `lenny-ctl upgrade --answers <file>`: the
// §24.20 line 304 answer-file replay against an existing install. It
// reuses the install wizard's answer parsing, value composition, and
// preflight phases, then renders the chart with `helm upgrade --dry-run`
// (the values diff) before applying with `helm upgrade`. spec: §24.20
// line 304. F-24.20.2.
func cmdUpgradeReplay(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseInstallFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: %v\n", err)
		return 2
	}
	if cfg.answerFile == "" {
		fmt.Fprintln(stderr, "lenny-ctl: upgrade --answers requires a file path")
		return 2
	}
	raw, err := os.ReadFile(cfg.answerFile)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: read answer file: %v\n", err)
		return 1
	}
	answers, err := parseAnswerFile(raw)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: parse answer file: %v\n", err)
		return 1
	}
	applyAnswerDefaults(&answers)
	if cfg.releaseName != "" {
		answers.Release.Name = cfg.releaseName
	}
	if cfg.namespace != "" {
		answers.Release.Namespace = cfg.namespace
	}
	if problems := validateAnswers(answers); len(problems) > 0 {
		fmt.Fprintln(stderr, "lenny-ctl: answer file is invalid:")
		for _, p := range problems {
			fmt.Fprintf(stderr, "  - %s\n", p)
		}
		return 1
	}
	values, err := composeValues(answers)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: compose values: %v\n", err)
		return 1
	}

	presetPath := filepath.Join(cfg.chartDir, "presets", "values-"+answers.Tier+".yaml")
	tmp, err := os.CreateTemp("", "lenny-upgrade-values-*.yaml")
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: temp values file: %v\n", err)
		return 1
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(values); err != nil {
		_ = tmp.Close()
		fmt.Fprintf(stderr, "lenny-ctl: write temp values: %v\n", err)
		return 1
	}
	_ = tmp.Close()

	fmt.Fprintln(stdout, "# Composed Helm values:")
	_, _ = stdout.Write(values)
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "# Tier preset layered under these values: %s\n", presetPath)
	applyArgs := helmUpgradeArgs(answers, cfg.chartDir, presetPath, tmp.Name(), false)
	fmt.Fprintf(stdout, "# Helm command:\n%s\n", "helm "+strings.Join(applyArgs, " "))

	if cfg.dryRun {
		fmt.Fprintln(stderr, "lenny-ctl: --dry-run set; not invoking helm")
		return 0
	}

	// Preflight against the resolved backends before mutating the
	// release (§24.20 line 304 "Runs preflight").
	if code := runInstallPreflight(context.Background(), installPreflightConfig(answers), infra.RealProbers(), stdout, stderr); code != 0 {
		return code
	}

	if _, err := exec.LookPath("helm"); err != nil {
		fmt.Fprintln(stderr, "lenny-ctl: helm not found on PATH; install Helm or re-run with --dry-run")
		return 1
	}
	// Render the values diff with `helm upgrade --dry-run` before the
	// destructive apply (§24.20 line 304 "renders the values diff").
	diff := exec.Command("helm", helmUpgradeArgs(answers, cfg.chartDir, presetPath, tmp.Name(), true)...)
	diff.Stdout = stdout
	diff.Stderr = stderr
	if err := diff.Run(); err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: helm upgrade --dry-run failed: %v\n", err)
		return 1
	}
	apply := exec.Command("helm", applyArgs...)
	apply.Stdout = stdout
	apply.Stderr = stderr
	if err := apply.Run(); err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: helm upgrade failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "lenny-ctl: release %q upgraded in namespace %q\n",
		answers.Release.Name, answers.Release.Namespace)
	return 0
}

// helmUpgradeArgs builds the `helm upgrade` argument vector. The tier
// preset is layered first and the composed per-question values second
// so the per-question values win on overlap, matching the install
// composition order (§17.6). dryRun appends --dry-run for the values
// diff render. F-24.20.2.
func helmUpgradeArgs(a installAnswers, chartDir, presetPath, valuesPath string, dryRun bool) []string {
	args := []string{
		"upgrade", a.Release.Name, chartDir,
		"--namespace", a.Release.Namespace,
		"-f", presetPath,
	}
	if valuesPath != "" {
		args = append(args, "-f", valuesPath)
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return args
}

// gatewayGet issues a GET to the gateway admin API and prints the JSON
// response.
func gatewayGet(ctx context.Context, c *ctl.Client, path string, stdout, stderr io.Writer) int {
	var out map[string]any
	if err := c.Do(ctx, "GET", path, nil, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

// gatewaySend issues a mutating request to the gateway admin API and
// prints the JSON response.
func gatewaySend(ctx context.Context, c *ctl.Client, method, path string, body any, stdout, stderr io.Writer) int {
	var out map[string]any
	if err := c.Do(ctx, method, path, body, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

// splitCSV splits a comma-separated flag value into a trimmed,
// empty-dropped slice.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}
