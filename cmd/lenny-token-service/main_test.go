// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// spec: 4.3 (§4.3 / §12.2.4 Token Service gRPC surface — the binary's
// --grpc-addr flag exposes the lenny.tokenservice.v1.TokenService RPCs)
// diagnosis: a smoke test that builds cmd/lenny-token-service, starts
// the binary against an ephemeral gRPC port, and dials the
// AssignCredentials RPC. With no credential pools registered, the call
// returns NotFound for the requested pool, which is the documented
// fail-fast behavior in pkg/tokenservice/grpc.go.
func TestBinaryServesGRPC(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}

	bin := filepath.Join(t.TempDir(), "lenny-token-service")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	// Bind ephemeral ports for both surfaces. The HTTP token-exchange
	// surface is irrelevant to this test but the binary still binds it.
	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("http listen: %v", err)
	}
	httpAddr := httpLis.Addr().String()
	_ = httpLis.Close()
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("grpc listen: %v", err)
	}
	grpcAddr := grpcLis.Addr().String()
	_ = grpcLis.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(
		ctx, bin,
		"--addr="+httpAddr,
		"--grpc-addr="+grpcAddr,
		"--issuer=https://test.local/token",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})

	// Poll until the gRPC port accepts a TCP connection. grpc.NewClient
	// is lazy and would not surface a server-not-ready state on the
	// RPC, so a direct TCP dial is the readiness signal.
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		probe, err := net.DialTimeout("tcp", grpcAddr, 250*time.Millisecond)
		if err == nil {
			_ = probe.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatal("token-service gRPC port did not accept a TCP connection within 5s")
	}
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	client := tokensv1.NewTokenServiceClient(conn)
	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer rpcCancel()
	_, err = client.AssignCredentials(rpcCtx, &tokensv1.AssignCredentialsRequest{
		TenantId:     "acme",
		SessionId:    "ses-1",
		PoolIds:      []string{"openai-default"},
		PodSpiffeUri: "spiffe://lenny/agent/acme/ses-1",
	})
	if err == nil {
		t.Fatal("AssignCredentials with no registered pool returned nil error; want NotFound")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("AssignCredentials error is not a gRPC status: %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Errorf("AssignCredentials code = %s, want NotFound (no pool registered)", st.Code())
	}
}
