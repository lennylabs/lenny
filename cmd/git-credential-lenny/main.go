// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

// main is the §26.2 in-pod Git credential helper entrypoint. Git invokes
// it as `git-credential-lenny <operation>` with the request on stdin.
// spec: §26.2 line 119. F-26.2.5.
func main() {
	manifestPath := os.Getenv("LENNY_ADAPTER_MANIFEST")
	if manifestPath == "" {
		manifestPath = defaultManifestPath
	}
	// spec: §26.2 line 119 — gitClone.url declares vcs.<provider>.read or
	// .write; Git does not tell the helper which operation it is for, so
	// the helper requests read (clone/fetch) by default. A session that
	// pushes sets LENNY_VCS_MODE=write. The scope drives the §4.9.2 audit
	// record; the resolved token covers the operations its grants allow.
	mode := "read"
	if os.Getenv("LENNY_VCS_MODE") == "write" {
		mode = "write"
	}

	op := ""
	if len(os.Args) > 1 {
		op = os.Args[1]
	}

	var mint minterFunc
	// Only the `get` operation reaches the gateway; store/erase are
	// no-ops, so a manifest read failure must not fail those.
	if op == "get" {
		socket, nonce, err := readManifest(manifestPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "git-credential-lenny: %v\n", err)
			os.Exit(1)
		}
		mint = socketMinter(socket, nonce, 10*time.Second)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := Run(ctx, os.Args[1:], os.Stdin, os.Stdout, mode, mint); err != nil {
		fmt.Fprintf(os.Stderr, "git-credential-lenny: %v\n", err)
		os.Exit(1)
	}
}
