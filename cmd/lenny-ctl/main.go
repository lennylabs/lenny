// SPDX-License-Identifier: MIT

// Command lenny-ctl is the operator-context entry point for the Lenny
// CLI. It is a thin shim over pkg/ctlcli, which implements the unified
// command surface shared with the short-name `lenny` binary: both names
// support every subcommand per the §24 preamble (line 17). The operator
// command tree (§24 admin groups, §25.14 operability groups) and the
// §24.19 Embedded Mode local commands are all reachable here.
package main

import (
	"os"

	"github.com/lennylabs/lenny/pkg/ctlcli"
)

// version is the CLI build version. The release pipeline stamps it via
// -ldflags "-X main.version=<tag>"; source builds report "dev". The
// symbol must exist for the linker override to bind, and ctlcli.Run
// receives it so the version command prints the stamped tag.
// spec: §24.0 line 23, §17.6 line 360.
var version = "dev"

func main() {
	os.Exit(ctlcli.Run(os.Args[1:], os.Stdout, os.Stderr, version))
}
