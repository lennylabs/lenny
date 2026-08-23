//go:build contract

// SPDX-License-Identifier: MIT

package adapter_jsonl_test

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// envelopeCapturingAdapter records the raw envelope the gateway's
// PodExecutor forwards over an Attach stream, so a contract case can read
// the key off the wire rather than off the producing struct. The gateway
// carries the address as a JSON struct tag, so a rename that misses the
// tag compiles and diverges here.
type envelopeCapturingAdapter struct {
	adapterv1.UnimplementedAdapterServer

	mu  sync.Mutex
	raw []byte
}

func (a *envelopeCapturingAdapter) Attach(stream grpc.BidiStreamingServer[adapterv1.AttachRequest, adapterv1.AttachResponse]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	for {
		req, err := stream.Recv()
		if err != nil {
			return nil //nolint:nilerr // EOF or cancel ends the capture cleanly.
		}
		if env := req.GetEnvelopeJson(); len(env) > 0 {
			a.mu.Lock()
			a.raw = append([]byte(nil), env...)
			a.mu.Unlock()
			_ = stream.Send(&adapterv1.AttachResponse{
				EnvelopeJson: []byte(`{"type":"response","output":[{"type":"text","inline":"ack"}]}`),
			})
		}
	}
}

func (a *envelopeCapturingAdapter) snapshot() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.raw
}

// dialEnvelopeCapturingAdapter serves rec over bufconn and returns a
// connected gateway adapter client.
func dialEnvelopeCapturingAdapter(t *testing.T, rec *envelopeCapturingAdapter) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, rec)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// spec: 28.5.3 (the outbound message envelope names the session it is
//
//	addressed to), 5.2 (the address is carried on every pod, whatever the
//	pool's concurrency), 15.4
//
// diagnosis: the key the gateway emits on the message envelope is not the
//
//	one the published JSON Lines schema declares, or the value is empty on
//	a bind that recorded no separate slot. Either leaves every runtime on
//	an exclusive pool reading no address, and every frame it emits in
//	response unaddressed. The envelope carries the key as a struct tag, so
//	no compiler catches it and the check has to read the wire. That the
//	published schema requires the property on this frame is pinned
//	separately by TestAdapterPopulatedFramesRequireSessionAddress.
func TestGatewayEnvelopeCarriesTheAddressedSession_spec_28_5_3(t *testing.T) {
	for _, recordedSlot := range []string{"", "slot_02"} {
		rec := &envelopeCapturingAdapter{}
		cl := dialEnvelopeCapturingAdapter(t, rec)
		reg := podsession.NewRegistry()
		reg.Put(&podsession.BindResult{
			SessionID: "sess-envelope", TenantID: "acme", Adapter: cl, SlotID: recordedSlot,
		})
		pe := executor.NewPodExecutor(reg, nil)
		if _, err := pe.Send(context.Background(), "sess-envelope", []executor.Message{
			{Role: "user", Content: "hello"},
		}); err != nil {
			t.Fatalf("Send with recorded slot %q: %v", recordedSlot, err)
		}
		raw := rec.snapshot()
		if len(raw) == 0 {
			t.Fatalf("no envelope reached the adapter for recorded slot %q", recordedSlot)
		}
		var probe map[string]any
		if err := json.Unmarshal(raw, &probe); err != nil {
			t.Fatalf("envelope is not JSON: %v (%s)", err, raw)
		}
		if probe["sessionId"] != "sess-envelope" {
			t.Errorf("envelope with recorded slot %q carries sessionId %v, want sess-envelope: %s",
				recordedSlot, probe["sessionId"], raw)
		}
	}
}
