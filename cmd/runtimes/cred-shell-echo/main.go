// SPDX-License-Identifier: MIT

// Command cred-shell-echo is the tier-9-test reference runtime that
// pairs the Basic-level echo loop with a Helm-renderable Runtime
// definition declaring `credentialPoolRefs`, deployed on an
// Alpine-based image that retains /bin/sh. The combination unblocks
// the credential-leakage probes (TestCredentialLeakageEnvironment
// and TestCredentialLeakageFilesystem): the credential-delivery path
// runs because the runtime declares credentials, and `kubectl exec
// ... cat /proc/<pid>/environ` works because the image has a shell.
//
// The binary itself is a thin wrapper around pkg/runtimekit/echocore
// — the Basic adapter protocol stays unchanged, so the existing
// tier-3 adapter contract tests pass unchanged. The shell + the
// credential declaration are deployment-side properties, not runtime
// behavior.
//
// This image SHOULD NOT be used in production: shipping a shell with
// an agent pod weakens the §13.1 pod-security posture. The
// `lenny-pod-security` admission webhook rejects it in production
// installations; tier-9 deploys it explicitly into the e2e overlay
// for the leakage probes.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/lennylabs/lenny/pkg/runtimekit"
	"github.com/lennylabs/lenny/pkg/runtimekit/echocore"
)

const (
	exitOK            = 0
	exitRuntimeError  = 1
	exitProtocolError = 2
)

func main() {
	// §4.7: Serve runs one §28.5.3 loop per adapter connection, so the
	// §5.2 recycle boundary's close of the ending session's runtime leaves
	// this process alive to serve the pod's next session.
	if err := runtimekit.Serve(context.Background(),
		func(ctx context.Context, in io.Reader, out io.Writer) error {
			return echocore.Run(ctx, in, out, os.Stderr)
		}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var pe echocore.ProtocolError
		if errors.As(err, &pe) {
			os.Exit(exitProtocolError)
		}
		os.Exit(exitRuntimeError)
	}
	os.Exit(exitOK)
}
