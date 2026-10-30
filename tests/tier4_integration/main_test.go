// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/binbuild"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// harnessControlPlane is the envtest control plane every lifecycle harness
// in this package runs on. Each harness serves a namespace of its own on it
// (lifecycleharness.Options.ControlPlane), so a harness build does not boot
// etcd and kube-apiserver (TEST-GAPS T-17.4.32).
var harnessControlPlane envtest.Shared

// harnessPostgres is the Postgres container the tests of this package
// share, each in a database cloned from one migrated template: the
// compose-backed lifecycle harnesses (lifecycleharness.SharedPostgresStore)
// and every test that takes its database from sharedDatabase.
var harnessPostgres containers.SharedPostgres

// sharedDatabase returns a database of t's own on harnessPostgres, migrated
// with the production migrations and dropped at t's cleanup. A test uses it
// in place of containers.StartPostgres unless it stops Postgres to inject an
// outage, alters a role or another per-server object, needs an unmigrated
// server, or runs a different Postgres image; each of those keeps a
// container of its own (TEST-GAPS T-17.4.33).
func sharedDatabase(t *testing.T) *containers.Postgres {
	t.Helper()
	return harnessPostgres.Database(t, filepath.Join(schematest.RepoRoot(t), "migrations"))
}

func TestMain(m *testing.M) {
	code := m.Run()
	if err := harnessControlPlane.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "envtest: stop shared API server: %v\n", err)
	}
	if err := harnessPostgres.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "containers: stop shared postgres: %v\n", err)
	}
	if err := binbuild.Cleanup(); err != nil {
		fmt.Fprintf(os.Stderr, "binbuild: remove the shared binaries: %v\n", err)
	}
	os.Exit(code)
}
