// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// harnessControlPlane is the envtest control plane every lifecycle harness
// in this package runs on. Each harness serves a namespace of its own on it
// (lifecycleharness.Options.ControlPlane), so a harness build does not boot
// etcd and kube-apiserver (TEST-GAPS T-17.4.31).
var harnessControlPlane envtest.Shared

// harnessPostgres is the Postgres container every compose-backed lifecycle
// harness in this package stores its sessions on, each in a database
// cloned from one migrated template (lifecycleharness.SharedPostgresStore).
var harnessPostgres containers.SharedPostgres

func TestMain(m *testing.M) {
	code := m.Run()
	if err := harnessControlPlane.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "envtest: stop shared API server: %v\n", err)
	}
	if err := harnessPostgres.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "containers: stop shared postgres: %v\n", err)
	}
	os.Exit(code)
}
