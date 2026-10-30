// SPDX-License-Identifier: MIT

package inproc

import (
	"bytes"
	"context"
	"net/http"
	"testing"
)

// TestGatewayBootsRealLenny pins the TESTING.md §12.7.a requirement that
// the in-process multi-component harness "boots a single-binary Lenny with
// miniredis, an embedded Postgres adapter, and a fake Kubernetes API
// surface". A real boot routes a session create through the gateway's
// §5.2 slot-claim path, which writes the per-pod concurrency counter into
// the embedded Redis. The current gateway is a self-contained in-memory
// stub whose session map is disconnected from miniredis, so a create
// leaves the embedded Redis untouched; that is the mock behavior this
// test exists to reject once the real boot path is wired.
//
// spec: TESTING.md §12.7.a (tier 7a in-process multi-component harness
// boots a single-binary Lenny with miniredis, an embedded Postgres
// adapter, and a fake Kubernetes API surface)
func TestGatewayBootsRealLenny(t *testing.T) {
	t.Skip("inproc gateway is a stub, not the real single-binary Lenny boot path; TEST-GAPS.md finding on the in-process multi-component harness remains open")

	env := New(Config{})
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer env.Stop(context.Background())

	req, err := http.NewRequest(http.MethodPost, env.GatewayURL()+"/v1/sessions",
		bytes.NewReader([]byte(`{"runtimeRef":"echo"}`)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/sessions: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d want %d", resp.StatusCode, http.StatusCreated)
	}

	// A real single-binary Lenny gateway claims a §5.2 concurrency slot on
	// create, writing the per-pod counter into the embedded Redis. The stub
	// never touches miniredis, so an empty keyspace here means the harness
	// exercised the mock rather than real Lenny code.
	if keys := env.redis.Keys(); len(keys) == 0 {
		t.Fatalf("no keys in embedded Redis after session create: harness ran the gateway stub, not the real single-binary Lenny boot path")
	}
}
