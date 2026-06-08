// SPDX-License-Identifier: MIT

// Command lenny is the §17.4 Embedded Mode entry point: a single binary
// that brings up a complete Lenny stack on localhost with no
// pre-existing Kubernetes cluster, Postgres, Redis, or OIDC provider.
//
// Embedded Mode command surface (§17.4, §24.19):
//
//	lenny up                Start the embedded stack. Idempotent.
//	lenny down [--purge]    Tear the stack down. --purge removes ~/.lenny.
//	lenny status            Print component health and active session count.
//	lenny logs [<comp>]     Tail merged logs, or filter to one component.
//	lenny restart <comp>    Restart one component in place.
//	lenny token print       Emit a bearer token for the built-in user.
//	lenny image <...>       Manage the embedded containerd image store.
//	lenny session new       Start a session against the running gateway.
//
// The lenny binary is the same executable as lenny-ctl (§24) under a
// short name. Both names dispatch through pkg/ctlcli and support every
// subcommand per the §24 preamble (line 17): `lenny bootstrap`,
// `lenny admin tenants list`, and `lenny drift report` behave identically
// to their `lenny-ctl` forms. Invoked as lenny, the version banner names
// the short binary; the Embedded Mode local commands target the local
// stack rooted at ~/.lenny/ (override with LENNY_HOME).
package main

import (
	"os"

	"github.com/lennylabs/lenny/pkg/ctlcli"
)

// version is the CLI build version. The release pipeline stamps it via
// -ldflags "-X main.version=<tag>"; source builds report "dev". The
// symbol must exist for the linker override to bind (the release job
// passes the ldflag to ./cmd/lenny as well as ./cmd/lenny-ctl).
// spec: §24.0 line 23, §17.6 line 360.
var version = "dev"

func main() {
	os.Exit(ctlcli.Run(os.Args[1:], os.Stdout, os.Stderr, version))
}
