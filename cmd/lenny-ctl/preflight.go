// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// cmdPreflight implements the §10.5 step-1 CRD-currency preflight:
// `lenny-ctl preflight --config <values.yaml>`. It compares the
// `lenny.dev/schema-version` annotation on every installed Lenny CRD
// against the version this binary expects and exits non-zero on a
// mismatch, so an operator catches a stale CRD before running
// `helm upgrade` (the spec's "assert CRD version currency" step). It is
// the interactive equivalent of the `lenny-preflight` Helm pre-upgrade
// Job; scripts/lenny-upgrade.sh runs it as step 1.
//
// The check builds a controller-runtime client from the ambient
// kubeconfig (KUBECONFIG / in-cluster), so it targets the cluster the
// operator is pointed at rather than the gateway API.
//
// spec: §10.5 line 443. F-10.5.4.
func cmdPreflight(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("preflight", flag.ContinueOnError)
	fs.SetOutput(stderr)
	// --config is accepted for parity with the documented invocation and
	// future value-gated checks; the CRD-currency assertion compares the
	// binary-embedded schema version against the cluster, so it reads no
	// values content.
	_ = fs.String("config", "", "path to the Helm values.yaml (reserved for value-gated checks)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl preflight: resolve cluster config: %v\n", err)
		return 1
	}
	scheme := runtime.NewScheme()
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl preflight: build cluster client: %v\n", err)
		return 1
	}
	return runPreflightCRDCheck(ctx, cl, stdout, stderr)
}

// runPreflightCRDCheck runs the CRD schema-version currency check against
// reader and reports the outcome. It is split from cmdPreflight so a test
// can drive it with a fake client.
//
// spec: §10.5 line 443. F-10.5.4.
func runPreflightCRDCheck(ctx context.Context, reader client.Reader, stdout, stderr io.Writer) int {
	decision := preflight.CRDSchemaVersionCheck{
		Expected: preflight.CurrentCRDSchemaVersion,
	}.Decide(ctx, reader)
	if !decision.Passed {
		fmt.Fprintf(stderr, "lenny-ctl preflight: %s\n", decision.Reason)
		return 1
	}
	fmt.Fprintf(stdout, "lenny-ctl preflight: %s\n", decision.Reason)
	return 0
}
