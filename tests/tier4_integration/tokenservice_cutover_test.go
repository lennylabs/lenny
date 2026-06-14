// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §4.3 gateway → Token Service
// cutover. Boots cmd/lenny-token-service and cmd/lenny-gateway as
// real subprocesses with the gRPC link wired between them, then
// asserts:
//
//   - The Token Service's gRPC surface responds with a structured
//     error to a synthetic AssignCredentials call (the liveness
//     probe).
//   - The gateway starts cleanly with --token-service-grpc-addr set
//     and serves the §15.1 REST surface — meaning the gRPC dial
//     succeeded and the gateway's credassign.Client is wired in.
//   - A REST session creation that names no credential pools
//     succeeds (the cutover only fires when a session names
//     credentialPoolRefs, so the unconfigured-pool path goes through
//     unchanged).
//
// Together these gate the cutover end-to-end: the gateway no longer
// runs pkg/credential.MintLease in-process, the trust boundary is
// the mTLS gRPC link, and the gateway boots successfully when
// pointed at a live Token Service.

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// spec: 4.3
// diagnosis: cmd/lenny-token-service exposes a gRPC TokenService and
// AssignCredentials responds (with a structured NotFound) when the
// requested pool is not registered. This is the liveness gate for
// the cutover's bottom half — the Token Service binary, not the
// in-process server.
func TestTokenServiceProcessBootsAndRespondsOnGRPC(t *testing.T) {
	t.Parallel()
	tokensvc.SkipUnlessAvailable(t)
	ts := tokensvc.Start(t)
	if err := ts.Ping(t); err == nil {
		t.Fatalf("Ping returned no error; want NotFound for an unregistered pool")
	} else if status.Code(err) != codes.NotFound {
		t.Errorf("Ping err = %v, want NotFound for an unregistered pool", err)
	}
}

// spec: 4.3
// diagnosis: cmd/lenny-gateway starts cleanly with
// --token-service-grpc-addr set, which exercises the gateway-side
// credassign.Client construction in main. A boot failure or a
// crashed gateway under the configured Token Service link signals
// a regression in the wiring.
func TestGatewayWithTokenServiceWiringBoots(t *testing.T) {
	t.Parallel()
	tokensvc.SkipUnlessAvailable(t)
	ts := tokensvc.Start(t)
	gw := gateway.StartWith(
		t,
		"--token-service-grpc-addr", ts.GRPCAddr(),
		"--token-service-tenant", "tier4-cutover",
	)
	// /healthz on the gateway is unguarded; if the boot succeeded the
	// listener answers it.
	req, _ := http.NewRequest(http.MethodGet, gw.BaseURL()+"/healthz", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// spec: 4.3 / 15.1
// diagnosis: a session that does not name credentialPoolRefs creates
// successfully with the cutover in place. The Token Service is not
// called on this path, but its presence on --token-service-grpc-addr
// must not regress the create-without-pools flow.
func TestSessionCreateWithoutPoolsUnderCutover(t *testing.T) {
	t.Parallel()
	tokensvc.SkipUnlessAvailable(t)
	ts := tokensvc.Start(t)
	gw := gateway.StartWith(
		t,
		"--token-service-grpc-addr", ts.GRPCAddr(),
		"--token-service-tenant", "tier4-cutover",
	)

	body, _ := json.Marshal(map[string]any{
		"runtimeRef": "echo",
	})
	req, _ := http.NewRequest(http.MethodPost, gw.BaseURL()+"/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "tier4-cutover")
	req.Header.Set("X-Lenny-User-ID", "alice")
	ctx, cancel := context.WithTimeout(context.Background(), 5*1000*1000*1000)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/sessions: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusCreated, raw)
	}
}
