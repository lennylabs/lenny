// SPDX-License-Identifier: MIT

package interceptor_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	interceptorv1 "github.com/lennylabs/lenny/pkg/proto/interceptor/v1"
)

// fakeInterceptorServer is a configurable §4 RequestInterceptor gRPC
// server. fn produces the response for each Intercept call; when fn is
// nil it returns ALLOW. lastReq records the most recent request so a
// test can assert the gateway forwarded the right payload.
type fakeInterceptorServer struct {
	interceptorv1.UnimplementedRequestInterceptorServer
	fn      func(*interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error)
	lastReq *interceptorv1.InterceptRequest
}

func (s *fakeInterceptorServer) Intercept(_ context.Context, req *interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
	s.lastReq = req
	if s.fn != nil {
		return s.fn(req)
	}
	return &interceptorv1.InterceptResponse{Action: interceptorv1.InterceptResponse_ALLOW}, nil
}

// startFakeInterceptor serves srv on an in-memory bufconn listener and
// returns a generated RequestInterceptor client wired to it. The server
// and connection are torn down on test cleanup.
func startFakeInterceptor(t *testing.T, srv *fakeInterceptorServer) interceptor.InterceptClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	interceptorv1.RegisterRequestInterceptorServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial fake interceptor: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		gs.Stop()
	})
	return interceptorv1.NewRequestInterceptorClient(conn)
}

func TestExternalAllowThroughGRPC(t *testing.T) {
	srv := &fakeInterceptorServer{
		fn: func(*interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
			return &interceptorv1.InterceptResponse{Action: interceptorv1.InterceptResponse_ALLOW}, nil
		},
	}
	c := interceptor.NewChain()
	if _, err := c.RegisterExternal(interceptor.PhasePreDelegation, interceptor.ExternalConfig{
		Name:     "ext-allow",
		Priority: 500,
		Client:   startFakeInterceptor(t, srv),
	}); err != nil {
		t.Fatalf("RegisterExternal: %v", err)
	}

	res := c.Run(context.Background(), interceptor.Request{
		Phase:     interceptor.PhasePreDelegation,
		SessionID: "sess-1",
		TenantID:  "acme",
		Content:   []byte("hello"),
	})
	if res.Action != interceptor.ActionAllow {
		t.Errorf("action = %v, want ALLOW", res.Action)
	}
	// The gateway must forward the phase, identity, and payload verbatim.
	if srv.lastReq.GetPhase() != string(interceptor.PhasePreDelegation) {
		t.Errorf("forwarded phase = %q, want %q", srv.lastReq.GetPhase(), interceptor.PhasePreDelegation)
	}
	if srv.lastReq.GetSessionId() != "sess-1" || srv.lastReq.GetTenantId() != "acme" {
		t.Errorf("forwarded identity = %q/%q, want sess-1/acme",
			srv.lastReq.GetSessionId(), srv.lastReq.GetTenantId())
	}
	if string(srv.lastReq.GetContent()) != "hello" {
		t.Errorf("forwarded content = %q, want hello", srv.lastReq.GetContent())
	}
}

func TestExternalRejectThroughGRPC(t *testing.T) {
	srv := &fakeInterceptorServer{
		fn: func(*interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
			return &interceptorv1.InterceptResponse{
				Action: interceptorv1.InterceptResponse_REJECT,
				Reason: "content blocked by classifier",
			}, nil
		},
	}
	c := interceptor.NewChain()
	if _, err := c.RegisterExternal(interceptor.PhasePreDelegation, interceptor.ExternalConfig{
		Name:   "ext-reject",
		Client: startFakeInterceptor(t, srv),
	}); err != nil {
		t.Fatalf("RegisterExternal: %v", err)
	}

	res := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation})
	if res.Action != interceptor.ActionReject {
		t.Fatalf("action = %v, want REJECT", res.Action)
	}
	if res.Reason != "content blocked by classifier" {
		t.Errorf("reason = %q, want the remote interceptor's reason", res.Reason)
	}
}

func TestExternalRejectShortCircuitsLaterInterceptor(t *testing.T) {
	srv := &fakeInterceptorServer{
		fn: func(*interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
			return &interceptorv1.InterceptResponse{
				Action: interceptorv1.InterceptResponse_REJECT,
				Reason: "blocked",
			}, nil
		},
	}
	c := interceptor.NewChain()
	if _, err := c.RegisterExternal(interceptor.PhasePreDelegation, interceptor.ExternalConfig{
		Name:     "ext-reject",
		Priority: 200,
		Client:   startFakeInterceptor(t, srv),
	}); err != nil {
		t.Fatalf("RegisterExternal: %v", err)
	}
	var calls []string
	mustRegister(t, c, interceptor.PhasePreDelegation,
		&fakeInterceptor{name: "after", priority: 300, builtin: true, calls: &calls})

	res := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation})
	if res.Action != interceptor.ActionReject {
		t.Fatalf("action = %v, want REJECT", res.Action)
	}
	if len(calls) != 0 {
		t.Errorf("later interceptor ran %v times; an external REJECT must short-circuit the chain", calls)
	}
}

func TestExternalModifyRewritesPayload(t *testing.T) {
	srv := &fakeInterceptorServer{
		fn: func(req *interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
			return &interceptorv1.InterceptResponse{
				Action:          interceptorv1.InterceptResponse_MODIFY,
				ModifiedContent: append(append([]byte(nil), req.GetContent()...), []byte("-redacted")...),
			}, nil
		},
	}
	c := interceptor.NewChain()
	if _, err := c.RegisterExternal(interceptor.PhasePreDelegation, interceptor.ExternalConfig{
		Name:     "ext-modify",
		Priority: 200,
		Client:   startFakeInterceptor(t, srv),
	}); err != nil {
		t.Fatalf("RegisterExternal: %v", err)
	}
	// A built-in after the external interceptor must see the modified
	// payload — the §4.8 MODIFY result flows down the same chain.
	var seen []byte
	mustRegister(t, c, interceptor.PhasePreDelegation, &fakeInterceptor{
		name: "observer", priority: 300, builtin: true,
		fn: func(_ context.Context, req interceptor.Request) (interceptor.Result, error) {
			seen = append([]byte(nil), req.Content...)
			return interceptor.Result{Action: interceptor.ActionAllow}, nil
		},
	})

	res := c.Run(context.Background(), interceptor.Request{
		Phase:   interceptor.PhasePreDelegation,
		Content: []byte("secret"),
	})
	if res.Action != interceptor.ActionModify {
		t.Fatalf("action = %v, want MODIFY", res.Action)
	}
	if string(res.ModifiedContent) != "secret-redacted" {
		t.Errorf("modified content = %q, want secret-redacted", res.ModifiedContent)
	}
	if string(seen) != "secret-redacted" {
		t.Errorf("downstream interceptor saw %q, want the post-MODIFY payload", seen)
	}
}

func TestExternalFailClosedRejectsOnTransportError(t *testing.T) {
	srv := &fakeInterceptorServer{
		fn: func(*interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
			return nil, status.Error(14, "interceptor unavailable") // codes.Unavailable
		},
	}
	c := interceptor.NewChain()
	if _, err := c.RegisterExternal(interceptor.PhasePreDelegation, interceptor.ExternalConfig{
		Name:       "ext-down",
		FailPolicy: interceptor.FailClosed,
		Client:     startFakeInterceptor(t, srv),
	}); err != nil {
		t.Fatalf("RegisterExternal: %v", err)
	}

	res := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation})
	if res.Action != interceptor.ActionReject {
		t.Fatalf("action = %v, want REJECT — a fail-closed external interceptor rejects on transport error", res.Action)
	}
	if res.Code != interceptor.CodeInterceptorTimeout {
		t.Errorf("code = %q, want %q", res.Code, interceptor.CodeInterceptorTimeout)
	}
}

func TestExternalFailClosedIsTheDefault(t *testing.T) {
	srv := &fakeInterceptorServer{
		fn: func(*interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
			return nil, status.Error(14, "interceptor unavailable")
		},
	}
	// FailPolicy left unset — NewExternal must default it to fail-closed
	// so a degraded interceptor cannot silently bypass policy.
	c := interceptor.NewChain()
	if _, err := c.RegisterExternal(interceptor.PhasePreDelegation, interceptor.ExternalConfig{
		Name:   "ext-default",
		Client: startFakeInterceptor(t, srv),
	}); err != nil {
		t.Fatalf("RegisterExternal: %v", err)
	}

	res := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation})
	if res.Action != interceptor.ActionReject || res.Code != interceptor.CodeInterceptorTimeout {
		t.Errorf("result = %+v, want REJECT/INTERCEPTOR_TIMEOUT — fail-closed is the default", res)
	}
}

func TestExternalFailOpenSkipsOnTransportError(t *testing.T) {
	srv := &fakeInterceptorServer{
		fn: func(*interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
			return nil, status.Error(14, "interceptor unavailable")
		},
	}
	c := interceptor.NewChain()
	if _, err := c.RegisterExternal(interceptor.PhasePreDelegation, interceptor.ExternalConfig{
		Name:       "ext-down",
		Priority:   200,
		FailPolicy: interceptor.FailOpen,
		Client:     startFakeInterceptor(t, srv),
	}); err != nil {
		t.Fatalf("RegisterExternal: %v", err)
	}
	var calls []string
	mustRegister(t, c, interceptor.PhasePreDelegation,
		&fakeInterceptor{name: "after", priority: 300, builtin: true, calls: &calls})

	res := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation})
	if res.Action != interceptor.ActionAllow {
		t.Errorf("action = %v, want ALLOW — a fail-open external interceptor is skipped", res.Action)
	}
	if want := []string{"after"}; !equal(calls, want) {
		t.Errorf("calls = %v, want %v — the chain continues past a fail-open external error", calls, want)
	}
}

func TestExternalTimeoutFailClosed(t *testing.T) {
	srv := &fakeInterceptorServer{
		fn: func(*interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
			time.Sleep(200 * time.Millisecond)
			return &interceptorv1.InterceptResponse{Action: interceptorv1.InterceptResponse_ALLOW}, nil
		},
	}
	c := interceptor.NewChain()
	if _, err := c.RegisterExternal(interceptor.PhasePreDelegation, interceptor.ExternalConfig{
		Name:       "ext-slow",
		FailPolicy: interceptor.FailClosed,
		Timeout:    20 * time.Millisecond,
		Client:     startFakeInterceptor(t, srv),
	}); err != nil {
		t.Fatalf("RegisterExternal: %v", err)
	}

	res := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation})
	if res.Action != interceptor.ActionReject || res.Code != interceptor.CodeInterceptorTimeout {
		t.Errorf("result = %+v, want REJECT/INTERCEPTOR_TIMEOUT for a timed-out external interceptor", res)
	}
}

func TestRegisterExternalRejectsReservedPriority(t *testing.T) {
	c := interceptor.NewChain()
	_, err := c.RegisterExternal(interceptor.PhasePreDelegation, interceptor.ExternalConfig{
		Name:     "ext-low",
		Priority: interceptor.ReservedPriorityCeiling,
		Client:   startFakeInterceptor(t, &fakeInterceptorServer{}),
	})
	if !errors.Is(err, interceptor.ErrInvalidPriority) {
		t.Errorf("err = %v, want ErrInvalidPriority — an external interceptor may not register at the reserved ceiling", err)
	}
	if c.Len(interceptor.PhasePreDelegation) != 0 {
		t.Error("a rejected external interceptor must not be added to the chain")
	}
}

func TestRegisterExternalRejectsPreAuth(t *testing.T) {
	c := interceptor.NewChain()
	_, err := c.RegisterExternal(interceptor.PhasePreAuth, interceptor.ExternalConfig{
		Name:     "ext-preauth",
		Priority: 500,
		Client:   startFakeInterceptor(t, &fakeInterceptorServer{}),
	})
	if !errors.Is(err, interceptor.ErrInvalidPhase) {
		t.Errorf("err = %v, want ErrInvalidPhase — external interceptors may not register for PreAuth", err)
	}
}

func TestRegisterExternalDefaultsPriority(t *testing.T) {
	// A zero Priority must default to DefaultExternalPriority, which is
	// above the reserved ceiling, so registration succeeds.
	c := interceptor.NewChain()
	ic, err := c.RegisterExternal(interceptor.PhasePreDelegation, interceptor.ExternalConfig{
		Name:   "ext-default-prio",
		Client: startFakeInterceptor(t, &fakeInterceptorServer{}),
	})
	if err != nil {
		t.Fatalf("RegisterExternal with zero priority: %v", err)
	}
	if ic.Priority() != interceptor.DefaultExternalPriority {
		t.Errorf("priority = %d, want the default %d", ic.Priority(), interceptor.DefaultExternalPriority)
	}
}

func TestExternalRunsAfterBuiltinAtEqualPriority(t *testing.T) {
	srv := &fakeInterceptorServer{}
	c := interceptor.NewChain()
	var calls []string
	// Register the external first; the built-in must still run first at
	// equal priority.
	if _, err := c.RegisterExternal(interceptor.PhasePreDelegation, interceptor.ExternalConfig{
		Name:     "external",
		Priority: 500,
		Client:   startFakeInterceptor(t, srv),
	}); err != nil {
		t.Fatalf("RegisterExternal: %v", err)
	}
	mustRegister(t, c, interceptor.PhasePreDelegation, &fakeInterceptor{
		name: "builtin", priority: 500, builtin: true, calls: &calls,
	})

	c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation})
	// The built-in records itself; the external runs after it (the fake
	// server records lastReq). Both ran, built-in first.
	if want := []string{"builtin"}; !equal(calls, want) {
		t.Errorf("built-in calls = %v, want %v", calls, want)
	}
	if srv.lastReq == nil {
		t.Error("external interceptor did not run after the built-in")
	}
}

func TestNewExternalRequiresNameAndClient(t *testing.T) {
	if _, err := interceptor.NewExternal(interceptor.ExternalConfig{
		Client: startFakeInterceptor(t, &fakeInterceptorServer{}),
	}); err == nil {
		t.Error("NewExternal with no name should fail")
	}
	if _, err := interceptor.NewExternal(interceptor.ExternalConfig{Name: "x"}); err == nil {
		t.Error("NewExternal with no client should fail")
	}
}

func TestExternalRejectsUnknownAction(t *testing.T) {
	// A malformed Action in an otherwise-successful response is a protocol
	// error; a fail-closed interceptor rejects.
	srv := &fakeInterceptorServer{
		fn: func(*interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
			return &interceptorv1.InterceptResponse{Action: interceptorv1.InterceptResponse_Action(99)}, nil
		},
	}
	c := interceptor.NewChain()
	if _, err := c.RegisterExternal(interceptor.PhasePreDelegation, interceptor.ExternalConfig{
		Name:       "ext-bad-action",
		FailPolicy: interceptor.FailClosed,
		Client:     startFakeInterceptor(t, srv),
	}); err != nil {
		t.Fatalf("RegisterExternal: %v", err)
	}

	res := c.Run(context.Background(), interceptor.Request{Phase: interceptor.PhasePreDelegation})
	if res.Action != interceptor.ActionReject || res.Code != interceptor.CodeInterceptorTimeout {
		t.Errorf("result = %+v, want REJECT/INTERCEPTOR_TIMEOUT for an unknown remote action", res)
	}
}
