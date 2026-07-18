// SPDX-License-Identifier: MIT

package ctlcli

import (
	"flag"
	"fmt"
	"io"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
)

// cmdSLO implements the §16.10 `slo` group. The only subcommand is
// `export`, which renders the §16.5 SLO catalog as OpenSLO v1
// SLO/SLI/AlertPolicy documents plus a shared AlertNotificationTarget the
// alert policies reference. It runs offline from the embedded
// pkg/alerting/rules catalog (the same source the bundled §16.5
// burn-rate alerts and the chart's OpenSLO ConfigMap derive from), so it
// reaches no gateway. spec: §16.10 lines 732-736.
func cmdSLO(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl: slo requires a subcommand (export)")
		return 2
	}
	switch args[0] {
	case "export":
		return cmdSLOExport(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl: unknown slo subcommand %q\n", args[0])
		return 2
	}
}

func cmdSLOExport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("slo export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "openslo", "export format; only \"openslo\" is supported")
	tier := fs.String("tier", "tier1", "deployment tier scoping the rendered SLO queries (tier1|tier2|tier3)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *format != "openslo" {
		fmt.Fprintf(stderr, "lenny-ctl: unsupported slo export format %q (only \"openslo\")\n", *format)
		return 2
	}
	out, err := rules.RenderOpenSLO(rules.OpenSLOService, *tier, rules.OpenSLODefaultNotificationTarget)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: slo export: %v\n", err)
		return 1
	}
	if _, err := stdout.Write(out); err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: slo export: %v\n", err)
		return 1
	}
	return 0
}
