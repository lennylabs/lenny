// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/vcscred"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

const (
	// hungCallTimeout is the shortened prober deadline the hang tests run with.
	hungCallTimeout = 100 * time.Millisecond
	// hungCallLimit fails a test whose call outlives the deadline. It is
	// below credassign.DefaultClientTimeout, so a prober that ignored the
	// shortened deadline fails too.
	hungCallLimit = 3 * time.Second
)

// hungTokenService accepts ProbeSecretAccess and never answers. Its handler
// ignores the call's context, as a frozen process does, so only a deadline
// on the client side ends the call.
type hungTokenService struct {
	tokensv1.UnimplementedTokenServiceServer
	release chan struct{}
	calls   atomic.Int32
}

func (h *hungTokenService) ProbeSecretAccess(context.Context, *tokensv1.ProbeSecretAccessRequest) (*tokensv1.ProbeSecretAccessResponse, error) {
	h.calls.Add(1)
	<-h.release
	return nil, status.Error(codes.Unavailable, "released at test end")
}

// dialHungTokenService serves a hungTokenService over an in-memory gRPC
// connection and returns the service with a client on that connection.
func dialHungTokenService(t *testing.T) (*hungTokenService, tokensv1.TokenServiceClient) {
	t.Helper()
	hung := &hungTokenService{release: make(chan struct{})}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	tokensv1.RegisterTokenServiceServer(srv, hung)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	t.Cleanup(func() { close(hung.release) })
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return hung, tokensv1.NewTokenServiceClient(conn)
}

// withinHungCallLimit returns fn's result, failing the test when fn outlives
// hungCallLimit rather than leaving the test blocked on the hang.
func withinHungCallLimit[T any](t *testing.T, fn func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- fn() }()
	select {
	case v := <-done:
		return v
	case <-time.After(hungCallLimit):
		t.Fatalf("call still blocked after %v on a silent Token Service", hungCallLimit)
		panic("unreachable")
	}
}

// deadlineTokenServiceClient records the deadline each ProbeSecretAccess
// call arrives with.
type deadlineTokenServiceClient struct {
	tokensv1.TokenServiceClient
	deadline    time.Time
	hasDeadline bool
}

func (d *deadlineTokenServiceClient) ProbeSecretAccess(ctx context.Context, _ *tokensv1.ProbeSecretAccessRequest, _ ...grpc.CallOption) (*tokensv1.ProbeSecretAccessResponse, error) {
	d.deadline, d.hasDeadline = ctx.Deadline()
	return verdictResp(tokensv1.Verdict_VERDICT_ALLOWED), nil
}

// spec: §4.9 (Credential Storage Topology: admin-time Secret probe; Token
// Service secret resolution) — both production constructors bound each
// ProbeSecretAccess call by credassign.DefaultClientTimeout, whatever context
// the caller passes, so the hang tests' shortened deadline stands for the
// shipped one. The assertion fails against a prober that sends the caller's
// context unchanged.
func TestTokenServiceSecretProber_ProductionCallsCarryTheLinkDeadline(t *testing.T) {
	cases := []struct {
		name string
		call func(stub tokensv1.TokenServiceClient) error
	}{
		{"admin probe", func(stub tokensv1.TokenServiceClient) error {
			_, err := newTokenServiceSecretProber(stub, testCredNS).ProbeSecretAccess(context.Background(), "gh-token")
			return err
		}},
		{"VCS read", func(stub tokensv1.TokenServiceClient) error {
			_, err := newTokenServiceVCSReader(stub, testCredNS, nil).Resolve(context.Background(), "gh-token")
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &deadlineTokenServiceClient{}
			before := time.Now()
			if err := tc.call(stub); err != nil {
				t.Fatalf("call: %v", err)
			}
			if !stub.hasDeadline {
				t.Fatal("the RPC carried no deadline")
			}
			if got := stub.deadline.Sub(before); got <= 0 || got > credassign.DefaultClientTimeout+time.Second {
				t.Fatalf("deadline is %v after the call began, want about %v", got, credassign.DefaultClientTimeout)
			}
		})
	}
}

// spec: §4.9 (Credential Storage Topology: admin-time Secret probe rule 2),
// §15.1 (CREDENTIAL_PROBE_UNAVAILABLE) — a Token Service that accepts the
// probe and never answers returns no verdict, so the admin write ends at the
// call deadline with 503 CREDENTIAL_PROBE_UNAVAILABLE and commits nothing.
// Against a prober with no deadline the request blocks until the client
// leaves, and the limit fails the test.
func TestAdminSecretProbe_SilentTokenServiceIs503WithinDeadline(t *testing.T) {
	hung, stub := dialHungTokenService(t)
	prober := newTokenServiceSecretProber(stub, testCredNS)
	prober.timeout = hungCallTimeout
	pools := credentialpoolstore.NewMemory()
	r := admin.NewRouter(tenantstore.NewMemory(), admin.Options{CredentialsNamespace: testCredNS}).
		WithCredentialPools(pools).WithSecretAccessProber(prober)
	w := adminProbeWire{handler: r.Handler(), pools: pools}

	rec := withinHungCallLimit(t, func() *httptest.ResponseRecorder { return w.createPoolWithRef(t, "gh-token") })
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "CREDENTIAL_PROBE_UNAVAILABLE" {
		t.Fatalf("status %d body %s, want 503 CREDENTIAL_PROBE_UNAVAILABLE", rec.Code, rec.Body)
	}
	if hung.calls.Load() != 1 {
		t.Fatalf("RPC calls = %d, want 1", hung.calls.Load())
	}
	if _, err := pools.Get(context.Background(), "acme", "wire-pool"); err == nil {
		t.Fatal("a pool row committed after a probe with no verdict")
	}
}

// spec: §4.9 (Token Service secret resolution) rule 3, §4.3 — a Token Service
// that accepts the keyed read and never answers fails the read with a
// DeadlineExceeded that is not ErrNoUsableCredential, so plan binding answers
// the transient code. Each hang counts as a breaker failure, so the breaker
// opens at the threshold and the next read makes no RPC. Against a reader with
// no deadline fn never returns, the limit fails the test, and the breaker
// records nothing.
func TestTokenServiceVCSReader_SilentTokenServiceFailsTransientAndFeedsBreaker(t *testing.T) {
	hung, stub := dialHungTokenService(t)
	sub := freshTokenServiceSubsystem()
	r := newTokenServiceVCSReader(stub, testCredNS, sub)
	r.timeout = hungCallTimeout
	resolve := func() error {
		return withinHungCallLimit(t, func() error {
			_, err := r.Resolve(context.Background(), "gh-token")
			return err
		})
	}

	for i := 0; i < subsystem.DefaultFailureThreshold; i++ {
		err := resolve()
		if status.Code(errors.Unwrap(err)) != codes.DeadlineExceeded {
			t.Fatalf("call %d: error = %v, want the wrapped gRPC DeadlineExceeded", i, err)
		}
		if errors.Is(err, vcscred.ErrNoUsableCredential) {
			t.Fatalf("call %d: error %v must not read as a missing credential", i, err)
		}
	}
	if sub.State() != subsystem.StateOpen {
		t.Fatalf("breaker state = %v, want open after %d hung reads", sub.State(), subsystem.DefaultFailureThreshold)
	}
	if err := resolve(); !errors.Is(err, subsystem.ErrCircuitOpen) {
		t.Fatalf("Resolve after threshold error = %v, want ErrCircuitOpen", err)
	}
	if got := int(hung.calls.Load()); got != subsystem.DefaultFailureThreshold {
		t.Fatalf("RPC calls = %d, want %d (none once open)", got, subsystem.DefaultFailureThreshold)
	}
}
