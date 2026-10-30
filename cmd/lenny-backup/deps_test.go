// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"strings"
	"testing"
)

// baseDepsInput is a minimally-valid depsInput. The MinIO, Postgres,
// and Redis clients it builds connect lazily, so resolveDeps succeeds
// without a live backend.
func baseDepsInput() depsInput {
	return depsInput{
		minioEndpoint: "localhost:9000",
		minioBucket:   "lenny-backups",
		minioTLS:      true,
		reportDSN:     "postgres://user:pass@localhost:5432/lenny",
	}
}

// TestResolveDepsNoRedisLeavesEmitterNil_spec_25_5 confirms that without
// a --redis-url the run carries no operational-event emitter, so the
// §25.3 backup_completed / backup_failed events are not emitted (the
// audit row still lands).
func TestResolveDepsNoRedisLeavesEmitterNil_spec_25_5(t *testing.T) {
	d, err := resolveDeps(context.Background(), baseDepsInput())
	if err != nil {
		t.Fatalf("resolveDeps: %v", err)
	}
	defer d.close()
	if d.opsEmitter != nil {
		t.Error("opsEmitter should be nil without a configured Redis URL")
	}
}

// TestResolveDepsWithRedisBuildsStreamEmitter_spec_25_5 confirms that a
// --redis-url wires the §25.5 StreamEmitter so the run streams
// backup_completed / backup_failed to ops:events:stream.
func TestResolveDepsWithRedisBuildsStreamEmitter_spec_25_5(t *testing.T) {
	in := baseDepsInput()
	in.redisURL = "rediss://redis:6379"
	in.redisPassword = "s3cret"
	d, err := resolveDeps(context.Background(), in)
	if err != nil {
		t.Fatalf("resolveDeps: %v", err)
	}
	defer d.close()
	if d.opsEmitter == nil {
		t.Error("opsEmitter should be non-nil with a configured Redis URL")
	}
}

// TestResolveDepsRejectsInsecureRedis_spec_12_4 confirms the §12.4
// AUTH+TLS invariant is enforced for the backup Job's Redis client: a
// plaintext, password-less URL fails closed rather than silently
// emitting over an unauthenticated connection.
func TestResolveDepsRejectsInsecureRedis_spec_12_4(t *testing.T) {
	in := baseDepsInput()
	in.redisURL = "redis://redis:6379"
	d, err := resolveDeps(context.Background(), in)
	if err == nil {
		d.close()
		t.Fatal("resolveDeps should reject a plaintext, password-less Redis URL")
	}
	if !strings.Contains(err.Error(), "event stream") {
		t.Errorf("error = %v, want it to mention the §25.5 event stream", err)
	}
}
