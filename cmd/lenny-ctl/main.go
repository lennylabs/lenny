// SPDX-License-Identifier: MIT

// Command lenny-ctl is the operator CLI for the Lenny gateway. It
// wraps the §15.1 admin API surface with a §24-style command tree.
//
// v1 covers the resource-management subset:
//
//	lenny-ctl health
//	lenny-ctl version
//	lenny-ctl tenants list|get|create
//	lenny-ctl runtimes list|get
//	lenny-ctl bootstrap --from-values <file>
//
// Auth: pass --bearer <token> for a clustered gateway, or rely on
// the dev-header path (--dev-tenant / --dev-roles) for Embedded
// Mode. The target gateway is --api-url (default
// http://localhost:8080).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/ctl"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
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
	case "tenants":
		return cmdTenants(ctx, client, rest[1:], stdout, stderr)
	case "runtimes":
		return cmdRuntimes(ctx, client, rest[1:], stdout, stderr)
	case "bootstrap":
		return cmdBootstrap(ctx, client, rest[1:], stdout, stderr)
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
  --bearer <token>     Operator bearer token
  --dev-tenant <id>    Dev-header tenant (Embedded Mode)
  --dev-roles <roles>  Dev-header roles, comma-separated (Embedded Mode)

Commands:
  health                       Print the platform health report
  version                      Print the gateway version
  tenants list                 List tenants
  tenants get <id>             Get a tenant
  tenants create <id> [name]   Create a tenant
  runtimes list                List runtimes
  runtimes get <name>          Get a runtime
  bootstrap --from-values <f>  Apply a seed file (tenants/runtimes/users)`

type globalFlags struct {
	apiURL    string
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
		case "--bearer":
			if i+1 < len(args) {
				f.bearer = args[i+1]
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

func cmdHealth(ctx context.Context, c *ctl.Client, stdout, stderr *os.File) int {
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

func cmdVersion(ctx context.Context, c *ctl.Client, stdout, stderr *os.File) int {
	var v map[string]any
	if err := c.Do(ctx, "GET", "/v1/admin/platform/version", nil, &v); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, v)
	return 0
}

func cmdTenants(ctx context.Context, c *ctl.Client, args []string, stdout, stderr *os.File) int {
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

func cmdRuntimes(ctx context.Context, c *ctl.Client, args []string, stdout, stderr *os.File) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: runtimes requires a subcommand (list|get)")
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
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown runtimes subcommand %q\n", args[0])
		return 2
	}
	return 0
}

func cmdBootstrap(ctx context.Context, c *ctl.Client, args []string, stdout, stderr *os.File) int {
	var fromValues string
	for i := 0; i < len(args); i++ {
		if args[i] == "--from-values" && i+1 < len(args) {
			fromValues = args[i+1]
			i++
		}
	}
	if fromValues == "" {
		fmt.Fprintln(stderr, "lenny-ctl: bootstrap requires --from-values <file>")
		return 2
	}
	raw, err := os.ReadFile(fromValues)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: read %s: %v\n", fromValues, err)
		return 1
	}
	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: %s is not valid JSON: %v\n", fromValues, err)
		return 1
	}
	var out map[string]any
	if err := c.Do(ctx, "POST", "/v1/admin/bootstrap", body, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

func printJSON(w *os.File, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
