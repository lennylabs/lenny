// SPDX-License-Identifier: MIT

// Package ctlcli implements the unified Lenny CLI dispatcher. The §24
// preamble (line 17) requires both invocation names, `lenny` (short
// name, Embedded Mode ergonomics) and `lenny-ctl` (long name, operator
// context), to support every subcommand. Both `cmd/lenny` and
// `cmd/lenny-ctl` are thin shims over Run here, so the two names share
// one command surface: the §15.1 admin API tree (§24), the §25.14
// operability groups, and the §24.19 Embedded Mode local commands
// (delegated to pkg/embedded/localcli).
//
// Resource management lives under the `admin` group:
//
//	lenny-ctl admin tenants list|get|create|delete|force-delete|rotate-erasure-salt
//	lenny-ctl admin runtimes list|get
//	lenny-ctl admin pools list|get|set-warm-count|...
//	lenny-ctl admin sessions get|force-terminate
//	lenny-ctl admin quota reconcile
//	lenny-ctl admin circuit-breakers list|open|close
//
// The §25.14 / §24.15 operability groups wrap the gateway or lenny-ops API:
//
//	lenny-ctl operations list|get
//	lenny-ctl runbooks list|get
//	lenny-ctl locks list|get|acquire|release|steal
//	lenny-ctl escalations list|create|resolve
//	lenny-ctl diagnose session|pool|connectivity|credential-pool
//	lenny-ctl drift report|validate|snapshot refresh|reconcile
//	lenny-ctl backup list|get|create|verify|schedule|policy
//	lenny-ctl restore safety-check|preview|execute|status|resume|confirm-legal-hold-ledger
//	lenny-ctl logs pods <namespace> <name>
//	lenny-ctl mcp-management tools|call
//
// Standalone commands:
//
//	lenny-ctl health
//	lenny-ctl version          (local CLI build; offline)
//	lenny-ctl bootstrap --from-values <file>
//	lenny-ctl install [--answers <file>]
//
// Auth: pass --bearer <token> for a clustered gateway, or rely on
// the dev-header path (--dev-tenant / --dev-roles) for Embedded
// Mode. The target gateway is --api-url (default
// http://localhost:8080).
//
// Routing (§25.14): health targets the gateway directly; version is a
// local-only command that prints the CLI build. The operability groups
// target lenny-ops. The ops URL comes from the --ops-server flag, or is
// auto-discovered from the gateway's GET /v1/admin/platform/version
// response (the opsServiceURL field).
package ctlcli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/lennylabs/lenny/pkg/ctl"
	"github.com/lennylabs/lenny/pkg/embedded/localcli"
)

// version is the CLI build version, printed by the version command. The
// release pipeline stamps the per-binary `main.version` symbol via
// -ldflags "-X main.version=<tag>"; each thin shim (cmd/lenny,
// cmd/lenny-ctl) passes its stamped value into Run, which sets this
// package var. Source builds without the ldflag report "dev". The §17.6
// krew CI invariant asserts that `kubectl lenny --version` reports the
// release tag.
// spec: §24.0 line 23, §17.6 line 360.
var version = "dev"

// Run is the unified CLI entry point both binary names invoke. ver is the
// build version each thin shim stamps via its -X main.version ldflag.
// Run honors every subcommand under either invocation name per the §24
// preamble line 17 contract that "both names support every subcommand".
func Run(args []string, stdout, stderr io.Writer, ver string) int {
	version = ver
	return run(args, stdout, stderr)
}

// progName reports the invocation name the binary should print in its
// version line. The same build ships as lenny (short name), lenny-ctl
// (standalone, Homebrew), and kubectl-lenny (krew); the version string
// names whichever was run. Under `go test` the binary name does not match
// any known prefix and falls through to lenny-ctl.
// spec: §24 preamble line 17, §17.6 line 358 (one build, multiple names).
func progName() string {
	base := filepath.Base(os.Args[0])
	switch {
	case strings.HasPrefix(base, "kubectl-lenny"):
		return "kubectl-lenny"
	case strings.HasPrefix(base, "lenny-ctl"):
		return "lenny-ctl"
	case strings.HasPrefix(base, "lenny"):
		return "lenny"
	default:
		return "lenny-ctl"
	}
}

func run(args []string, stdout, stderr io.Writer) int {
	// §24.19 line 256 / §17.4: the embedded control plane runs as in-cluster
	// pods that outlive the CLI, so `lenny up` brings the stack up in-process
	// and returns; there is no detached host supervisor to re-exec into under
	// either binary name.
	flags, rest := parseGlobalFlags(args)
	if len(rest) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	ctx := context.Background()
	// §24.15 line 192: `lenny-ctl logs pods <namespace> <name>` proxies a
	// pod's container logs from lenny-ops (GET /v1/admin/logs/pods/...).
	// This clustered operability command is distinct from the §24.19
	// embedded `logs <component>` stack-log tailer that localcli claims
	// below. The `pods` subcommand disambiguates the two: `pods` is never a
	// valid embedded component, so a clustered proxy invocation cannot
	// shadow the embedded tailer and the embedded `logs <component>` form
	// still reaches localcli unchanged.
	if rest[0] == "logs" && len(rest) > 1 && rest[1] == "pods" {
		return withOps(ctx, flags, newClient(flags), stderr, func(ops *ctl.Client) int {
			return cmdLogs(ctx, ops, rest[2:], stdout, stderr)
		})
	}
	// §17.4 / §24.18: the `runtime` token is shared. `lenny runtime apply`
	// is the Embedded Mode CRD-apply verb localcli owns (it materializes a
	// runtime's Runtime/SandboxTemplate/SandboxWarmPool against the embedded
	// kubeconfig), while `runtime init|validate|publish` are the
	// gateway-and-scaffold runtime-author commands cmdRuntime owns below.
	// localcli.Local claims the whole `runtime` token, so this disambiguation
	// routes only `runtime apply` to localcli and lets every other runtime
	// subcommand fall through to the cmdRuntime case, mirroring the `logs
	// pods` split above. spec: §17.4 (the runtime-apply verb), §24.18 (the
	// runtime-author group).
	if rest[0] == "runtime" && len(rest) > 1 && rest[1] == "apply" {
		return localcli.Run(ctx, "runtime", rest[1:], stdout, stderr, version)
	}
	// §24.19 line 266 / §24.9 line 120: `lenny-ctl <local-command>`
	// behaves identically to `lenny <local-command>` against the
	// Embedded Mode stack. The gateway-targeting global flags do not
	// apply to local commands, so delegate the raw arguments after the
	// command name. The shared `runtime` token is excluded here: it is
	// dispatched by the disambiguation above (apply) and the cmdRuntime case
	// below (init/validate/publish), so a bare Local("runtime") claim must
	// not shadow the runtime-author group.
	if localcli.Local(rest[0]) && rest[0] != "runtime" {
		return localcli.Run(ctx, rest[0], rest[1:], stdout, stderr, version)
	}
	// §24.16 line 205: only the "json" output format is supported.
	if flags.output != "" && flags.output != "json" {
		fmt.Fprintf(stderr, "lenny-ctl: unsupported --output %q (only \"json\" is supported)\n", flags.output)
		return 2
	}
	client := newClient(flags)

	switch rest[0] {
	case "health":
		return cmdHealth(ctx, client, stdout, stderr)
	case "version", "--version", "-V", "-v":
		// version prints the local CLI build and runs offline. It is not
		// routed to the gateway: §25.14's routing table covers health and
		// recommendations, not version, and the §17.6 krew check requires
		// `kubectl lenny --version` to succeed before any gateway exists.
		// spec: §24.0 line 23, §17.6 line 360.
		return cmdVersion(stdout)
	case "bootstrap":
		return cmdBootstrap(ctx, client, rest[1:], stdout, stderr, flags.quiet)
	case "install":
		// install runs no gateway calls during values composition; it
		// shells out to helm. It is dispatched here so it shares the
		// global-flag parsing but ignores the gateway client.
		return cmdInstall(rest[1:], os.Stdin, stdout, stderr)
	case "preflight":
		// §24.2: preflight runs in two modes. When the gateway is
		// reachable it delegates to POST /v1/admin/preflight via the
		// client; otherwise it probes Postgres/Redis/MinIO locally and,
		// against a reachable cluster, runs the §10.5 CRD-currency check.
		return cmdPreflight(ctx, client, rest[1:], stdout, stderr)
	case "runtime":
		// runtime init and validate are local; runtime publish reaches
		// the gateway. The group is dispatched with the gateway client
		// and ignores it for the local subcommands.
		return cmdRuntime(ctx, client, rest[1:], stdout, stderr)
	case "admin":
		return cmdAdmin(ctx, client, rest[1:], stdout, stderr, flags.quiet)
	case "migrate":
		return cmdMigrate(ctx, client, rest[1:], stdout, stderr)
	case "policy":
		// §24.14 policy group: `audit-isolation` reports DelegationPolicy
		// rule × pool combinations that fail the §8.3 isolation
		// monotonicity check. Read-only, platform-admin, gateway admin
		// API (client-side join). spec: §24.14 line 172.
		return cmdPolicy(ctx, client, rest[1:], stdout, stderr)
	case "operations":
		// §24.15 operations group: list active long-running operations and
		// fetch one by id. The operations inventory is hosted by lenny-ops
		// (§15.1 line 909), so the group resolves the ops endpoint via the
		// §24.16 --ops-server auto-discovery. spec: §24.15 line 181.
		return cmdOperations(ctx, flags, client, rest[1:], stdout, stderr)
	case "me":
		// §24.15 me group: caller identity and authorized tools target the
		// gateway admin API, while `me operations` targets the lenny-ops
		// inventory (§15.1 line 909). cmdMe routes each. spec: §24.15
		// line 180.
		return cmdMe(ctx, flags, client, rest[1:], stdout, stderr)
	case "audit":
		// §24.15 audit group: query/summary and the OCSF/EventBus/
		// partition remediation verbs. Gateway admin API. spec: §24.15
		// line 186; §25.9.
		return cmdAudit(ctx, client, rest[1:], stdout, stderr)
	case "events":
		// §24.15 events group: the poll, SSE tail, and webhook
		// subscriptions target lenny-ops; the buffer subcommand targets
		// the gateway. cmdEvents routes each subcommand itself. spec:
		// §24.15 line 182; §25.5.
		return cmdEvents(ctx, flags, client, rest[1:], stdout, stderr)
	case "upgrade":
		// §24.15 upgrade group: the platform-upgrade state machine on
		// lenny-ops, plus the §24.20 `upgrade --answers` chart replay.
		// cmdUpgrade routes both. spec: §24.15 line 185; §24.20 line 304.
		return cmdUpgrade(ctx, flags, client, rest[1:], stdout, stderr)
	case "slo":
		// slo export renders the §16.10 OpenSLO documents from the
		// embedded §16.5 SLO catalog and runs offline; it reaches no
		// gateway. spec: §16.10 lines 732-736.
		return cmdSLO(rest[1:], stdout, stderr)
	case "values":
		// values validate checks a values.yaml against the chart's
		// generated values.schema.json (§17.6). Runs offline; reaches no
		// gateway. spec: §24.20 line 303, §17.6 line 666.
		return cmdValues(rest[1:], stdout, stderr)
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
	case "doctor":
		// §24.2 rows 2-3: doctor runs the §25.6 diagnostic suite via
		// POST /v1/admin/diagnostics/run on lenny-ops; --fix applies the
		// auto-remediations. spec: §24.2 lines 44-45; §25.6 lines 2941-2982.
		return withOps(ctx, flags, client, stderr, func(ops *ctl.Client) int {
			return cmdDoctor(ctx, ops, rest[1:], stdout, stderr)
		})
	case "drift":
		return withOps(ctx, flags, client, stderr, func(ops *ctl.Client) int {
			return cmdDrift(ctx, ops, rest[1:], stdout, stderr)
		})
	case "backup":
		return withOps(ctx, flags, client, stderr, func(ops *ctl.Client) int {
			return cmdBackup(ctx, ops, rest[1:], stdout, stderr)
		})
	case "restore":
		return withOps(ctx, flags, client, stderr, func(ops *ctl.Client) int {
			return cmdRestore(ctx, ops, rest[1:], stdout, stderr)
		})
	case "mcp-management":
		return withOps(ctx, flags, client, stderr, func(ops *ctl.Client) int {
			return cmdMCPManagement(ctx, ops, rest[1:], stdout, stderr)
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

Global flags (env fallbacks in parentheses):
  --api-url <url>      Gateway base URL (LENNY_API_URL; default http://localhost:8080)
  --ops-server <url>   lenny-ops base URL (LENNY_OPS_URL; auto-discovered when omitted)
  --token <token>      Operator bearer token (LENNY_API_TOKEN); --bearer is an alias
  --dev-tenant <id>    Dev-header tenant (Embedded Mode)
  --dev-roles <roles>  Dev-header roles, comma-separated (Embedded Mode)
  --timeout <secs>     Per-request timeout in seconds (default 30)
  --insecure-skip-verify  Skip TLS certificate verification (dev only)
  --output <format>    Output format; only "json" is supported (default json)
  --quiet              Suppress informational (non-result) messages

Gateway commands:
  health                                Print the platform health report
  version                               Print the local CLI build version (offline)
  bootstrap --from-values <f> [--wait-timeout <secs>]
                                        Apply a seed file (tenants/runtimes/users); --wait-timeout defaults to 120s (§17.6)
  install [--answers <f>]               Run the installation wizard (§17.6)
  preflight [--config <values.yaml>]    Assert installed CRD schema-version currency before helm upgrade (§10.5)
  values validate --config <values.yaml>  Validate a values.yaml against the chart's values.schema.json (§17.6, §24.20)
  runtime init <name> --language <l> --template <t>   Scaffold a runtime repo
  runtime validate [<path>] [--binary <adapter>] [--report <f>]
                                        Validate a runtime repo; --binary runs the §15.4.6 conformance probe
  runtime publish <name> --image <ref>  Push and register a runtime
  admin tenants list                    List tenants
  admin tenants get <id>                Get a tenant
  admin tenants create <id> [name]      Create a tenant
  admin tenants delete <id>             Initiate the §12.8 tenant-deletion lifecycle
  admin runtimes list                   List runtimes
  admin runtimes get <name>             Get a runtime
  admin runtimes register --manifest <f>  Register a runtime from a runtime.yaml
  admin runtimes grant-access --runtime <name> --tenant <id>   Grant a tenant access to a runtime
  admin runtimes list-access --runtime <name>   List tenants with access to a runtime
  admin runtimes revoke-access --runtime <name> --tenant <id>  Revoke a tenant's access to a runtime
  admin pools list                      List pool configurations
  admin pools get <name>                Get a pool configuration
  admin pools create --from-file <f>    Create a pool from a JSON file
  admin pools update <name> --from-file <f>  Replace a pool's configuration
  admin pools delete <name>             Delete a pool
  admin pools drain <name>              Drain a pool (no new assignments)
  admin pools sync-status <name>        Show Postgres↔CRD reconciliation state
  admin pools resume-reconciliation <name>  Clear PoolScalingAdmissionStuck state
  admin pools set-warm-count --pool <name> --min <N> [--dry-run]
  admin pools exit-bootstrap --pool <name>   Drop the bootstrap minWarm override
  admin pools circuit-breaker --pool <name> --state <enabled|disabled|auto>   Override SDK-warm circuit-breaker
  admin pools grant-access --pool <name> --tenant <id>   Grant a tenant access to a pool
  admin pools list-access --pool <name>   List tenants with access to a pool
  admin pools revoke-access --pool <name> --tenant <id>  Revoke a tenant's access to a pool
                                        Override a pool's minWarm for emergency scaling (§24.4)
  admin credential-pools list [--tenant <id>]   List credential pools and their status (§24.5)
  admin credential-pools get --pool <name> [--tenant <id>]   Show a pool with per-credential health and lease counts (§24.5)
  admin credential-pools add-credential --pool <name> --id <credId> [--secret-ref <r>] [--role-arn <a>] [--region <r>]   Add a credential (§24.5)
  admin credential-pools update-credential --pool <name> --credential <id> [--secret-ref <r>] [--role-arn <a>] [--region <r>]   Update a credential (§24.5)
  admin credential-pools remove-credential --pool <name> --credential <id>   Remove a credential (§24.5)
  admin credential-pools revoke-credential --pool <name> --credential <id> [--reason <r>]   Emergency-revoke one credential (§24.5, §4.9)
  admin credential-pools revoke-pool --pool <name> [--reason <r>]   Emergency-revoke all credentials in a pool (§24.5, §4.9)
  admin credential-pools re-enable --pool <name> --credential <id> [--reason <r>]   Re-enable a revoked credential (§24.5, §4.9)
  admin sessions get <id>               Investigate a session's state, metadata, and assigned pod (§24.11)
  admin sessions force-terminate <id> [--reason <text>]
                                        Force a stuck session to failed and release its pod (§24.11)
  admin users rotate-token --user <name> [--namespace <ns>]
                                        Rotate the admin token (RFC 8693 token-exchange) and patch the lenny-admin-token Secret (§24.9)
  admin quota reconcile (--all-tenants | --tenant <id>)
                                        Re-aggregate quota counters from Postgres into Redis after a Redis recovery (§24.6)
  admin circuit-breakers list           List circuit breakers
  admin circuit-breakers open <name> --limit-tier <t> --scope <k>=<v> --reason <text>
  admin circuit-breakers close <name>   Close a circuit breaker
  admin external-adapters validate --name <name>
                                        Run the conformance suite against a registered external adapter (§24.8); exits non-zero on validation_failed
  admin external-adapters list          List registered external protocol adapters
  admin external-adapters get <name>    Get a specific external adapter
  admin external-adapters register --name <name> --binary-path <path> --level <basic|standard|full> [--protocol <p>] [--path-prefix <p>] [--display-name <s>]
  admin external-adapters update --name <name> [--binary-path <path>] [--level <l>] [--protocol <p>] [--path-prefix <p>] [--display-name <s>]
  admin external-adapters delete <name> Delete an external adapter
  migrate status                        Show the expand-contract phase of every active schema migration (§24.13)
  migrate down --version <N> --confirm [--reason <text>]
                                        Roll back the most recently applied migration at version N (§24.13)
  policy audit-isolation                Report DelegationPolicy rule × pool isolation-monotonicity violations (§24.14)
  slo export [--format openslo] [--tier <tier1|tier2|tier3>]
                                        Print the §16.5 SLOs as OpenSLO v1 documents (offline, §16.10)
  operations list [--status <s>] [--kind <k>] [--actor <a>] [--tenant <id>] [--operation-id <id>] [--limit <n>]
                                        List active long-running operations with Progress Envelopes (§24.15)
  operations get <id>                   Fetch a single operation by id (§24.15)
  me [tools|operations]                 Caller identity, authorized tools, or in-flight operations (§24.15)
  audit query --since <t> [--event-type <e>] [--actor <id>] [--operation-id <id>] [--limit <n>]
                                        Query the audit log with scatter-gather (§24.15, §25.9)
  audit get <id>                        Fetch a single audit event (§24.15)
  audit summary --since <t> [--group-by <eventType|actorId|resourceType>]   Aggregate counts (§24.15)
  audit retranslate <id> [--translator-version <semver>]   Retry OCSF translation on one row (§24.15)
  audit republish <id>                  Re-queue one terminally-failed EventBus publish (§24.15)
  audit drop-partition <p> --force --acknowledge-data-loss   Force-drop a blocked audit partition (§24.15)
  events list [--since <t>] [--type <e>] [--limit <n>]   List operational events (polling, §24.15)
  events tail                           Stream operational events over SSE (§24.15)
  events buffer                         Read the gateway in-memory event buffer (§24.15)
  events subscriptions list|get <id>|create --url <u> --types <csv>|delete <id>   Manage webhook subscriptions (§24.15)
  upgrade check|status                  Inspect platform-upgrade availability and state (§24.15)
  upgrade start --version <v> [--confirm]|proceed|pause|rollback [--confirm]|verify   Drive the upgrade state machine (§24.15)
  upgrade --answers <file> [--dry-run]  Replay an answer file against an existing install (§24.20)

Operability commands (§25.14 / §24.15, target lenny-ops):
  runbooks list [--alert <name>]        List runbooks (optionally by alert)
  runbooks get <name>                   Print a runbook
  locks list                            List remediation locks
  locks get <id>                        Inspect a single remediation lock
  locks acquire --scope <s> --op <op>   Acquire a remediation lock
  locks release <id>                    Release a remediation lock
  locks steal <id> --confirm --reason <text> [--ttl <secs>]
                                        Take over a lock held by another caller (audited)
  escalations list                      List escalations
  escalations create --severity <s> --summary <text>
  escalations resolve <id>              Mark an escalation resolved
  diagnose session <id>                 Diagnose a session
  diagnose pool <name>                  Diagnose a pool
  diagnose connectivity                 Check dependency connectivity
  diagnose credential-pool <name>       Diagnose a credential pool
  doctor [--fix] [--findings <a,b,c>]   Run diagnostics; --fix applies safe auto-remediations
  drift report [--scope <s>] [--against <live|target|both>]
  drift validate --desired <file>       Validate desired state
  drift snapshot refresh --desired <file> [--confirm]   Replace the stored desired-state snapshot
  drift reconcile [--scope <s>] [--confirm]
  backup list                           List backups
  backup get <id>                       Show backup details
  backup create --type <full|postgres|config> [--confirm]   Trigger a backup
  backup verify <id> [--mode test-restore]   Verify backup integrity
  backup schedule get|set [--from-file <f>]  Get or replace the backup schedule
  backup policy get|set [--from-file <f>]    Get or replace the retention policy
  restore safety-check --backup <id>    Estimate data loss before a restore
  restore preview --backup <id>         Preview a restore
  restore execute --backup <id> --confirm --acknowledge-data-loss   Execute a restore
  restore status <id>                   Per-shard restore status
  restore resume <id>                   Resume a partially-completed restore
  restore confirm-legal-hold-ledger <id> --justification <text>
  logs pods <namespace> <name> [--container <c>] [--since <d>] [--tail <n>] [--previous]
                                        Proxy a pod's container logs from lenny-ops (§24.15, §25.4)
  mcp-management tools                  List the management MCP tools (§24.15)
  mcp-management call <tool> [--params <json>]   Invoke a management MCP tool (§24.15)

Embedded Mode local commands (§24.19; identical to the lenny short name):
  up [--http-port <n>] [--https-port <n>]   Start the local stack (idempotent)
  down [--purge]                        Stop the local stack
  status [--json]                       Print local component health
  logs [<component>] [--follow]         Tail merged local-stack logs
  restart <component>                   Restart one local component (gateway|controller)
  token print [--ttl <d>]               Print a bearer for the built-in user
  image <import|list|rm>                Manage the embedded containerd image store
  session new --runtime <name>          Start a session against the local gateway`

type globalFlags struct {
	apiURL    string
	opsServer string
	bearer    string
	devTenant string
	devRoles  string

	// §24.16 line 205 cross-command flags.
	timeout  time.Duration // --timeout <secs>; default 30s.
	insecure bool          // --insecure-skip-verify (dev only).
	output   string        // --output <format>; only "json" is supported.
	quiet    bool          // --quiet suppresses informational messages.
}

// envOr returns the trimmed value of environment variable key, or def
// when the variable is unset or empty. An explicitly-empty variable is
// treated as unset so a stray `export LENNY_API_URL=` does not blank the
// default gateway URL.
func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// parseGlobalFlags pulls the leading --flag value pairs off args and
// returns the remainder as the command + its arguments. Defaults come
// from the environment when the corresponding flag is absent: LENNY_API_URL
// for --api-url, LENNY_OPS_URL for --ops-server, and LENNY_API_TOKEN for
// --token/--bearer. An explicit flag always overrides the environment.
// spec: §24.0 line 26, §24.16 lines 197 and 199, §24 preamble line 8.
func parseGlobalFlags(args []string) (globalFlags, []string) {
	f := globalFlags{
		apiURL:    envOr("LENNY_API_URL", "http://localhost:8080"),
		opsServer: envOr("LENNY_OPS_URL", ""),
		bearer:    envOr("LENNY_API_TOKEN", ""),
		timeout:   30 * time.Second,
		output:    "json",
	}
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--timeout":
			// §24.16 line 205: per-request timeout in seconds.
			if i+1 < len(args) {
				if secs, err := strconv.Atoi(args[i+1]); err == nil && secs > 0 {
					f.timeout = time.Duration(secs) * time.Second
				}
				i += 2
				continue
			}
		case "--insecure-skip-verify":
			// §24.16 line 205: dev-only TLS-verification bypass. Valueless.
			f.insecure = true
			i++
			continue
		case "--output":
			// §24.16 line 205: machine-readable output selector.
			if i+1 < len(args) {
				f.output = args[i+1]
				i += 2
				continue
			}
		case "--quiet":
			// §24.16 line 205: suppress informational messages. Valueless.
			f.quiet = true
			i++
			continue
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
		case "--bearer", "--token":
			// --token is the spec-facing name (§24 preamble line 8);
			// --bearer is kept as an equivalent alias.
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

// newClient builds the gateway-targeting ctl.Client from the parsed
// global flags. It is the single construction site so the §24.16 auth and
// transport options stay consistent across every command path.
func newClient(flags globalFlags) *ctl.Client {
	return ctl.New(ctl.Options{
		BaseURL:            flags.apiURL,
		Bearer:             flags.bearer,
		DevTenant:          flags.devTenant,
		DevRoles:           flags.devRoles,
		Timeout:            flags.timeout,
		InsecureSkipVerify: flags.insecure,
	})
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

// cmdVersion prints the local CLI build version. Operators and the §17.6
// release CI use it for offline introspection (`lenny-ctl version` and
// `kubectl lenny --version`), so it makes no network call. The gateway's
// own platform version is a separate concern surfaced by the ops
// auto-discovery path (§25.14), not by this command.
// spec: §24.0 line 23, §17.6 line 360.
func cmdVersion(stdout io.Writer) int {
	fmt.Fprintf(stdout, "%s %s\n", progName(), version)
	return 0
}

// cmdAdmin dispatches the §24 `admin` resource-management group. The quiet
// flag carries the parsed --quiet so the mutating tenants and
// external-adapters groups can suppress their informational advisory lines
// (§24.16 line 205, F-CTL-4).
func cmdAdmin(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer, quiet bool) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: admin requires a resource (tenants|runtimes|pools|credential-pools|sessions|users|quota|circuit-breakers|ca-rotation|erasure-jobs|external-adapters)")
		return 2
	}
	switch args[0] {
	case "tenants":
		return cmdTenants(ctx, c, args[1:], stdout, stderr, quiet)
	case "users":
		return cmdUsers(ctx, c, args[1:], stdout, stderr)
	case "runtimes":
		return cmdRuntimes(ctx, c, args[1:], stdout, stderr)
	case "pools":
		return cmdPools(ctx, c, args[1:], stdout, stderr)
	case "credential-pools":
		return cmdCredentialPools(ctx, c, args[1:], stdout, stderr)
	case "sessions":
		return cmdSessions(ctx, c, args[1:], stdout, stderr)
	case "quota":
		return cmdQuota(ctx, c, args[1:], stdout, stderr)
	case "circuit-breakers":
		return cmdCircuitBreakers(ctx, c, args[1:], stdout, stderr)
	case "ca-rotation":
		return cmdCARotation(ctx, c, args[1:], stdout, stderr)
	case "erasure-jobs":
		return cmdErasureJobs(ctx, c, args[1:], stdout, stderr)
	case "external-adapters":
		return cmdExternalAdapters(ctx, c, args[1:], stdout, stderr, quiet)
	case "deployment":
		return cmdDeployment(ctx, c, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown admin resource %q\n", args[0])
		return 2
	}
}

// cmdDeployment implements the §16.7 deployment-transition audit
// reconciliation group. `sync-config` posts the rendered Helm
// deployment-scope configuration (from a chart-rendered JSON request file)
// to POST /v1/admin/deployment/config-change so the gateway emits the
// gateway.*/platform.*/deployment.* transition audit events under the
// operator's identity. It is invoked by the chart's post-upgrade hook.
// spec: §16.7 lines 672, 676, 677, 682. F-8.2.5, F-9.2.10, F-17.2.8.
func cmdDeployment(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: deployment requires a subcommand (sync-config)")
		return 2
	}
	switch args[0] {
	case "sync-config":
		var fromFile string
		// The post-upgrade hook runs from a distroless image with no shell,
		// so --wait-timeout polls the gateway health endpoint before posting,
		// mirroring the bootstrap readiness poll. spec: §17.6 line 421.
		waitSeconds := 120
		rest := args[1:]
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--from-file" && i+1 < len(rest):
				fromFile = rest[i+1]
				i++
			case rest[i] == "--wait-timeout" && i+1 < len(rest):
				if n, err := strconv.Atoi(rest[i+1]); err == nil {
					waitSeconds = n
				}
				i++
			}
		}
		if fromFile == "" {
			fmt.Fprintln(stderr, "lenny-ctl: deployment sync-config requires --from-file <file>")
			return 2
		}
		if waitSeconds > 0 {
			deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
			for {
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
		raw, err := os.ReadFile(fromFile)
		if err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: read %s: %v\n", fromFile, err)
			return 1
		}
		var body any
		if err := yaml.Unmarshal(raw, &body); err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: %s is not valid YAML or JSON: %v\n", fromFile, err)
			return 1
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/deployment/config-change", body, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
		return 0
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown deployment subcommand %q\n", args[0])
		return 2
	}
}

// cmdMigrate implements the §24.13 migration-management group:
// `migrate status` (GET /v1/admin/schema/migrations/status) and
// `migrate down --version <N> --confirm` (POST
// /v1/admin/schema/migrations/{version}/down). spec: §24.13 lines 150-151.
func cmdMigrate(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: migrate requires a subcommand (status|down)")
		return 2
	}
	switch args[0] {
	case "status":
		var out map[string]any
		if err := c.Do(ctx, "GET", "/v1/admin/schema/migrations/status", nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "down":
		fs := flag.NewFlagSet("migrate down", flag.ContinueOnError)
		fs.SetOutput(stderr)
		version := fs.String("version", "", "migration version to roll back (required)")
		confirm := fs.Bool("confirm", false, "confirm the destructive rollback (required)")
		reason := fs.String("reason", "", "free-text rollback reason recorded in the audit trail")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *version == "" {
			fmt.Fprintln(stderr, "lenny-ctl: migrate down requires --version <N>")
			return 2
		}
		if !*confirm {
			fmt.Fprintln(stderr, "lenny-ctl: migrate down requires --confirm (the operation is destructive)")
			return 2
		}
		body := map[string]any{"confirm": true}
		if *reason != "" {
			body["reason"] = *reason
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/schema/migrations/"+*version+"/down", body, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown migrate subcommand %q\n", args[0])
		return 2
	}
	return 0
}

// cmdErasureJobs implements the §24.12 erasure-job management group:
// `erasure-jobs get <job-id>` (GET /v1/admin/erasure-jobs/{jobId}),
// `erasure-jobs retry <job-id>` (POST .../retry), and
// `erasure-jobs clear-restriction <job-id> --justification <text>`
// (POST .../clear-processing-restriction). All require platform-admin.
// spec: §24.12 lines 140-144.
func cmdErasureJobs(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: erasure-jobs requires a subcommand (get|retry|clear-restriction)")
		return 2
	}
	switch args[0] {
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: erasure-jobs get requires <job-id>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "GET", "/v1/admin/erasure-jobs/"+args[1], nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "retry":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: erasure-jobs retry requires <job-id>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/erasure-jobs/"+args[1]+"/retry", nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "clear-restriction":
		fs := flag.NewFlagSet("erasure-jobs clear-restriction", flag.ContinueOnError)
		fs.SetOutput(stderr)
		justification := fs.String("justification", "", "operator justification recorded in the audit trail (required)")
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: erasure-jobs clear-restriction requires <job-id>")
			return 2
		}
		jobID := args[1]
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		if *justification == "" {
			fmt.Fprintln(stderr, "lenny-ctl: erasure-jobs clear-restriction requires --justification <text>")
			return 2
		}
		body := map[string]any{"justification": *justification}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/erasure-jobs/"+jobID+"/clear-processing-restriction", body, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown erasure-jobs subcommand %q\n", args[0])
		return 2
	}
	return 0
}

// cmdSessions implements the §24.11 session-investigation group:
// `sessions get <id>` (GET /v1/admin/sessions/{id}) and
// `sessions force-terminate <id> [--reason <text>]` (POST
// .../force-terminate). Both require platform-admin.
// spec: §24.11 lines 135-136.
func cmdSessions(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: sessions requires a subcommand (get|force-terminate)")
		return 2
	}
	switch args[0] {
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: sessions get requires <id>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "GET", "/v1/admin/sessions/"+args[1], nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "force-terminate":
		fs := flag.NewFlagSet("sessions force-terminate", flag.ContinueOnError)
		fs.SetOutput(stderr)
		reason := fs.String("reason", "", "optional justification recorded in the audit trail")
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: sessions force-terminate requires <id>")
			return 2
		}
		id := args[1]
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		var body any
		if *reason != "" {
			body = map[string]any{"reason": *reason}
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/sessions/"+id+"/force-terminate", body, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown sessions subcommand %q\n", args[0])
		return 2
	}
	return 0
}

// cmdOperations implements the §24.15 operations group:
// `operations list [--status|--kind|--actor|--tenant|--operation-id|--limit]`
// (GET /v1/admin/operations, the §25.4 Inventory scatter-gather with a
// Progress Envelope per record) and `operations get <id>` (GET
// /v1/admin/operations/{id}). The operations inventory is hosted by
// lenny-ops (§15.1 line 909), so both subcommands resolve the ops
// endpoint via the §24.16 --ops-server auto-discovery (rules 1-3) and
// target lenny-ops rather than the gateway. spec: §24.15 line 181;
// §15.1 line 909 (operations inventory assigned to lenny-ops).
func cmdOperations(ctx context.Context, flags globalFlags, gateway *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: operations requires a subcommand (list|get)")
		return 2
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("operations list", flag.ContinueOnError)
		fs.SetOutput(stderr)
		status := fs.String("status", "", "filter by status (comma-separated)")
		kind := fs.String("kind", "", "filter by operation kind (comma-separated)")
		actor := fs.String("actor", "", "filter by actor subject")
		tenant := fs.String("tenant", "", "filter by tenant id")
		operationID := fs.String("operation-id", "", "filter by correlation operation id")
		limit := fs.Int("limit", 0, "maximum number of operations to return")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		q := url.Values{}
		for name, v := range map[string]string{
			"status": *status, "kind": *kind, "actor": *actor,
			"tenantId": *tenant, "operationId": *operationID,
		} {
			if v != "" {
				q.Set(name, v)
			}
		}
		if *limit > 0 {
			q.Set("limit", strconv.Itoa(*limit))
		}
		path := "/v1/admin/operations"
		if enc := q.Encode(); enc != "" {
			path += "?" + enc
		}
		return withOps(ctx, flags, gateway, stderr, func(ops *ctl.Client) int {
			return opsGet(ctx, ops, path, stdout, stderr)
		})
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: operations get requires <id>")
			return 2
		}
		path := "/v1/admin/operations/" + url.PathEscape(args[1])
		return withOps(ctx, flags, gateway, stderr, func(ops *ctl.Client) int {
			return opsGet(ctx, ops, path, stdout, stderr)
		})
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown operations subcommand %q\n", args[0])
		return 2
	}
}

func cmdTenants(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer, quiet bool) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: tenants requires a subcommand (list|get|create|delete|force-delete|rotate-erasure-salt)")
		return 2
	}
	switch args[0] {
	case "list":
		return printItemList(ctx, c, "/v1/admin/tenants", stdout, stderr)
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
	case "delete":
		// spec: §24.10 line 128 — DELETE /v1/admin/tenants/{id} initiates
		// the §12.8 deletion lifecycle (active → disabling → deleting →
		// deleted). The multi-phase controller runs asynchronously; the
		// operator monitors progress with `tenants get <id>`, which surfaces
		// the §12.8 TenantState on the `state` field.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: tenants delete requires <id>")
			return 2
		}
		if err := c.Do(ctx, "DELETE", "/v1/admin/tenants/"+url.PathEscape(args[1]), nil, nil); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		// spec: §24.16 line 205 — --output json keeps stdout to the result
		// document, and --quiet suppresses informational messages. This
		// advisory is informational, so it goes to stderr and is gated on
		// --quiet, leaving the DELETE body-less stdout empty for a strict
		// `| jq` pipeline (F-CTL-4).
		if !quiet {
			fmt.Fprintf(stderr, "tenant %q deletion initiated; monitor with: lenny-ctl admin tenants get %s\n", args[1], args[1])
		}
	case "force-delete":
		// spec: §24.10 row 4 — POST /v1/admin/tenants/{id}/force-delete
		// proceeds with the §12.8 deletion lifecycle despite active legal
		// holds. --acknowledge-hold-override authorizes the Phase 3.5 escrow
		// segregation (held evidence is re-encrypted into the region-scoped
		// escrow); --justification <text> is required with the override.
		// Without --acknowledge-hold-override a held tenant is rejected with
		// TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD.
		return cmdTenantsForceDelete(ctx, c, args[1:], stdout, stderr, quiet)
	case "rotate-erasure-salt":
		// spec: §12.8 line 857 — POST /v1/admin/tenants/{id}/rotate-erasure-salt
		// rotates a compromised per-tenant billing-pseudonymization salt: the
		// old salt is deleted immediately and a security audit event fires.
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: tenants rotate-erasure-salt requires <id>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/tenants/"+url.PathEscape(args[1])+"/rotate-erasure-salt", nil, &out); err != nil {
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

// cmdTenantsForceDelete implements `admin tenants force-delete <id>
// [--acknowledge-hold-override --justification <text>]` (§24.10 row 4).
func cmdTenantsForceDelete(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer, quiet bool) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: tenants force-delete requires <id>")
		return 2
	}
	id := args[0]
	var (
		ack           bool
		justification string
	)
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--acknowledge-hold-override":
			ack = true
		case "--justification":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "lenny-ctl: --justification requires a value")
				return 2
			}
			justification, i = args[i+1], i+1
		default:
			fmt.Fprintf(stderr, "lenny-ctl: unknown flag %q for tenants force-delete\n", args[i])
			return 2
		}
	}
	if ack && justification == "" {
		fmt.Fprintln(stderr, "lenny-ctl: --acknowledge-hold-override requires --justification <text>")
		return 2
	}
	body := map[string]any{}
	if ack {
		body["acknowledgeHoldOverride"] = true
		body["justification"] = justification
	}
	var out map[string]any
	if err := c.Do(ctx, "POST", "/v1/admin/tenants/"+url.PathEscape(id)+"/force-delete", body, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	// spec: §24.16 line 205 — keep stdout to the JSON result document under
	// --output json, and suppress this informational advisory under --quiet,
	// so a strict `| jq` pipeline parses stdout as one document (F-CTL-4).
	if !quiet {
		fmt.Fprintf(stderr, "tenant %q force-delete initiated; monitor with: lenny-ctl admin tenants get %s\n", id, id)
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
		return printItemList(ctx, c, "/v1/admin/runtimes", stdout, stderr)
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
	case "grant-access", "list-access", "revoke-access":
		// spec: §24.3 — runtime tenant-access management. grant-access
		// and revoke-access take --runtime + --tenant; list-access takes
		// --runtime only. They map to the §15.1:778-780 tenant-access
		// endpoints under /v1/admin/runtimes/{name}/tenant-access.
		return cmdRuntimesAccess(ctx, c, args[0], args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown runtimes subcommand %q\n", args[0])
		return 2
	}
	return 0
}

// cmdRuntimesAccess implements the three §24.3 runtime tenant-access
// subcommands: grant-access, list-access, and revoke-access. The verb
// is passed in so the three share one flag parser. spec: §24.3 table.
func cmdRuntimesAccess(ctx context.Context, c *ctl.Client, verb string, args []string, stdout, stderr io.Writer) int {
	var runtime, tenant string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--runtime":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "lenny-ctl: --runtime requires a value")
				return 2
			}
			runtime, i = args[i+1], i+1
		case "--tenant":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "lenny-ctl: --tenant requires a value")
				return 2
			}
			tenant, i = args[i+1], i+1
		default:
			fmt.Fprintf(stderr, "lenny-ctl: unknown flag %q for runtimes %s\n", args[i], verb)
			return 2
		}
	}
	if runtime == "" {
		fmt.Fprintf(stderr, "lenny-ctl: runtimes %s requires --runtime <name>\n", verb)
		return 2
	}
	base := "/v1/admin/runtimes/" + url.PathEscape(runtime) + "/tenant-access"
	return tenantAccessVerb(ctx, c, "runtimes", base, verb, tenant, stdout, stderr)
}

// tenantAccessVerb performs the §15.1 tenant-access grant/list/revoke call
// against a resource's /tenant-access subresource. base is the full
// .../tenant-access path; resource names the parent group for error
// messages (for example "runtimes" or "pools"); verb is one of
// grant-access, list-access, or revoke-access. spec: §15.1 lines 778-780
// (runtimes), 802-804 (pools).
func tenantAccessVerb(ctx context.Context, c *ctl.Client, resource, base, verb, tenant string, stdout, stderr io.Writer) int {
	switch verb {
	case "grant-access":
		if tenant == "" {
			fmt.Fprintf(stderr, "lenny-ctl: %s grant-access requires --tenant <id>\n", resource)
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", base, map[string]string{"tenantId": tenant}, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "list-access":
		var out map[string]any
		if err := c.Do(ctx, "GET", base, nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "revoke-access":
		if tenant == "" {
			fmt.Fprintf(stderr, "lenny-ctl: %s revoke-access requires --tenant <id>\n", resource)
			return 2
		}
		if err := c.Do(ctx, "DELETE", base+"/"+url.PathEscape(tenant), nil, nil); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
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
		fmt.Fprintln(stderr, "lenny-ctl: pools requires a subcommand (list|get|create|update|delete|drain|sync-status|resume-reconciliation|set-warm-count|exit-bootstrap|circuit-breaker|grant-access|list-access|revoke-access|upgrade)")
		return 2
	}
	switch args[0] {
	case "upgrade":
		// spec: §10.5 lines 466-540 — the RuntimeUpgrade state machine is
		// managed by `lenny-ctl admin pools upgrade`.
		return cmdPoolsUpgrade(ctx, c, args[1:], stdout, stderr)
	case "list":
		return printItemList(ctx, c, "/v1/admin/pools", stdout, stderr)
	case "get":
		return cmdPoolsGet(ctx, c, args[1:], stdout, stderr)
	case "create":
		return cmdPoolsCreate(ctx, c, args[1:], stdout, stderr)
	case "update":
		return cmdPoolsUpdate(ctx, c, args[1:], stdout, stderr)
	case "delete":
		return cmdPoolsDelete(ctx, c, args[1:], stdout, stderr)
	case "drain":
		return cmdPoolsDrain(ctx, c, args[1:], stdout, stderr)
	case "sync-status":
		return cmdPoolsSyncStatus(ctx, c, args[1:], stdout, stderr)
	case "resume-reconciliation":
		return cmdPoolsResumeReconciliation(ctx, c, args[1:], stdout, stderr)
	case "set-warm-count":
		return cmdPoolsSetWarmCount(ctx, c, args[1:], stdout, stderr)
	case "exit-bootstrap":
		return cmdPoolsExitBootstrap(ctx, c, args[1:], stdout, stderr)
	case "circuit-breaker":
		return cmdPoolsCircuitBreaker(ctx, c, args[1:], stdout, stderr)
	case "grant-access", "list-access", "revoke-access":
		// spec: §24.4 lines 76-78 — pool tenant-access management.
		// grant-access and revoke-access take --pool + --tenant;
		// list-access takes --pool only. They map to the §15.1:802-804
		// endpoints under /v1/admin/pools/{name}/tenant-access.
		return cmdPoolsAccess(ctx, c, args[0], args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown pools subcommand %q\n", args[0])
		return 2
	}
}

// cmdPoolsGet implements `lenny-ctl admin pools get <name>`. spec: §15.1
// line 793 — GET /v1/admin/pools/{name}. args excludes the "get" verb.
func cmdPoolsGet(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "lenny-ctl: pools get requires <name>")
		return 2
	}
	var out map[string]any
	if err := c.Do(ctx, "GET", "/v1/admin/pools/"+url.PathEscape(args[0]), nil, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

// cmdPoolsCreate implements `lenny-ctl admin pools create --from-file
// <pool.json>`. args excludes the "create" verb. spec: §15.1 line 792 —
// POST /v1/admin/pools accepts the pool definition as JSON. --from-file
// reads the body from a local file so an operator can keep pool
// definitions under source control.
func cmdPoolsCreate(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	path := flagValue(args, "--from-file")
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
	return 0
}

// cmdPoolsUpdate implements `lenny-ctl admin pools update <name>
// --from-file <pool.json>`. args excludes the "update" verb. spec: §15.1
// line 795 — PUT /v1/admin/pools/{name} requires the updated pool
// definition in the body.
func cmdPoolsUpdate(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "lenny-ctl: pools update requires <name> --from-file <pool.json>")
		return 2
	}
	path := flagValue(args[1:], "--from-file")
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
	// spec: §15.1 lines 1207-1213 — the pool PUT enforces the If-Match
	// precondition; fetch the current ETag and forward it.
	poolPath := "/v1/admin/pools/" + url.PathEscape(args[0])
	if err := c.PutIfMatch(ctx, poolPath, poolPath, body, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

// cmdPoolsDelete implements `lenny-ctl admin pools delete <name>`. args
// excludes the "delete" verb. spec: §15.1 line 796 — DELETE
// /v1/admin/pools/{name}.
func cmdPoolsDelete(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "lenny-ctl: pools delete requires <name>")
		return 2
	}
	var out map[string]any
	if err := c.Do(ctx, "DELETE", "/v1/admin/pools/"+url.PathEscape(args[0]), nil, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

// cmdPoolsDrain implements `lenny-ctl admin pools drain <name>`. args
// excludes the "drain" verb. spec: §15.1 line 797 — POST
// /v1/admin/pools/{name}/drain.
func cmdPoolsDrain(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "lenny-ctl: pools drain requires <name>")
		return 2
	}
	var out map[string]any
	if err := c.Do(ctx, "POST", "/v1/admin/pools/"+url.PathEscape(args[0])+"/drain", nil, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

// cmdPoolsSyncStatus implements `lenny-ctl admin pools sync-status
// <name>`. args excludes the "sync-status" verb. spec: §15.1 line 798 —
// GET /v1/admin/pools/{name}/sync-status.
func cmdPoolsSyncStatus(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "lenny-ctl: pools sync-status requires <name>")
		return 2
	}
	var out map[string]any
	if err := c.Do(ctx, "GET", "/v1/admin/pools/"+url.PathEscape(args[0])+"/sync-status", nil, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

// cmdPoolsResumeReconciliation implements `lenny-ctl admin pools
// resume-reconciliation <name>`. args excludes the
// "resume-reconciliation" verb. spec: §15.1 line 799 — POST
// /v1/admin/pools/{name}/resume-reconciliation.
func cmdPoolsResumeReconciliation(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "lenny-ctl: pools resume-reconciliation requires <name>")
		return 2
	}
	var out map[string]any
	if err := c.Do(ctx, "POST", "/v1/admin/pools/"+url.PathEscape(args[0])+"/resume-reconciliation", nil, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

// cmdPoolsSetWarmCount implements `lenny-ctl admin pools set-warm-count
// --pool <name> --min <N>`. args excludes the "set-warm-count" verb.
// spec: §24.4 line 63 — it maps to PUT /v1/admin/pools/{name}/warm-count
// with the operability `{minWarm, confirm}` body (§25.17 lines
// 5232-5239). The warm-pool-exhaustion / sdk-connect-timeout /
// pool-bootstrap-mode / coordinator-handoff-slow runbooks invoke this for
// emergency scaling. The operator's explicit invocation confirms the
// mutation; --dry-run returns the §25.2 preview instead.
func cmdPoolsSetWarmCount(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pools set-warm-count", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pool := fs.String("pool", "", "pool name (required)")
	min := fs.Int("min", -1, "new minWarm count (required, >= 0)")
	dryRun := fs.Bool("dry-run", false, "return the scaling preview without applying it")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *pool == "" {
		fmt.Fprintln(stderr, "lenny-ctl: pools set-warm-count requires --pool <name>")
		return 2
	}
	if *min < 0 {
		fmt.Fprintln(stderr, "lenny-ctl: pools set-warm-count requires --min <N> (>= 0)")
		return 2
	}
	body := map[string]any{"minWarm": *min, "confirm": !*dryRun}
	var out map[string]any
	if err := c.Do(ctx, "PUT", "/v1/admin/pools/"+url.PathEscape(*pool)+"/warm-count", body, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

// cmdPoolsExitBootstrap implements `lenny-ctl admin pools exit-bootstrap
// --pool <name>`. args excludes the "exit-bootstrap" verb. spec: §24.4
// line 64 — it removes the bootstrap minWarm override and switches to
// formula-driven scaling immediately. Maps to DELETE
// /v1/admin/pools/{name}/bootstrap-override (§15.1 line 875).
func cmdPoolsExitBootstrap(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pools exit-bootstrap", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pool := fs.String("pool", "", "pool name (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *pool == "" {
		fmt.Fprintln(stderr, "lenny-ctl: pools exit-bootstrap requires --pool <name>")
		return 2
	}
	var out map[string]any
	if err := c.Do(ctx, "DELETE", "/v1/admin/pools/"+url.PathEscape(*pool)+"/bootstrap-override", nil, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

// cmdPoolsCircuitBreaker implements `lenny-ctl admin pools
// circuit-breaker --pool <name> --state <enabled|disabled|auto>`. args
// excludes the "circuit-breaker" verb. spec: §24.4 line 75 — it overrides
// the §6.1 SDK-warm circuit-breaker state. Maps to PUT
// /v1/admin/pools/{name}/circuit-breaker (§15.1 line 801), which requires
// If-Match; the ETag is read from the pool resource it shares
// pool_config_generation with.
func cmdPoolsCircuitBreaker(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pools circuit-breaker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pool := fs.String("pool", "", "pool name (required)")
	state := fs.String("state", "", "circuit-breaker override: enabled|disabled|auto (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *pool == "" {
		fmt.Fprintln(stderr, "lenny-ctl: pools circuit-breaker requires --pool <name>")
		return 2
	}
	switch *state {
	case "enabled", "disabled", "auto":
	default:
		fmt.Fprintln(stderr, "lenny-ctl: pools circuit-breaker requires --state <enabled|disabled|auto>")
		return 2
	}
	body := map[string]any{"sdkWarm": map[string]any{"circuitBreakerOverride": *state}}
	poolPath := "/v1/admin/pools/" + url.PathEscape(*pool)
	var out map[string]any
	if err := c.PutIfMatch(ctx, poolPath, poolPath+"/circuit-breaker", body, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

// cmdPoolsAccess implements the three §24.4 pool tenant-access
// subcommands: grant-access, list-access, and revoke-access. The verb is
// passed in so the three share one flag parser. spec: §24.4 lines 76-78.
func cmdPoolsAccess(ctx context.Context, c *ctl.Client, verb string, args []string, stdout, stderr io.Writer) int {
	var pool, tenant string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--pool":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "lenny-ctl: --pool requires a value")
				return 2
			}
			pool, i = args[i+1], i+1
		case "--tenant":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "lenny-ctl: --tenant requires a value")
				return 2
			}
			tenant, i = args[i+1], i+1
		default:
			fmt.Fprintf(stderr, "lenny-ctl: unknown flag %q for pools %s\n", args[i], verb)
			return 2
		}
	}
	if pool == "" {
		fmt.Fprintf(stderr, "lenny-ctl: pools %s requires --pool <name>\n", verb)
		return 2
	}
	base := "/v1/admin/pools/" + url.PathEscape(pool) + "/tenant-access"
	return tenantAccessVerb(ctx, c, "pools", base, verb, tenant, stdout, stderr)
}

// cmdPoolsUpgrade implements `lenny-ctl admin pools upgrade
// start|proceed|pause|resume|rollback|status`, the operator surface for
// the §10.5 RuntimeUpgrade state machine. Each subcommand is keyed by
// --pool and maps to the /v1/admin/pools/{name}/upgrade* endpoints.
// spec: §10.5 lines 466-540.
func cmdPoolsUpgrade(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: pools upgrade requires a subcommand (start|proceed|pause|resume|rollback|status)")
		return 2
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "start":
		fs := flag.NewFlagSet("pools upgrade start", flag.ContinueOnError)
		fs.SetOutput(stderr)
		pool := fs.String("pool", "", "pool name (required)")
		newImage := fs.String("new-image", "", "digest-pinned runtime image to roll out (required)")
		canary := fs.Int("canary-percent", 0, "percentage of new sessions routed to the new pool during Expanding")
		schemaVersion := fs.String("schema-version", "", "schema version gated on upgrade completion (§10.5 line 502)")
		drainFirst := fs.Bool("drain-first", false, "force Draining to complete before Contracting")
		autoAdvance := fs.Bool("auto-advance", false, "auto-proceed through phases without an operator proceed")
		stabWindow := fs.Int64("stabilization-window-seconds", 0, "dwell the new pool must hold before Expanding exits (default 120)")
		drainTimeout := fs.Int64("drain-timeout-seconds", 0, "bound on Draining before remaining sessions are force-terminated")
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if *pool == "" {
			fmt.Fprintln(stderr, "lenny-ctl: pools upgrade start requires --pool <name>")
			return 2
		}
		if *newImage == "" {
			fmt.Fprintln(stderr, "lenny-ctl: pools upgrade start requires --new-image <digest>")
			return 2
		}
		body := map[string]any{
			"newImage":      *newImage,
			"canaryPercent": *canary,
			"schemaVersion": *schemaVersion,
			"drainFirst":    *drainFirst,
			"autoAdvance":   *autoAdvance,
		}
		if *stabWindow > 0 {
			body["stabilizationWindowSeconds"] = *stabWindow
		}
		if *drainTimeout > 0 {
			body["drainTimeoutSeconds"] = *drainTimeout
		}
		return doUpgradeCall(ctx, c, "POST", "/v1/admin/pools/"+url.PathEscape(*pool)+"/upgrade/start", body, stdout, stderr)
	case "proceed", "resume":
		pool, code := requireUpgradePool(sub, rest, stderr)
		if code != 0 {
			return code
		}
		return doUpgradeCall(ctx, c, "POST", "/v1/admin/pools/"+url.PathEscape(pool)+"/upgrade/"+sub, nil, stdout, stderr)
	case "pause":
		fs := flag.NewFlagSet("pools upgrade pause", flag.ContinueOnError)
		fs.SetOutput(stderr)
		pool := fs.String("pool", "", "pool name (required)")
		reason := fs.String("reason", "", "free-text pause reason recorded on the upgrade record (§10.5 line 494)")
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if *pool == "" {
			fmt.Fprintln(stderr, "lenny-ctl: pools upgrade pause requires --pool <name>")
			return 2
		}
		var body map[string]any
		if *reason != "" {
			body = map[string]any{"reason": *reason}
		}
		return doUpgradeCall(ctx, c, "POST", "/v1/admin/pools/"+url.PathEscape(*pool)+"/upgrade/pause", body, stdout, stderr)
	case "rollback":
		fs := flag.NewFlagSet("pools upgrade rollback", flag.ContinueOnError)
		fs.SetOutput(stderr)
		pool := fs.String("pool", "", "pool name (required)")
		restore := fs.Bool("restore-old-pool", false, "recreate the old pool from previousPoolSpec (required from Draining/Contracting)")
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if *pool == "" {
			fmt.Fprintln(stderr, "lenny-ctl: pools upgrade rollback requires --pool <name>")
			return 2
		}
		body := map[string]any{"restoreOldPool": *restore}
		return doUpgradeCall(ctx, c, "POST", "/v1/admin/pools/"+url.PathEscape(*pool)+"/upgrade/rollback", body, stdout, stderr)
	case "status":
		pool, code := requireUpgradePool(sub, rest, stderr)
		if code != 0 {
			return code
		}
		return doUpgradeCall(ctx, c, "GET", "/v1/admin/pools/"+url.PathEscape(pool)+"/upgrade-status", nil, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown pools upgrade subcommand %q\n", sub)
		return 2
	}
}

// requireUpgradePool parses the --pool flag shared by the no-body upgrade
// subcommands (proceed, resume, status).
func requireUpgradePool(sub string, args []string, stderr io.Writer) (string, int) {
	fs := flag.NewFlagSet("pools upgrade "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	pool := fs.String("pool", "", "pool name (required)")
	if err := fs.Parse(args); err != nil {
		return "", 2
	}
	if *pool == "" {
		fmt.Fprintf(stderr, "lenny-ctl: pools upgrade %s requires --pool <name>\n", sub)
		return "", 2
	}
	return *pool, 0
}

// doUpgradeCall performs the HTTP call and prints the §10.5 upgrade-status
// JSON the gateway returns.
func doUpgradeCall(ctx context.Context, c *ctl.Client, method, path string, body any, stdout, stderr io.Writer) int {
	var out map[string]any
	if err := c.Do(ctx, method, path, body, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

// cmdQuota implements the §24.6 quota-operations group. `quota reconcile`
// re-aggregates in-flight session usage from the Postgres checkpoint into
// Redis after a Redis recovery, mapping to POST /v1/admin/quota/reconcile.
// The redis-failure runbook uses the platform-wide `--all-tenants` form;
// the redis-sentinel-failover runbook reloads one tenant via `--tenant
// <id>`. spec: §24.6 line 99; §15.1 line 879.
func cmdQuota(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: quota requires a subcommand (reconcile)")
		return 2
	}
	switch args[0] {
	case "reconcile":
		fs := flag.NewFlagSet("quota reconcile", flag.ContinueOnError)
		fs.SetOutput(stderr)
		allTenants := fs.Bool("all-tenants", false, "reconcile every tenant's quota counters")
		tenant := fs.String("tenant", "", "reconcile a single tenant's quota counters")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *allTenants == (*tenant != "") {
			// Either neither or both scopes were supplied.
			fmt.Fprintln(stderr, "lenny-ctl: quota reconcile requires exactly one of --all-tenants or --tenant <id>")
			return 2
		}
		body := map[string]any{}
		if *allTenants {
			body["allTenants"] = true
		} else {
			body["tenantId"] = *tenant
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/quota/reconcile", body, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown quota subcommand %q\n", args[0])
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

// cmdCARotation implements the §10.3 lines 344-350 CA-rotation group:
// `status` reads the current stage, `begin <newCaId>` introduces the new
// CA, `promote` swaps the issuer, and `retire` drops the old CA once the
// overlap window closes. Each transition is platform-admin-only and is
// audited as platform.ca_rotated on the gateway. F-10.3.21.
func cmdCARotation(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: ca-rotation requires a subcommand (status|begin|promote|retire)")
		return 2
	}
	switch args[0] {
	case "status":
		var out map[string]any
		if err := c.Do(ctx, "GET", "/v1/admin/ca-rotation", nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "begin":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: ca-rotation begin requires <newCaId>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/ca-rotation/begin", map[string]any{"newCaId": args[1]}, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "promote", "retire":
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/ca-rotation/"+args[0], nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown ca-rotation subcommand %q\n", args[0])
		return 2
	}
	return 0
}

// cmdExternalAdapters implements the §24.8 external-protocol adapter
// group: the normative `validate` command (§24.8 line 113) plus the
// §15.1 lines 850-855 CRUD subcommands. `validate` exits non-zero when
// the adapter fails the conformance suite (status validation_failed) so
// CI gates can branch on it.
func cmdExternalAdapters(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer, quiet bool) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: external-adapters requires a subcommand (validate|list|get|register|update|delete)")
		return 2
	}
	switch args[0] {
	case "validate":
		name := flagValue(args[1:], "--name")
		if name == "" && len(args) >= 2 && !strings.HasPrefix(args[1], "-") {
			name = args[1]
		}
		if name == "" {
			fmt.Fprintln(stderr, "lenny-ctl: external-adapters validate requires --name <name>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", "/v1/admin/external-adapters/"+url.PathEscape(name)+"/validate", nil, &out); err != nil {
			// On a failing run the gate returns 422 ADAPTER_VALIDATION_FAILED;
			// fetch the record so the operator sees the per-test report, then
			// exit non-zero.
			fmt.Fprintln(stderr, err)
			var rec map[string]any
			if gerr := c.Do(ctx, "GET", "/v1/admin/external-adapters/"+url.PathEscape(name), nil, &rec); gerr == nil {
				printJSON(stdout, rec)
			}
			return 1
		}
		printJSON(stdout, out)
	case "list":
		return printItemList(ctx, c, "/v1/admin/external-adapters", stdout, stderr)
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: external-adapters get requires <name>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "GET", "/v1/admin/external-adapters/"+url.PathEscape(args[1]), nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "register", "update":
		body, name, err := parseExternalAdapter(args[1:])
		if err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: %v\n", err)
			return 2
		}
		var out map[string]any
		if args[0] == "register" {
			if err := c.Do(ctx, "POST", "/v1/admin/external-adapters", body, &out); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		} else {
			if name == "" {
				fmt.Fprintln(stderr, "lenny-ctl: external-adapters update requires --name <name>")
				return 2
			}
			// spec: §15.1 lines 1207-1213 — the external-adapter PUT enforces
			// the If-Match precondition; fetch the current ETag and forward it.
			path := "/v1/admin/external-adapters/" + url.PathEscape(name)
			if err := c.PutIfMatch(ctx, path, path, body, &out); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
		printJSON(stdout, out)
	case "delete":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "lenny-ctl: external-adapters delete requires <name>")
			return 2
		}
		if err := c.Do(ctx, "DELETE", "/v1/admin/external-adapters/"+url.PathEscape(args[1]), nil, nil); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		// spec: §24.16 line 205 — informational advisory goes to stderr and
		// is suppressed under --quiet, leaving the body-less DELETE stdout
		// empty for a strict `| jq` pipeline (F-CTL-4).
		if !quiet {
			fmt.Fprintf(stderr, "external adapter %q deleted\n", args[1])
		}
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown external-adapters subcommand %q\n", args[0])
		return 2
	}
	return 0
}

// cmdCredentialPools implements the §24.5 credential-management group:
// the eight `lenny-ctl admin credential-pools …` subcommands, each
// mapping 1:1 to a §15.1 `/v1/admin/credential-pools` route (§24 preamble
// line 5). All commands accept `--tenant <id>`, sent as the `?tenantId=`
// query a platform-admin uses to target a tenant's pools.
//
// spec: §24.5 rows 1-8; §15.1 lines 805-812, 876-878.
func cmdCredentialPools(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: credential-pools requires a subcommand "+
			"(list|get|add-credential|update-credential|remove-credential|revoke-credential|revoke-pool|re-enable)")
		return 2
	}
	// tenantQuery renders the optional ?tenantId= suffix from --tenant.
	tenantQuery := func(rest []string) string {
		if v := flagValue(rest, "--tenant"); v != "" {
			return "?tenantId=" + url.QueryEscape(v)
		}
		return ""
	}
	base := "/v1/admin/credential-pools"
	switch args[0] {
	case "list":
		var out map[string]any
		if err := c.Do(ctx, "GET", base+tenantQuery(args[1:]), nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "get":
		pool := flagValue(args[1:], "--pool")
		if pool == "" {
			fmt.Fprintln(stderr, "lenny-ctl: credential-pools get requires --pool <name>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "GET", base+"/"+url.PathEscape(pool)+tenantQuery(args[1:]), nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "add-credential":
		pool := flagValue(args[1:], "--pool")
		if pool == "" {
			fmt.Fprintln(stderr, "lenny-ctl: credential-pools add-credential requires --pool <name>")
			return 2
		}
		id := flagValue(args[1:], "--id")
		if id == "" {
			fmt.Fprintln(stderr, "lenny-ctl: credential-pools add-credential requires --id <credential-id>")
			return 2
		}
		body := map[string]any{"id": id}
		if v := flagValue(args[1:], "--secret-ref"); v != "" {
			body["secretRef"] = v
		}
		if v := flagValue(args[1:], "--role-arn"); v != "" {
			body["roleArn"] = v
		}
		if v := flagValue(args[1:], "--region"); v != "" {
			body["region"] = v
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", base+"/"+url.PathEscape(pool)+"/credentials"+tenantQuery(args[1:]), body, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "update-credential":
		pool := flagValue(args[1:], "--pool")
		cred := flagValue(args[1:], "--credential")
		if pool == "" || cred == "" {
			fmt.Fprintln(stderr, "lenny-ctl: credential-pools update-credential requires --pool <name> --credential <id>")
			return 2
		}
		body := map[string]any{}
		if v := flagValue(args[1:], "--secret-ref"); v != "" {
			body["secretRef"] = v
		}
		if v := flagValue(args[1:], "--role-arn"); v != "" {
			body["roleArn"] = v
		}
		if v := flagValue(args[1:], "--region"); v != "" {
			body["region"] = v
		}
		var out map[string]any
		if err := c.Do(ctx, "PUT", base+"/"+url.PathEscape(pool)+"/credentials/"+url.PathEscape(cred)+tenantQuery(args[1:]), body, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "remove-credential":
		pool := flagValue(args[1:], "--pool")
		cred := flagValue(args[1:], "--credential")
		if pool == "" || cred == "" {
			fmt.Fprintln(stderr, "lenny-ctl: credential-pools remove-credential requires --pool <name> --credential <id>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "DELETE", base+"/"+url.PathEscape(pool)+"/credentials/"+url.PathEscape(cred)+tenantQuery(args[1:]), nil, &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "revoke-credential":
		pool := flagValue(args[1:], "--pool")
		cred := flagValue(args[1:], "--credential")
		if pool == "" || cred == "" {
			fmt.Fprintln(stderr, "lenny-ctl: credential-pools revoke-credential requires --pool <name> --credential <id>")
			return 2
		}
		var out map[string]any
		path := base + "/" + url.PathEscape(pool) + "/credentials/" + url.PathEscape(cred) + "/revoke" + tenantQuery(args[1:])
		if err := c.Do(ctx, "POST", path, revokeBody(args[1:]), &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "revoke-pool":
		pool := flagValue(args[1:], "--pool")
		if pool == "" {
			fmt.Fprintln(stderr, "lenny-ctl: credential-pools revoke-pool requires --pool <name>")
			return 2
		}
		var out map[string]any
		if err := c.Do(ctx, "POST", base+"/"+url.PathEscape(pool)+"/revoke"+tenantQuery(args[1:]), revokeBody(args[1:]), &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	case "re-enable":
		pool := flagValue(args[1:], "--pool")
		cred := flagValue(args[1:], "--credential")
		if pool == "" || cred == "" {
			fmt.Fprintln(stderr, "lenny-ctl: credential-pools re-enable requires --pool <name> --credential <id>")
			return 2
		}
		var out map[string]any
		path := base + "/" + url.PathEscape(pool) + "/credentials/" + url.PathEscape(cred) + "/re-enable" + tenantQuery(args[1:])
		if err := c.Do(ctx, "POST", path, revokeBody(args[1:]), &out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printJSON(stdout, out)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown credential-pools subcommand %q\n", args[0])
		return 2
	}
	return 0
}

// revokeBody renders the optional §4.9 {reason, note} body for the
// revoke / revoke-pool / re-enable commands. It returns nil when neither
// flag is present so the request carries an empty body (the spec marks
// both optional). spec: §24.5 rows 6-8; §4.9 lines 1632-1636.
func revokeBody(rest []string) map[string]any {
	body := map[string]any{}
	if v := flagValue(rest, "--reason"); v != "" {
		body["reason"] = v
	}
	if v := flagValue(rest, "--note"); v != "" {
		body["note"] = v
	}
	if len(body) == 0 {
		return nil
	}
	return body
}

// parseExternalAdapter builds the §15.1 external-adapter register/update
// request body from the --name, --binary-path, --level, --protocol,
// --path-prefix, and --display-name flags. Returns the resolved name so
// the update path can target the right record.
func parseExternalAdapter(args []string) (map[string]any, string, error) {
	body := map[string]any{}
	var name string
	for i := 0; i < len(args); i++ {
		key := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", key)
			}
			i++
			return args[i], nil
		}
		v, err := next()
		if err != nil {
			return nil, "", err
		}
		switch key {
		case "--name":
			name = v
			body["name"] = v
		case "--binary-path":
			body["binaryPath"] = v
		case "--level":
			body["level"] = v
		case "--protocol":
			body["protocol"] = v
		case "--path-prefix":
			body["pathPrefix"] = v
		case "--display-name":
			body["displayName"] = v
		default:
			return nil, "", fmt.Errorf("unknown flag %q", key)
		}
	}
	return body, name, nil
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

func cmdBootstrap(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer, quiet bool) int {
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
		// §24.16 line 205: --quiet suppresses informational progress
		// messages; the readiness-failure line below is an error, not
		// informational, so it always prints.
		if !quiet {
			fmt.Fprintf(stderr, "lenny-ctl: waiting up to %ds for the gateway\n", waitSeconds)
		}
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
	// spec: §17.6 line 473 — the first-use prompt. On a first run (the
	// gateway created the Secret) print where to retrieve the token; on a
	// re-run print the no-change notice. Printed to stderr so a script
	// parsing the JSON result on stdout is unaffected.
	out.printAdminTokenPrompt(stderr, quiet)
	// spec: §17.6 line 420 — exit 0 = all seeded, 1 = validation error,
	// 2 = partial failure.
	return out.exitCode()
}

// printAdminTokenPrompt prints the §17.6 line 473 first-use prompt. The
// "Retrieve with" command is always printed on a fresh Secret (a
// one-time onboarding pointer the operator must not miss); the "already
// exists" notice is suppressed under --quiet as routine re-run noise.
// F-24.1.7.
func (b bootstrapResult) printAdminTokenPrompt(stderr io.Writer, quiet bool) {
	if b.AdminToken == nil {
		return
	}
	ns := b.AdminToken.SecretNamespace
	name := b.AdminToken.SecretName
	if b.AdminToken.SecretCreated {
		fmt.Fprintf(stderr,
			"Initial admin token written to Secret %s/%s. Retrieve with: "+
				"kubectl get secret %s -n %s -o jsonpath='{.data.token}' | base64 -d\n",
			ns, name, name, ns)
		return
	}
	if !quiet {
		fmt.Fprintln(stderr, "Admin token Secret already exists — no changes.")
	}
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
	Tenants            bootstrapSection     `json:"tenants"`
	Runtimes           bootstrapSection     `json:"runtimes"`
	Users              bootstrapSection     `json:"users"`
	Pools              bootstrapSection     `json:"pools"`
	CredentialPools    bootstrapSection     `json:"credentialPools"`
	DelegationPolicies bootstrapSection     `json:"delegationPolicies"`
	Environments       bootstrapSection     `json:"environments"`
	AdminToken         *bootstrapAdminToken `json:"adminToken"`
}

// bootstrapAdminToken mirrors the §17.6 admin-credential section the
// gateway returns so the CLI can print the first-use prompt. F-24.1.7.
type bootstrapAdminToken struct {
	SecretCreated   bool   `json:"secretCreated"`
	SecretNamespace string `json:"secretNamespace"`
	SecretName      string `json:"secretName"`
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
		"tenant":           b.Tenants,
		"runtime":          b.Runtimes,
		"user":             b.Users,
		"pool":             b.Pools,
		"credentialPool":   b.CredentialPools,
		"delegationPolicy": b.DelegationPolicies,
		"environment":      b.Environments,
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

// listAllItems walks every page of a §15.1 cursor-paginated admin
// collection (spec §15.1 lines 1228-1253) and accumulates the `items`
// arrays into one slice. The CLI shows the full inventory rather than a
// single 50-item page, so it follows `cursor` until `hasMore` is false,
// requesting the 200-item maximum per page to minimise round trips.
//
// spec: §15.1 lines 1228-1253. F-15.1.6.
func listAllItems(ctx context.Context, c *ctl.Client, path string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	cursor := ""
	for {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		page := path + sep + "limit=200"
		if cursor != "" {
			page += "&cursor=" + url.QueryEscape(cursor)
		}
		var env struct {
			Items   []json.RawMessage `json:"items"`
			Cursor  string            `json:"cursor"`
			HasMore bool              `json:"hasMore"`
		}
		if err := c.Do(ctx, "GET", page, nil, &env); err != nil {
			return nil, err
		}
		all = append(all, env.Items...)
		if !env.HasMore || env.Cursor == "" {
			return all, nil
		}
		cursor = env.Cursor
	}
}

// listAllAdmin walks every page of a §15.1 paginated admin collection
// and decodes each item into T, returning the full typed inventory.
func listAllAdmin[T any](ctx context.Context, c *ctl.Client, path string) ([]T, error) {
	raws, err := listAllItems(ctx, c, path)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(raws))
	for _, raw := range raws {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// printItemList renders the full inventory of a §15.1 paginated admin
// collection under a single `{items: [...]}` envelope.
func printItemList(ctx context.Context, c *ctl.Client, path string, stdout, stderr io.Writer) int {
	items, err := listAllItems(ctx, c, path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	printJSON(stdout, map[string]any{"items": items})
	return 0
}
