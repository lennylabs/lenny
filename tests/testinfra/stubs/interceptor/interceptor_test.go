// SPDX-License-Identifier: MIT

package interceptor_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	interceptorv1 "github.com/lennylabs/lenny/pkg/proto/interceptor/v1"
	stubinterceptor "github.com/lennylabs/lenny/tests/testinfra/stubs/interceptor"
)

// dial builds a plaintext client against the stub's real TCP address, the
// same insecure transport the gateway uses for a dev-mode interceptor.
func dial(t *testing.T, addr string) interceptorv1.RequestInterceptorClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial stub: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return interceptorv1.NewRequestInterceptorClient(conn)
}

// spec: §4.8 — external RequestInterceptor ALLOW / REJECT / MODIFY over
// gRPC. Verifies the stub returns each configured action and records the
// forwarded request.
func TestStubActions_spec_4_8(t *testing.T) {
	t.Run("allow", func(t *testing.T) {
		stub := stubinterceptor.Start(t, stubinterceptor.Allow())
		c := dial(t, stub.Addr())
		res, err := c.Intercept(context.Background(), &interceptorv1.InterceptRequest{Phase: "PostAgentOutput", TenantId: "acme"})
		if err != nil {
			t.Fatalf("Intercept: %v", err)
		}
		if res.GetAction() != interceptorv1.InterceptResponse_ALLOW {
			t.Errorf("action = %v, want ALLOW", res.GetAction())
		}
		reqs := stub.Requests()
		if len(reqs) != 1 || reqs[0].GetTenantId() != "acme" {
			t.Errorf("recorded requests = %+v, want one for tenant acme", reqs)
		}
	})

	t.Run("reject", func(t *testing.T) {
		stub := stubinterceptor.Start(t, stubinterceptor.Reject("nope"))
		c := dial(t, stub.Addr())
		res, err := c.Intercept(context.Background(), &interceptorv1.InterceptRequest{})
		if err != nil {
			t.Fatalf("Intercept: %v", err)
		}
		if res.GetAction() != interceptorv1.InterceptResponse_REJECT || res.GetReason() != "nope" {
			t.Errorf("got action=%v reason=%q, want REJECT/nope", res.GetAction(), res.GetReason())
		}
	})

	t.Run("modify", func(t *testing.T) {
		stub := stubinterceptor.Start(t, stubinterceptor.Modify([]byte("changed")))
		c := dial(t, stub.Addr())
		res, err := c.Intercept(context.Background(), &interceptorv1.InterceptRequest{})
		if err != nil {
			t.Fatalf("Intercept: %v", err)
		}
		if res.GetAction() != interceptorv1.InterceptResponse_MODIFY || string(res.GetModifiedContent()) != "changed" {
			t.Errorf("got action=%v content=%q, want MODIFY/changed", res.GetAction(), res.GetModifiedContent())
		}
	})
}

// spec: §4.8 timeout/failPolicy — the Hang handler blocks until the call
// deadline elapses, so the gateway's per-interceptor timeout fires.
func TestStubHang_spec_4_8(t *testing.T) {
	stub := stubinterceptor.Start(t, stubinterceptor.Hang())
	c := dial(t, stub.Addr())
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := c.Intercept(ctx, &interceptorv1.InterceptRequest{}); err == nil {
		t.Fatal("Intercept against a hanging stub returned nil error; want deadline exceeded")
	}
}
