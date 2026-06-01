// SPDX-License-Identifier: MIT

// Command lenny is the §17.4 Embedded Mode entry point: a single
// binary that brings up a complete Lenny stack on localhost with no
// pre-existing Kubernetes cluster, Postgres, Redis, or OIDC provider.
//
// Command surface (§17.4, §24.19):
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
// The embedded stack runs the production gateway, controllers, CRDs,
// and storage interfaces. Embedded Mode signals the mode=embedded
// platform flag through the gateway's standard configuration surface;
// there are no mode-dependent code splits in platform business logic.
//
// The lenny binary is the same executable as lenny-ctl (§24) under a
// short name. The command logic lives in pkg/embedded/localcli so both
// binaries share it: invoked as lenny it defaults to Embedded Mode and
// targets the local stack; invoked as lenny-ctl <same-command> it
// behaves identically (§24.19 line 266). The Embedded Mode state
// directory is ~/.lenny/, overridable with LENNY_HOME.
package main

import (
	"os"

	"github.com/lennylabs/lenny/pkg/embedded/localcli"
)

// version is the CLI build version. The release pipeline stamps it via
// -ldflags "-X main.version=<tag>"; source builds report "dev". The
// symbol must exist for the linker override to bind (the release job
// passes the ldflag to ./cmd/lenny as well as ./cmd/lenny-ctl).
// spec: §24.0 line 23, §17.6 line 360.
var version = "dev"

func main() {
	os.Exit(localcli.Main(os.Args[1:], os.Stdout, os.Stderr, version))
}
