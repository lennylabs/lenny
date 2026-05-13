//go:build component

// SPDX-License-Identifier: MIT

package containers_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// spec: 12.3 (Postgres 14+)
// diagnosis: StartPostgres failed to provision or the returned pool
//
//	cannot execute a trivial query. Docker not running or the
//	test container is hitting a pull/network error.
func TestStartPostgresBasic(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{})

	ctx := context.Background()
	var one int
	if err := pg.Pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if one != 1 {
		t.Fatalf("SELECT 1 returned %d", one)
	}
	if pg.DSN == "" {
		t.Errorf("DSN is empty")
	}
}
