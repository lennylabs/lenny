// SPDX-License-Identifier: MIT

// Command lenny-ctl is the operator CLI for the Lenny gateway. It
// wraps the §15.1 admin API surface with the §24 command tree and the
// §25.14 operability command groups.
//
// Resource management lives under the `admin` group:
//
//	lenny-ctl admin tenants list|get|create
//	lenny-ctl admin runtimes list|get
//	lenny-ctl admin circuit-breakers list|open|close
//
// The §25.14 operability groups wrap the lenny-ops API:
//
//	lenny-ctl runbooks list|get
//	lenny-ctl locks list|acquire|release
//	lenny-ctl escalations list|create|resolve
//	lenny-ctl diagnose session|pool|connectivity|credential-pool
//	lenny-ctl drift report|validate|reconcile
//
// Standalone commands:
//
//	lenny-ctl health
//	lenny-ctl version
//	lenny-ctl bootstrap --from-values <file>
//	lenny-ctl install [--answer-file <file>]
//
// Auth: pass --bearer <token> for a clustered gateway, or rely on
// the dev-header path (--dev-tenant / --dev-roles) for Embedded
// Mode. The target gateway is --api-url (default
// http://localhost:8080).
//
// Routing (§25.14): health and version target the gateway directly.
// The operability groups target lenny-ops. The ops URL comes from the
// --ops-server flag, or is auto-discovered from the gateway's
// GET /v1/admin/platform/version response (the opsServiceURL field).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/lennylabs/lenny/pkg/ctl"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags, rest := parseGlobalFlags(args)
	if len(rest) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	client := ctl.New(ctl.Options{
		BaseURL:   flags.apiURL,
		Bearer:    flags.bearer,
		DevTenant: flags.devTenant,
		DevRoles:  flags.devRoles,
		Timeout:   30 * time.Second,
	})
	ctx := context.Background()

	switch rest[0] {
	case "health":
		return cmdHealth(ctx, client, stdout, stderr)
	case "version":
		return cmdVersion(ctx, client, stdout, stderr)
	case "bootstrap":
		return cmdBootstrap(ctx, client, rest[1:], stdout, stderr)
	case "install":
		// install runs no gateway calls during values composition; it
		// shells out to helm. It is dispatched here so it shares the
		// global-flag parsing but ignores the gateway client.
		return cmdInstall(rest[1:], os.Stdin, stdout, stderr)
	case "runtime":
		// runtime init and validate are local; runtime publish reaches
		// the gateway. The group is dispatched with the gateway client
		// and ignores it for the local subcommands.
		return cmdRuntime(ctx, client, rest[1:], stdout, stderr)
	case "admin":
		return cmdAdmin(ctx, client, rest[1:], stdout, stderr)
	// §25.14 operability command groups — these target lenny-ops, not
	// the gateway. opsClient resolves the ops URL (the --ops-server flag
	// or auto-discovery) on first use.
	case "runbooks":
		return withOps(ctx, flags, client, stderr, func(ops *ctl.Client) int {
			return cmdRunbooks(ctx, ops, rest[1:], stdout, stderr)
		})
	case "locks":
		return withOps(ctx, flags, client, stderr, func(ops *ctl.Client) int {
			return cmdLocks(ctx, ops, rest[1:], stdout, stderr)
		})
	case "escalations":
		return withOps(ctx, flags, client, stderr, func(ops *ctl.Client) int {
			return cmdEscalations(ctx, ops, rest[1:], stdout, stderr)
		})
	case "diagnose":
		return withOps(ctx, flags, client, stderr, func(ops *ctl.Client) int {
			return cmdDiagnose(ctx, ops, rest[1:], stdout, stderr)
		})
	case "drift":
		return withOps(ctx, flags, client, stderr, func(ops *ctl.Client) int {
			return cmdDrift(ctx, ops, rest[1:], stdout, stderr)
		})
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown command %q\n\n%s\n", rest[0], usage)
		return 2
	}
}

const usage = `lenny-ctl — Lenny gateway operator CLI

Usage:
  lenny-ctl [global flags] <command> [args]

Global flags:
  --api-url <url>      Gateway base URL (default http://localhost:8080)
  --ops-server <url>   lenny-ops base URL (auto-discovered when omitted)
  --bearer <token>     Operator bearer token
  --dev-tenant <id>    Dev-header tenant (Embedded Mode)
  --dev-roles <roles>  Dev-header roles, comma-separated (Embedded Mode)

Gateway commands:
  health                                Print the platform health report
  version                               Print the gateway version
  bootstrap --from-values <f> [--wait-timeout <secs>]
                                        Apply a seed file (tenants/runtimes/users); --wait-timeout defaults to 120s (§17.6)
  install [--answer-file <f>]           Run the installation wizard (§17.6)
  runtime init <name> --language <l> --template <t>   Scaffold a runtime repo
  runtime validate [<path>]             Statically validate a runtime repo
  runtime publish <name> --image <ref>  Push and register a runtime
  admin tenants list                    List tenants
  admin tenants get <id>                Get a tenant
  admin tenants create <id> [name]      Create a tenant
  admin runtimes list                   List runtimes
  admin runtimes get <name>             Get a runtime
  admin runtimes register --manifest <f>  Register a runtime from a runtime.yaml
  admin pools list                      List pool configurations
  admin pools get <name>                Get a pool configuration
  admin pools create --from-file <f>    Create a pool from a JSON file
  admin pools update <name> --from-file <f>  Replace a pool's configuration
  admin pools delete <name>             Delete a pool
  admin pools drain <name>              Drain a pool (no new assignments)
  admin pools sync-status <name>        Show Postgres↔CRD reconciliation state
  admin pools resume-reconciliation <name>  Clear PoolScalingAdmissionStuck state
  admin circuit-breakers list           List circuit breakers
  admin circuit-breakers open <name> --limit-tier <t> --scope <k>=<v> --reason <text>
  admin circuit-breakers close <name>   Close a circuit breaker

Operability commands (§25.14, target lenny-ops):
  runbooks list [--alert <name>]        List runbooks (optionally by alert)
  runbooks get <name>                   Print a runbook
  locks list                            List remediation locks
  locks acquire --scope <s> --op <op>   Acquire a remediation lock
  locks release <id>                    Release a remediation lock
  escalations list                      List escalations
  escalations create --severity <s> --summary <text>
  escalations resolve <id>              Mark an escalation resolved
  diagnose session <id>                 Diagnose a session
  diagnose pool <name>                  Diagnose a pool
  diagnose connectivity                 Check dependency connectivity
  diagnose credential-pool <name>       Diagnose a credential pool
  drift report [--scope <s>] [--against <live|target|both>]
  drift validate --desired <file>       Validate desired state
  drift reconcile [--scope <s>] [--confirm]`

type globalFlags struct {
	apiURL    string
	opsServer string
	bearer    string
	devTenant string
	devRoles  string
}

// parseGlobalFlags pulls the leading --flag value pairs off args and
// returns the remainder as the command + its arguments.
func parseGlobalFlags(args []string) (globalFlags, []string) {
	f := globalFlags{apiURL: "http://localhost:8080"}
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--api-url":
			if i+1 < len(args) {
				f.apiURL = args[i+1]
				i += 2
				continue
			}
		case "--ops-server":
			if i+1 < len(args) {
				f.opsServer = args[i+1]
				i += 2
				continue
			}
		case "--bearer":
			if i+1 < len(args) {
				f.bearer = args[i+1]
				i += 2
				continue
			}
		case "--bearer-file":
			// Read the bearer token from a file. The lenny-bootstrap Job
			// mounts the operator-token Secret and passes its path here:
			// the distroless image has no shell to read the file inline.
			if i+1 < len(args) {
				if tok, err := os.ReadFile(args[i+1]); err == nil {
					f.bearer = strings.TrimSpace(string(tok))
				}
				i += 2
				continue
			}
		case "--dev-tenant":
			if i+1 < len(args) {
				f.devTenant = args[i+1]
				i += 2
				continue
			}
		case "--dev-roles":
			if i+1 < len(args) {
				f.devRoles = args[i+1]
				i += 2
				continue
			}
		}
		break
	}
	return f, args[i:]
}

func cmdHealth(ctx context.Context, c *ctl.Client, stdout, stderr io.Writer) int {
	var report map[string]any
	if err := c.Do(ctx, "GET", "/v1/admin/health", nil, &report); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, report)
	if report["status"] == "unhealthy" {
		return 1
	}
	return 0
}

func cmdVersion(ctx context.Context, c *ctl.Client, stdout, stderr io.Writer) int {
	var v map[string]any
	if err := c.Do(ctx, "GET", "/v1/admin/platform/version", nil, &v); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, v)
	return 0
}

// cmdAdmin dispatches the §24 `admin` resource-management group.
func cmdAdmin(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: admin requires a resource (tenants|runtimes|pools|circuit-breakers)")
		return 2
	}
	switch args[0] {
	case "tenants":
		return cmdTenants(ctx, c, args[1:], stdout, stderr)
	case "runtimes":
		return cmdRuntimes(ctx, c, args[1:], stdout, stderr)
	case "pools":
		return cmdPools(ctx, c, args[1:], stdout, stderr)
	case "circuit-breakers":
		return cmdCircuitBreakers(ctx, c, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown admin resource %q\n", args[0])
		return 2
	}
}

func cmdTenants(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: tenants requires a subcommand (list|get|create)")
		return 2
	}
	switch args[0] {
	case "list":
		var out map[string]any
		if err := c.Do(ctx, "GET", "/v1/admin/tenants", nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: tenants get requires <id>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "GET", "/v1/admin/tenants/"+args[1], nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "create":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: tenants create requires <id>")
			return 2
		}
		body := map[string]string{"id": args[1]}
		if len(args) >= 3 {
			body["displayName"] = args[2]
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/tenants", body, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown tenants subcommand %q\n", args[0])
		return 2
	}
	return 0
}

func cmdRuntimes(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: runtimes requires a subcommand (list|get|register)")
		return 2
	}
	switch args[0] {
	case "list":
		var out map[string]any
		if err := c.Do(ctx, "GET", "/v1/admin/runtimes", nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: runtimes get requires <name>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "GET", "/v1/admin/runtimes/"+args[1], nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "register":
		// register creates a runtime definition from a runtime.yaml
		// (POST /v1/admin/runtimes). `lenny runtime publish` wraps the
		// same path, adding a docker push (§24.18).
		return cmdRuntimesRegister(ctx, c, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown runtimes subcommand %q\n", args[0])
		return 2
	}
	return 0
}

// cmdRuntimesRegister implements `lenny-ctl admin runtimes register`.
// It reads a runtime.yaml manifest, optionally overrides the image, and
// registers the runtime against the gateway via registerRuntime.
func cmdRuntimesRegister(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	var manifestPath, image string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--manifest", "--from-file":
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "lenny-ctl: %s requires a path\n", args[i])
				return 2
			}
			manifestPath, i = args[i+1], i+1
		case "--image":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "lenny-ctl: --image requires a value")
				return 2
			}
			image, i = args[i+1], i+1
		default:
			fmt.Fprintf(stderr, "lenny-ctl: unknown runtimes register flag %q\n", args[i])
			return 2
		}
	}
	if manifestPath == "" {
		fmt.Fprintln(stderr, "lenny-ctl: runtimes register requires --manifest <runtime.yaml>")
		return 2
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: read %s: %v\n", manifestPath, err)
		return 1
	}
	var body map[string]any
	if err := yaml.Unmarshal(raw, &body); err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: %s is not valid YAML: %v\n", manifestPath, err)
		return 1
	}
	if image != "" {
		body["image"] = image
	}
	return registerRuntime(ctx, c, body, stdout, stderr)
}

// cmdPools implements the §24.4 `lenny-ctl admin pools` group. spec:
// spec/24_lenny-ctl-command-reference.md lines 61-78. v1 covers the
// CRUD primitives, drain, sync-status, and resume-reconciliation. Each
// subcommand maps 1:1 to the §15.1 admin REST surface
// (spec/15_external-api-surface.md lines 792-799).
func cmdPools(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: pools requires a subcommand (list|get|create|update|delete|drain|sync-status|resume-reconciliation)")
		return 2
	}
	switch args[0] {
	case "list":
		var out map[string]any
		if err := c.Do(ctx, "GET", "/v1/admin/pools", nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: pools get requires <name>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "GET", "/v1/admin/pools/"+url.PathEscape(args[1]), nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "create":
		// spec: §15.1 line 792 — POST /v1/admin/pools accepts the pool
		// definition as JSON. --from-file reads the body from a local
		// file so an operator can keep pool definitions under source
		// control.
		path := flagValue(args[1:], "--from-file")
		if path == "" {
			fmt.Fprintln(stderr, "lenny-ctl: pools create requires --from-file <pool.json>")
			return 2
		}
		body, err := readJSONFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: %v\n", err)
			return 1
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/pools", body, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "update":
		// spec: §15.1 line 795 — PUT /v1/admin/pools/{name} requires the
		// updated pool definition in the body.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: pools update requires <name> --from-file <pool.json>")
			return 2
		}
		path := flagValue(args[2:], "--from-file")
		if path == "" {
			fmt.Fprintln(stderr, "lenny-ctl: pools update requires --from-file <pool.json>")
			return 2
		}
		body, err := readJSONFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: %v\n", err)
			return 1
		}
		var out map[string]any
		if err := c.Do(ctx, "PUT", "/v1/admin/pools/"+url.PathEscape(args[1]), body, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "delete":
		// spec: §15.1 line 796 — DELETE /v1/admin/pools/{name}.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: pools delete requires <name>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "DELETE", "/v1/admin/pools/"+url.PathEscape(args[1]), nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "drain":
		// spec: §15.1 line 797 — POST /v1/admin/pools/{name}/drain.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: pools drain requires <name>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/pools/"+url.PathEscape(args[1])+"/drain", nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "sync-status":
		// spec: §15.1 line 798 — GET /v1/admin/pools/{name}/sync-status.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: pools sync-status requires <name>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "GET", "/v1/admin/pools/"+url.PathEscape(args[1])+"/sync-status", nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "resume-reconciliation":
		// spec: §15.1 line 799 — POST /v1/admin/pools/{name}/resume-reconciliation.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: pools resume-reconciliation requires <name>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/pools/"+url.PathEscape(args[1])+"/resume-reconciliation", nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown pools subcommand %q\n", args[0])
		return 2
	}
	return 0
}

// cmdCircuitBreakers implements the §24.7 circuit-breaker commands.
func cmdCircuitBreakers(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: circuit-breakers requires a subcommand (list|open|close)")
		return 2
	}
	switch args[0] {
	case "list":
		var out map[string]any
		if err := c.Do(ctx, "GET", "/v1/admin/circuit-breakers", nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "open":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: circuit-breakers open requires <name>")
			return 2
		}
		body, err := parseOpenBreaker(args[2:])
		if err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: %v\n", err)
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/circuit-breakers/"+args[1]+"/open", body, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "close":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: circuit-breakers close requires <name>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/circuit-breakers/"+args[1]+"/close", nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown circuit-breakers subcommand %q\n", args[0])
		return 2
	}
	return 0
}

// parseOpenBreaker builds the §15.1 open-breaker request body from the
// --limit-tier, --scope, and --reason flags.
func parseOpenBreaker(args []string) (map[string]any, error) {
	var reason, limitTier string
	scope := map[string]string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--reason":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--reason requires a value")
			}
			reason, i = args[i+1], i+1
		case "--limit-tier":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--limit-tier requires a value")
			}
			limitTier, i = args[i+1], i+1
		case "--scope":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--scope requires a <key>=<value> pair")
			}
			k, v, ok := strings.Cut(args[i+1], "=")
			if !ok || k == "" {
				return nil, fmt.Errorf("--scope must be <key>=<value>, got %q", args[i+1])
			}
			scope[k], i = v, i+1
		default:
			return nil, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	// spec: §24.7 line 106 — `--reason`, `--scope`, and `--limit-tier`
	// are all listed unbracketed (required) in the command syntax. The
	// client enforces the contract up front so an operator sees a
	// single deterministic error rather than reaching the gateway with
	// a partial payload (empty reason silently degrades the audit
	// trail; empty scope produces a 422 from the gateway). F-24.7.1 /
	// F-24.7.2.
	if limitTier == "" {
		return nil, fmt.Errorf("circuit-breakers open requires --limit-tier")
	}
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("circuit-breakers open requires --reason <text>")
	}
	if len(scope) == 0 {
		return nil, fmt.Errorf("circuit-breakers open requires --scope <key>=<value>")
	}
	return map[string]any{"reason": reason, "limit_tier": limitTier, "scope": scope}, nil
}

func cmdBootstrap(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	var fromValues string
	// spec: §17.6 line 421 — --wait-timeout (default 120s) for the
	// bootstrap readiness poll.
	const defaultWaitTimeoutSeconds = 120
	waitSeconds := defaultWaitTimeoutSeconds
	// spec: §24.1 line 35 — --dry-run maps to ?dryRun=true; §17.6 line 450
	// — --force-update maps to ?forceUpdate=true (overwrite differing
	// fields on an existing resource instead of skipping).
	var dryRun, forceUpdate bool
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--from-values" && i+1 < len(args):
			fromValues = args[i+1]
			i++
		case args[i] == "--wait-timeout" && i+1 < len(args):
			if n, err := strconv.Atoi(args[i+1]); err == nil {
				waitSeconds = n
			}
			i++
		case args[i] == "--dry-run":
			dryRun = true
		case args[i] == "--force-update":
			forceUpdate = true
		}
	}
	if fromValues == "" {
		fmt.Fprintln(stderr, "lenny-ctl: bootstrap requires --from-values <file>")
		return 2
	}
	// --wait-timeout polls the gateway's health endpoint until it is
	// ready or the deadline elapses. The lenny-bootstrap Job needs this
	// because it runs from a distroless image with no shell to poll
	// from, and it is a post-install hook that races the gateway
	// Deployment.
	if waitSeconds > 0 {
		deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
		fmt.Fprintf(stderr, "lenny-ctl: waiting up to %ds for the gateway\n", waitSeconds)
		for {
			// /healthz is the unauthenticated liveness endpoint; a 2xx
			// means the gateway is serving. The richer /v1/admin/health
			// report is admin-auth-gated and not needed for a readiness
			// poll.
			if err := c.Do(ctx, "GET", "/healthz", nil, nil); err == nil {
				break
			}
			if time.Now().After(deadline) {
				fmt.Fprintf(stderr, "lenny-ctl: gateway not ready after %ds\n", waitSeconds)
				return 1
			}
			time.Sleep(3 * time.Second)
		}
	}
	raw, err := os.ReadFile(fromValues)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: read %s: %v\n", fromValues, err)
		return 1
	}
	// sigs.k8s.io/yaml.Unmarshal accepts both YAML and JSON: it
	// converts YAML to JSON internally before decoding. The spec names
	// a bootstrap-values.yaml file (§17.6); pre-existing JSON seed
	// files keep parsing unchanged.
	var body any
	if err := yaml.Unmarshal(raw, &body); err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: %s is not valid YAML or JSON: %v\n", fromValues, err)
		return 1
	}
	// spec: §24.1 line 35; §15.1 line 1140 — dryRun/forceUpdate are query
	// parameters on POST /v1/admin/bootstrap.
	path := "/v1/admin/bootstrap"
	if q := bootstrapQuery(dryRun, forceUpdate); q != "" {
		path += "?" + q
	}
	var out bootstrapResult
	if err := c.Do(ctx, "POST", path, body, &out); err != nil {
		// A 4xx/5xx (whole-body validation, auth, transport) is a
		// validation error per §17.6 line 420 exit-code 1.
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	// spec: §17.6 lines 449-450 — log INFO/WARN per skipped resource so
	// operators see why a re-run left existing resources unchanged.
	out.logSkips(stderr)
	// spec: §17.6 line 420 — exit 0 = all seeded, 1 = validation error,
	// 2 = partial failure.
	return out.exitCode()
}

// bootstrapQuery builds the dryRun/forceUpdate query string.
func bootstrapQuery(dryRun, forceUpdate bool) string {
	q := url.Values{}
	if dryRun {
		q.Set("dryRun", "true")
	}
	if forceUpdate {
		q.Set("forceUpdate", "true")
	}
	return q.Encode()
}

// bootstrapResult mirrors the §15.1 POST /v1/admin/bootstrap response. The
// CLI decodes only the fields it needs to log skips and compute the exit
// code, keeping the client thin (no dependency on the gateway package).
type bootstrapResult struct {
	Tenants         bootstrapSection `json:"tenants"`
	Runtimes        bootstrapSection `json:"runtimes"`
	Users           bootstrapSection `json:"users"`
	CredentialPools bootstrapSection `json:"credentialPools"`
}

type bootstrapSection struct {
	CreatedCount int             `json:"createdCount"`
	UpdatedCount int             `json:"updatedCount"`
	SkippedCount int             `json:"skippedCount"`
	Errors       []bootstrapErr  `json:"errors"`
	Skipped      []bootstrapSkip `json:"skipped"`
}

type bootstrapErr struct {
	ID      string `json:"id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type bootstrapSkip struct {
	ID                string   `json:"id"`
	Code              string   `json:"code"`
	ConflictingFields []string `json:"conflictingFields"`
}

func (b bootstrapResult) sections() map[string]bootstrapSection {
	return map[string]bootstrapSection{
		"tenant":         b.Tenants,
		"runtime":        b.Runtimes,
		"user":           b.Users,
		"credentialPool": b.CredentialPools,
	}
}

// logSkips prints the §17.6 line 450 WARN line for every resource that
// was left unchanged because its seed fields differ and --force-update
// was not supplied.
func (b bootstrapResult) logSkips(stderr io.Writer) {
	for kind, s := range b.sections() {
		for _, sk := range s.Skipped {
			fmt.Fprintf(stderr,
				"WARN resource %s/%s: exists with differing fields %v; skipping (use --force-update to overwrite)\n",
				kind, sk.ID, sk.ConflictingFields)
		}
	}
}

// exitCode maps the bootstrap response to the §17.6 line 420 exit codes:
// 0 = all resources seeded (created, updated, or already-present skips),
// 1 = validation error (a security-critical block, or a pure-failure run
// where nothing succeeded), 2 = partial failure (some succeeded, some
// failed operationally).
func (b bootstrapResult) exitCode() int {
	succeeded, failed, securityCritical := 0, 0, false
	for _, s := range b.sections() {
		succeeded += s.CreatedCount + s.UpdatedCount + s.SkippedCount
		failed += len(s.Errors)
		for _, e := range s.Errors {
			if e.Code == "SEED_SECURITY_CRITICAL_FIELD" {
				securityCritical = true
			}
		}
	}
	if securityCritical {
		return 1
	}
	if failed == 0 {
		return 0
	}
	if succeeded > 0 {
		return 2
	}
	return 1
}

func printJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
