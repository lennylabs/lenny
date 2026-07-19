//go:build contract

// SPDX-License-Identifier: MIT

// Package checkpoint_stream_test is the Tier 3 contract suite for the
// bidirectional Checkpoint RPC on the gateway ↔ pod adapter gRPC contract
// (schemas/lenny-adapter.proto). The gateway is the gRPC client: it opens
// the stream with a CheckpointStart carrying the gateway-minted
// checkpoint_id and typed trigger, the adapter reports its on-disk
// workspace size (CheckpointProbe) so the gateway reserves storage quota,
// and then the two parties run the ChunkReady → CheckpointGrant → PUT →
// ChunkCommitted loop before a terminal CheckpointSummary (or a
// CheckpointFailed / CheckpointAbort). This suite drives the compiled
// gateway and adapter over an in-memory gRPC transport so it verifies the
// wire contract the running binaries exchange rather than the .proto text,
// and it pins the protocol invariants §10.1 mandates: the frame order, the
// out-of-range chunk rejection, the length-versus-bytes reconciliation, the
// verbatim replay of every signed SigV4 header, and the typed-trigger
// round-trip.
//
// The companion adapter_checkpointbarrier suite pins the
// CheckpointBarrierResponse ack message; this suite pins the streaming
// upload contract that ack accompanies.
package checkpoint_stream_test

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// ---------------------------------------------------------------------------
// Descriptor pins: the message set, the oneof arms, and the trigger enum.
// A proto renumber or rename that survives a coordinated proto+regeneration
// edit is still caught here.
// ---------------------------------------------------------------------------

// wantField is one expected field on a message: proto name and number.
type wantField struct {
	name   string
	number protoreflect.FieldNumber
}

func assertOneof(t *testing.T, md protoreflect.MessageDescriptor, oneof string, want []wantField) {
	t.Helper()
	od := md.Oneofs().ByName(protoreflect.Name(oneof))
	if od == nil {
		t.Fatalf("%s: oneof %q missing", md.Name(), oneof)
	}
	if got := od.Fields().Len(); got != len(want) {
		t.Fatalf("%s.%s has %d arms, want %d", md.Name(), oneof, got, len(want))
	}
	for _, w := range want {
		f := md.Fields().ByName(protoreflect.Name(w.name))
		if f == nil {
			t.Errorf("%s.%s: arm %q missing", md.Name(), oneof, w.name)
			continue
		}
		if f.Number() != w.number {
			t.Errorf("%s.%s: arm %q number %d, want %d", md.Name(), oneof, w.name, f.Number(), w.number)
		}
	}
}

func assertFields(t *testing.T, md protoreflect.MessageDescriptor, want []wantField) {
	t.Helper()
	for _, w := range want {
		f := md.Fields().ByName(protoreflect.Name(w.name))
		if f == nil {
			t.Errorf("%s: field %q missing", md.Name(), w.name)
			continue
		}
		if f.Number() != w.number {
			t.Errorf("%s: field %q number %d, want %d", md.Name(), w.name, f.Number(), w.number)
		}
	}
}

// TestCheckpointStreamMessageContract pins the streaming Checkpoint frame
// set: the client/server oneof arms, the fields the protocol reads, and
// that CheckpointGrant.headers is the SigV4 header map. The compiled gateway
// and adapter select these arms by wire number, so a renumber breaks binary
// compatibility silently without this pin.
//
// spec: 10.1 lines 130-132, 11.2 line 35, 4.7, 5.2 (per-slot addressing)
// diagnosis: The streaming Checkpoint wire contract diverged from the
// gateway-driven grant/confirm protocol. An arm was renumbered, renamed, or
// dropped, CheckpointGrant.headers stopped being a map, or CheckpointStart
// lost its slot_id per-slot routing field, which silently breaks the
// gateway/adapter frame exchange. Re-edit schemas/lenny-adapter.proto and
// run `make generate-proto`.
func TestCheckpointStreamMessageContract(t *testing.T) {
	client := (&adapterv1.CheckpointRequest{}).ProtoReflect().Descriptor()
	assertOneof(t, client, "msg", []wantField{
		{name: "start", number: 1},
		{name: "grant", number: 2},
		{name: "abort", number: 3},
	})

	server := (&adapterv1.CheckpointResponse{}).ProtoReflect().Descriptor()
	assertOneof(t, server, "msg", []wantField{
		{name: "probe", number: 1},
		{name: "chunk_ready", number: 2},
		{name: "chunk_committed", number: 3},
		{name: "summary", number: 4},
		{name: "failed", number: 5},
	})

	startMD := (&adapterv1.CheckpointStart{}).ProtoReflect().Descriptor()
	assertFields(t, startMD, []wantField{
		{name: "checkpoint_id", number: 1},
		{name: "trigger", number: 2},
		{name: "chunk_size_bytes", number: 3},
		{name: "chunk_encoding", number: 4},
		{name: "deadline_ms", number: 5},
		{name: "slot_id", number: 6},
	})
	// assertFields iterates only its want slice and never asserts the total
	// field count, so a coordinated proto+regen renumber of the slot-routing
	// field would pass unpinned. Assert CheckpointStart has exactly six fields
	// and that slot_id is a SlotId message field, so the gateway-to-adapter
	// slot addressing cannot silently break.
	if got := startMD.Fields().Len(); got != 6 {
		t.Errorf("CheckpointStart has %d fields, want 6", got)
	}
	if sf := startMD.Fields().ByName("slot_id"); sf == nil {
		t.Error("CheckpointStart.slot_id field missing")
	} else if sf.Kind() != protoreflect.MessageKind || sf.Message().FullName() != (&adapterv1.SlotId{}).ProtoReflect().Descriptor().FullName() {
		t.Errorf("CheckpointStart.slot_id must be a SlotId message field, got kind %v message %v", sf.Kind(), sf.Message())
	}
	assertFields(t, (&adapterv1.ChunkReady{}).ProtoReflect().Descriptor(), []wantField{
		{name: "index", number: 1},
		{name: "length", number: 2},
	})

	grant := (&adapterv1.CheckpointGrant{}).ProtoReflect().Descriptor()
	assertFields(t, grant, []wantField{
		{name: "index", number: 1},
		{name: "url", number: 2},
		{name: "content_length", number: 3},
		{name: "headers", number: 4},
		{name: "expires_at", number: 5},
	})
	if hf := grant.Fields().ByName("headers"); hf == nil || !hf.IsMap() {
		t.Error("CheckpointGrant.headers must be a map<string,string> so the adapter replays every signed SigV4 header verbatim")
	}
}

// TestCheckpointStartTriggerRoundTrips pins the CheckpointStart.trigger enum
// round-trip: the gateway maps a typed §4.4 trigger onto the wire enum and
// the adapter maps it back to the same typed value it selects its retry
// budget from. A drift in either direction (a renumbered enum value, a
// dropped case) would deliver the adapter a different trigger than the
// gateway intended.
//
// spec: 10.1 lines 130-132, 11.2 line 35, 4.7
// diagnosis: The CheckpointStart.trigger enum no longer round-trips through
// checkpoint.Trigger. The gateway's duration-histogram label and the
// adapter's retry-budget selection would key on different triggers. Re-check
// pkg/checkpoint Trigger.Proto / TriggerFromProto against the proto enum.
func TestCheckpointStartTriggerRoundTrips(t *testing.T) {
	cases := []struct {
		trigger checkpoint.Trigger
		wire    adapterv1.CheckpointTrigger
	}{
		{checkpoint.TriggerPeriodic, adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PERIODIC},
		{checkpoint.TriggerPreScaleDown, adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_PRE_SCALE_DOWN},
		{checkpoint.TriggerEviction, adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_EVICTION},
	}
	for _, tc := range cases {
		if got := tc.trigger.Proto(); got != tc.wire {
			t.Errorf("%s.Proto() = %v, want %v", tc.trigger, got, tc.wire)
		}
		if got := checkpoint.TriggerFromProto(tc.wire); got != tc.trigger {
			t.Errorf("TriggerFromProto(%v) = %q, want %q", tc.wire, got, tc.trigger)
		}
	}
	// The unspecified wire value is not a valid §4.4 trigger; the adapter
	// must not decode it to a typed trigger.
	if got := checkpoint.TriggerFromProto(adapterv1.CheckpointTrigger_CHECKPOINT_TRIGGER_UNSPECIFIED); got.IsValid() {
		t.Errorf("TriggerFromProto(UNSPECIFIED) = %q, want an invalid trigger", got)
	}
}

// ---------------------------------------------------------------------------
// Part A: the real adapter server driven by a scripted gateway client. Pins
// the server-side frame order, the no-PUT-before-grant rule, the verbatim
// SigV4 header replay, and that a dropped signed header fails at the store.
// ---------------------------------------------------------------------------

// recordingTransport captures every checkpoint PUT the adapter issues so a
// test can assert the adapter replayed the signed header set verbatim and
// uploaded after (never before) it received the grant. requireHeaders, when
// set, models the object store's SigV4 check: a PUT missing a required
// header is rejected with 403 SignatureDoesNotMatch, the same failure a
// dropped signed header produces against a real backend.
type recordingTransport struct {
	mu             sync.Mutex
	puts           []recordedPut
	requireHeaders map[string]string
}

type recordedPut struct {
	headers map[string]string
	body    []byte
}

func (r *recordingTransport) PutChunk(_ context.Context, _ string, headers map[string]string, _ int64, body io.Reader) (int, string, error) {
	b, err := io.ReadAll(body)
	if err != nil {
		return 0, "", err
	}
	hcopy := make(map[string]string, len(headers))
	for k, v := range headers {
		hcopy[k] = v
	}
	r.mu.Lock()
	r.puts = append(r.puts, recordedPut{headers: hcopy, body: b})
	require := r.requireHeaders
	r.mu.Unlock()
	for k, v := range require {
		if hcopy[k] != v {
			return 403, "SignatureDoesNotMatch", nil
		}
	}
	return 200, "", nil
}

func (r *recordingTransport) GetChunk(context.Context, string, map[string]string) (io.ReadCloser, error) {
	return nil, io.EOF
}

func (r *recordingTransport) putCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.puts)
}

// realAdapterClient serves a real adapter Server with the given transport
// and a seeded one-file workspace over bufconn, returning a connected
// Adapter client.
func realAdapterClient(t *testing.T, transport adapter.CheckpointTransport) adapterv1.AdapterClient {
	t.Helper()
	s := adapter.New("checkpoint-stream-contract")
	s.WorkspaceRoot = t.TempDir()
	s.StagingDir = t.TempDir()
	s.CheckpointTransport = transport
	if err := os.WriteFile(filepath.Join(s.WorkspaceRoot, "state.txt"), []byte("agent workspace state"), 0o644); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return adapterv1.NewAdapterClient(conn)
}

// t4SignedHeaders is the SigV4 header set the gateway folds into a T4-tenant
// chunk grant on an SSE-KMS backend. The adapter must replay every entry.
var t4SignedHeaders = map[string]string{
	"x-amz-server-side-encryption":                "aws:kms",
	"x-amz-server-side-encryption-aws-kms-key-id": "arn:aws:kms:us-east-1:111122223333:key/acme",
}

// TestCheckpointStreamFrameOrderAndHeaderReplay drives the real adapter with
// a scripted gateway that answers each ChunkReady with a T4 SigV4 grant. It
// pins that the adapter sends its Probe before any ChunkReady, issues no PUT
// before the gateway grants, replays every signed header byte-for-byte on
// the PUT, and closes with Summary as the terminal frame.
//
// spec: 10.1 lines 130-132, 11.2 line 35, 4.7
// diagnosis: The adapter side of the Checkpoint stream broke a §10.1
// protocol invariant: it emitted a ChunkReady before the Probe, PUT a chunk
// before receiving its grant, dropped a signed SigV4 header on the PUT (which
// a real store rejects with SignatureDoesNotMatch), or sent a frame after the
// Summary. Re-check pkg/adapter Checkpoint / streamChunks.
func TestCheckpointStreamFrameOrderAndHeaderReplay(t *testing.T) {
	transport := &recordingTransport{}
	client := realAdapterClient(t, transport)

	stream, err := client.Checkpoint(context.Background())
	if err != nil {
		t.Fatalf("open Checkpoint stream: %v", err)
	}
	// The gateway opens with a non-default trigger so the wire path carries
	// a typed trigger the adapter decodes.
	if err := stream.Send(&adapterv1.CheckpointRequest{
		Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
			CheckpointId:   "gw-ckpt-order",
			Trigger:        checkpoint.TriggerPreScaleDown.Proto(),
			ChunkSizeBytes: 1 << 20,
			ChunkEncoding:  "tar",
		}},
	}); err != nil {
		t.Fatalf("send start: %v", err)
	}

	var order []string
	var sawProbe, sawSummary bool
	var readyCount int
loop:
	for {
		msg, rerr := stream.Recv()
		if rerr != nil {
			t.Fatalf("stream.Recv: %v", rerr)
		}
		if sawSummary {
			t.Fatalf("received a %T frame after the Summary; Summary must be the terminal frame", msg.GetMsg())
		}
		switch m := msg.GetMsg().(type) {
		case *adapterv1.CheckpointResponse_Probe:
			order = append(order, "probe")
			sawProbe = true
		case *adapterv1.CheckpointResponse_ChunkReady:
			order = append(order, "chunk_ready")
			if !sawProbe {
				t.Fatal("ChunkReady arrived before the Probe; the workspace-size probe is the first frame")
			}
			// No PUT may have happened before the gateway grants: at the
			// instant a chunk is declared, the adapter is blocked awaiting the
			// grant, so it can have issued no PUT for it yet.
			if readyCount == 0 && transport.putCount() != 0 {
				t.Fatalf("the adapter PUT a chunk before receiving any grant (%d puts); no capability is used before the gateway signs it", transport.putCount())
			}
			readyCount++
			if err := stream.Send(&adapterv1.CheckpointRequest{
				Msg: &adapterv1.CheckpointRequest_Grant{Grant: &adapterv1.CheckpointGrant{
					Index:         m.ChunkReady.GetIndex(),
					Url:           "https://objectstore.example/chunk",
					ContentLength: m.ChunkReady.GetLength(),
					Headers:       t4SignedHeaders,
				}},
			}); err != nil {
				t.Fatalf("send grant: %v", err)
			}
		case *adapterv1.CheckpointResponse_ChunkCommitted:
			order = append(order, "chunk_committed")
		case *adapterv1.CheckpointResponse_Summary:
			order = append(order, "summary")
			sawSummary = true
			break loop
		case *adapterv1.CheckpointResponse_Failed:
			t.Fatalf("unexpected Failed frame: %+v", m.Failed)
		}
	}

	if !sawSummary {
		t.Fatal("stream closed without a Summary")
	}
	if order[0] != "probe" || order[len(order)-1] != "summary" {
		t.Fatalf("frame order = %v, want probe first and summary last", order)
	}
	if readyCount == 0 {
		t.Fatal("no chunk was declared; the seeded workspace must produce at least one chunk")
	}

	// Every PUT replayed the signed SigV4 header set byte-for-byte.
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.puts) == 0 {
		t.Fatal("no chunk was PUT to the object store")
	}
	for i, p := range transport.puts {
		if !reflect.DeepEqual(p.headers, t4SignedHeaders) {
			t.Fatalf("PUT %d headers = %v, want the signed set replayed verbatim %v", i, p.headers, t4SignedHeaders)
		}
	}
}

// TestCheckpointStreamDroppedSignedHeaderFails pins that a signed header the
// grant omits is rejected at the store: the adapter replays exactly the
// grant's header set, and a store that requires the SSE-KMS header the
// gateway dropped answers 403 SignatureDoesNotMatch, which the adapter
// surfaces as a terminal CheckpointFailed carrying that HTTP status and error
// code. This is the failure a dropped SigV4 header produces on a T4 backend.
//
// spec: 10.1 lines 130-132, 11.2 line 35, 4.7
// diagnosis: A checkpoint PUT missing a signed SigV4 header did not surface
// the store's SignatureDoesNotMatch through a CheckpointFailed frame, so the
// gateway cannot classify the rejection. Re-check pkg/adapter uploadChunk /
// failChunk.
func TestCheckpointStreamDroppedSignedHeaderFails(t *testing.T) {
	// The store requires the SSE-KMS header set. The scripted gateway signs a
	// grant that drops it, standing in for a T4 grant that failed to fold the
	// classification header into the signature.
	transport := &recordingTransport{requireHeaders: t4SignedHeaders}
	client := realAdapterClient(t, transport)

	stream, err := client.Checkpoint(context.Background())
	if err != nil {
		t.Fatalf("open Checkpoint stream: %v", err)
	}
	if err := stream.Send(&adapterv1.CheckpointRequest{
		Msg: &adapterv1.CheckpointRequest_Start{Start: &adapterv1.CheckpointStart{
			CheckpointId:   "gw-ckpt-drophdr",
			Trigger:        checkpoint.TriggerPeriodic.Proto(),
			ChunkSizeBytes: 1 << 20,
		}},
	}); err != nil {
		t.Fatalf("send start: %v", err)
	}

	var failed *adapterv1.CheckpointFailed
	for failed == nil {
		msg, rerr := stream.Recv()
		if rerr != nil {
			t.Fatalf("stream.Recv: %v", rerr)
		}
		switch m := msg.GetMsg().(type) {
		case *adapterv1.CheckpointResponse_ChunkReady:
			// Sign a grant that drops the required SigV4 headers entirely.
			if err := stream.Send(&adapterv1.CheckpointRequest{
				Msg: &adapterv1.CheckpointRequest_Grant{Grant: &adapterv1.CheckpointGrant{
					Index:         m.ChunkReady.GetIndex(),
					Url:           "https://objectstore.example/chunk",
					ContentLength: m.ChunkReady.GetLength(),
					Headers:       nil,
				}},
			}); err != nil {
				t.Fatalf("send grant: %v", err)
			}
		case *adapterv1.CheckpointResponse_Summary:
			t.Fatal("stream summarised despite the store rejecting the header-stripped PUT")
		case *adapterv1.CheckpointResponse_Failed:
			failed = m.Failed
		}
	}
	if failed.GetHttpStatus() != 403 {
		t.Errorf("failed http_status = %d, want 403", failed.GetHttpStatus())
	}
	if failed.GetErrorCode() != "SignatureDoesNotMatch" {
		t.Errorf("failed error_code = %q, want SignatureDoesNotMatch", failed.GetErrorCode())
	}
}

// ---------------------------------------------------------------------------
// Part B: the real gateway upload driver against a scripted adapter. Pins the
// gateway-side out-of-range chunk rejection (before any capability is signed
// and before any quota is consumed) and the length-versus-bytes
// reconciliation abort.
// ---------------------------------------------------------------------------

// scriptChunk is one chunk a scripted adapter declares: the length it
// reports in ChunkReady and the number of bytes it actually "PUTs" into the
// store (defaulting to the declared length).
type scriptChunk struct {
	declaredLen int64
	putBytes    int64
}

// scriptedAdapter is a §10.1 chunked producer double that drives the gateway
// upload driver with a chosen probe and a chosen chunk script, and records
// the terminal CheckpointAbort the gateway sends. When expectAbortAfterCommit
// is set it reads one frame after the final ChunkCommitted so a gateway abort
// raised by the asynchronous confirm is observed on the wire.
type scriptedAdapter struct {
	adapterv1.UnimplementedAdapterServer
	probeBytes             int64
	chunks                 []scriptChunk
	expectAbortAfterCommit bool
	presigner              *recordingPresigner
	store                  *blobstore.MemoryStore

	mu             sync.Mutex
	checkpointID   string
	receivedGrants []*adapterv1.CheckpointGrant
}

// grantsReceived returns a copy of every CheckpointGrant the scripted adapter
// received from the gateway, so a test can assert the wire fields the gateway
// populated (such as the capability expiry).
func (a *scriptedAdapter) grantsReceived() []*adapterv1.CheckpointGrant {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*adapterv1.CheckpointGrant, len(a.receivedGrants))
	copy(out, a.receivedGrants)
	return out
}

// mintedCheckpointID returns the gateway-minted checkpoint_id the adapter
// received in CheckpointStart, so a test can read the durable manifest the
// gateway finalised for the attempt. The gateway signals its abort both by
// sending a CheckpointAbort and by cancelling the stream, and the cancel can
// tear the stream down before the frame flushes, so the manifest_reason the
// gateway durably finalises is the authoritative abort outcome rather than
// the best-effort CheckpointAbort frame.
func (a *scriptedAdapter) mintedCheckpointID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.checkpointID
}

func (a *scriptedAdapter) Checkpoint(stream grpc.BidiStreamingServer[adapterv1.CheckpointRequest, adapterv1.CheckpointResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.checkpointID = first.GetStart().GetCheckpointId()
	a.mu.Unlock()
	if err := stream.Send(&adapterv1.CheckpointResponse{
		Msg: &adapterv1.CheckpointResponse_Probe{Probe: &adapterv1.CheckpointProbe{WorkspaceBytes: a.probeBytes}},
	}); err != nil {
		return err
	}
	var total int64
	for i, ch := range a.chunks {
		if err := stream.Send(&adapterv1.CheckpointResponse{
			Msg: &adapterv1.CheckpointResponse_ChunkReady{ChunkReady: &adapterv1.ChunkReady{Index: uint32(i), Length: ch.declaredLen}},
		}); err != nil {
			return err
		}
		msg, rerr := stream.Recv()
		if rerr != nil {
			return rerr
		}
		if msg.GetAbort() != nil {
			return nil
		}
		grant := msg.GetGrant()
		if grant == nil {
			return status.Errorf(codes.Internal, "expected a grant for chunk %d", i)
		}
		a.mu.Lock()
		a.receivedGrants = append(a.receivedGrants, grant)
		a.mu.Unlock()
		wrote := ch.putBytes
		if wrote == 0 {
			wrote = ch.declaredLen
		}
		a.putObject(grant, wrote)
		total += wrote
		if err := stream.Send(&adapterv1.CheckpointResponse{
			Msg: &adapterv1.CheckpointResponse_ChunkCommitted{ChunkCommitted: &adapterv1.ChunkCommitted{Index: uint32(i)}},
		}); err != nil {
			return err
		}
	}
	if a.expectAbortAfterCommit {
		// The gateway's asynchronous chunk confirm aborts after the commit;
		// draining one more frame lets it deliver the abort and tear the
		// stream down before the adapter returns.
		_, _ = stream.Recv()
		return nil
	}
	return stream.Send(&adapterv1.CheckpointResponse{
		Msg: &adapterv1.CheckpointResponse_Summary{Summary: &adapterv1.CheckpointSummary{
			ChunkCount: uint32(len(a.chunks)), TotalBytes: total,
		}},
	})
}

// putObject writes size bytes at the grant's key so the gateway's Stat
// confirm observes the byte count the adapter actually uploaded.
func (a *scriptedAdapter) putObject(grant *adapterv1.CheckpointGrant, size int64) {
	if a.store == nil {
		return
	}
	uri, ok := a.presigner.lookup(grant.GetUrl())
	if !ok {
		return
	}
	_, _ = a.store.Put(uri, "application/octet-stream", strings.NewReader(strings.Repeat("x", int(size))))
}

// recordingPresigner mints grant URLs, records the URI each maps to so the
// scripted adapter writes to the same key the gateway Stats, and counts the
// PresignPut calls so a test asserts no capability was signed.
type recordingPresigner struct {
	mu       sync.Mutex
	uris     map[string]blobstore.URI
	expiries map[string]time.Time
	calls    int
}

func newRecordingPresigner() *recordingPresigner {
	return &recordingPresigner{uris: map[string]blobstore.URI{}, expiries: map[string]time.Time{}}
}

func (p *recordingPresigner) PresignPut(u blobstore.URI, contentLength int64, ttl time.Duration) (blobstore.Grant, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	url := u.SessionID + "/" + u.PartID
	p.uris[url] = u
	exp := time.Now().Add(ttl)
	p.expiries[url] = exp
	return blobstore.Grant{URL: url, ExpiresAt: exp}, nil
}

func (p *recordingPresigner) expiryFor(url string) (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	exp, ok := p.expiries[url]
	return exp, ok
}

func (p *recordingPresigner) PresignGet(blobstore.URI, time.Duration) (blobstore.Grant, error) {
	return blobstore.Grant{URL: "get"}, nil
}

func (p *recordingPresigner) lookup(url string) (blobstore.URI, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	u, ok := p.uris[url]
	return u, ok
}

func (p *recordingPresigner) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// gatewayHarness wires a real Checkpointer against the scripted adapter over
// bufconn, with in-memory manifest, quota, and object stores.
type gatewayHarness struct {
	cp        *checkpointer.Checkpointer
	presigner *recordingPresigner
	quota     *storagequota.Memory
	manifests *partialmanifeststore.MemoryStore
}

const (
	harnessTenant  = "acme"
	harnessSession = "s-contract"
	harnessLimit   = 1 << 30
	harnessChunk   = 1024
)

func newGatewayHarness(t *testing.T, sa *scriptedAdapter) *gatewayHarness {
	t.Helper()
	store := blobstore.NewMemoryStore(time.Now)
	presigner := newRecordingPresigner()
	sa.store = store
	sa.presigner = presigner

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, sa)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })

	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: harnessSession, TenantID: harnessTenant, Adapter: cl})
	sessions := memstore.New()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: harnessSession, TenantID: harnessTenant, State: session.StateRunning, RuntimeRef: "echo",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	quota := storagequota.NewMemory()
	manifests := partialmanifeststore.NewMemoryStore(nil)
	cp := &checkpointer.Checkpointer{
		Sessions:       sessions,
		Registry:       registry,
		Manifests:      manifests,
		Quota:          quota,
		QuotaLimitFor:  func(context.Context, string) (int64, error) { return harnessLimit, nil },
		Presigner:      presigner,
		ObjectStore:    store,
		ChunkSizeBytes: harnessChunk,
		Deadline:       5 * time.Second,
	}
	return &gatewayHarness{cp: cp, presigner: presigner, quota: quota, manifests: manifests}
}

// TestCheckpointStreamRejectsOutOfRangeChunk pins that a ChunkReady whose
// declared length is zero, negative, or greater than the chunk_size_bytes the
// gateway sent in Start aborts the attempt with stream_truncated before any
// capability is signed and before any chunk quota is consumed.
//
// spec: 10.1 lines 130-132, 11.2 line 35, 4.7
// diagnosis: The gateway upload driver signed a capability for, or ran quota
// arithmetic against, an out-of-range chunk declaration instead of aborting
// with stream_truncated first. A pod could then present a chunk larger than
// the gateway's own chunk_size_bytes and have it signed. Re-check
// pkg/gateway/checkpoint/checkpointer onChunkReady.
func TestCheckpointStreamRejectsOutOfRangeChunk(t *testing.T) {
	cases := []struct {
		name        string
		declaredLen int64
	}{
		{"zero", 0},
		{"negative", -1},
		{"over_chunk_size", harnessChunk + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sa := &scriptedAdapter{
				probeBytes: 4096,
				chunks:     []scriptChunk{{declaredLen: tc.declaredLen}},
			}
			h := newGatewayHarness(t, sa)

			err := h.cp.CheckpointWithTrigger(context.Background(), harnessTenant, harnessSession, checkpoint.TriggerPeriodic)
			if err == nil {
				t.Fatal("checkpoint reported success on an out-of-range chunk declaration")
			}
			// The gateway durably finalises the intent row stream_truncated.
			rec, gerr := h.manifests.Get(context.Background(), harnessTenant, sa.mintedCheckpointID())
			if gerr != nil {
				t.Fatalf("get manifest for the aborted attempt: %v", gerr)
			}
			if rec.ManifestReason != partialmanifeststore.ReasonStreamTruncated {
				t.Errorf("manifest_reason = %q, want %q", rec.ManifestReason, partialmanifeststore.ReasonStreamTruncated)
			}
			// No capability was signed before the rejection.
			if got := h.presigner.callCount(); got != 0 {
				t.Errorf("PresignPut called %d times; an out-of-range chunk must be rejected before any capability is signed", got)
			}
			// The probe reservation was released on the abort arm, so the
			// tenant counter is not left carrying the rejected attempt's quota.
			used, uerr := h.quota.Used(context.Background(), harnessTenant)
			if uerr != nil {
				t.Fatalf("quota Used: %v", uerr)
			}
			if used != 0 {
				t.Errorf("tenant quota counter = %d after the rejected attempt, want 0 (reservation released)", used)
			}
		})
	}
}

// TestCheckpointStreamAbortsOnLengthMismatch pins that a ChunkReady whose
// declared length does not match the bytes the adapter actually PUTs is
// aborted: the gateway signs the grant at the declared length, Stat-confirms
// a larger object, and aborts the attempt rather than finalising a manifest
// whose byte accounting the pod inflated past its signed capability.
//
// spec: 10.1 lines 130-132, 11.2 line 35, 4.7
// diagnosis: The gateway upload driver accepted a committed chunk whose
// Stat-confirmed size exceeded the Content-Length it signed, so a pod could
// write more bytes than its capability granted without aborting. Re-check
// pkg/gateway/checkpoint/checkpointer confirmChunk.
func TestCheckpointStreamAbortsOnLengthMismatch(t *testing.T) {
	sa := &scriptedAdapter{
		probeBytes:             4096,
		chunks:                 []scriptChunk{{declaredLen: 100, putBytes: 150}},
		expectAbortAfterCommit: true,
	}
	h := newGatewayHarness(t, sa)

	err := h.cp.CheckpointWithTrigger(context.Background(), harnessTenant, harnessSession, checkpoint.TriggerPeriodic)
	if err == nil {
		t.Fatal("checkpoint reported success though the PUT wrote more bytes than the declared length")
	}
	// The over-size confirm aborts the attempt: the gateway finalises the
	// intent row quota_exceeded rather than completing it.
	rec, gerr := h.manifests.Get(context.Background(), harnessTenant, sa.mintedCheckpointID())
	if gerr != nil {
		t.Fatalf("get manifest for the aborted attempt: %v", gerr)
	}
	if rec.ManifestReason != partialmanifeststore.ReasonQuotaExceeded {
		t.Errorf("manifest_reason = %q, want %q", rec.ManifestReason, partialmanifeststore.ReasonQuotaExceeded)
	}
}

// TestCheckpointStreamGrantCarriesCapabilityExpiry pins that the gateway
// stamps CheckpointGrant.expires_at with the presigned capability's expiry on
// every minted grant. The adapter reads this window to re-mint a grant for a
// PUT retry that outlives its signature instead of replaying a dead URL; a
// grant sent without expires_at leaves the adapter's grant-expiry recovery
// path dead, so a slow-object-store retry hits an expired-signature 403 rather
// than requesting a fresh grant.
//
// spec: 4.4 lines 261-264 (grant re-mint on expiry), 13.2 (capability expiry)
// diagnosis: The gateway upload driver minted a CheckpointGrant without
// expires_at, so the adapter cannot tell when the presigned PUT capability has
// expired and never re-mints. A checkpoint PUT retried past the capability TTL
// replays a dead signature and fails instead of recovering. Re-check
// pkg/gateway/checkpoint/checkpointer mintGrant.
func TestCheckpointStreamGrantCarriesCapabilityExpiry(t *testing.T) {
	sa := &scriptedAdapter{
		probeBytes: 4096,
		chunks:     []scriptChunk{{declaredLen: 128}, {declaredLen: 64}},
	}
	h := newGatewayHarness(t, sa)
	// A short, explicit TTL so the wire expiry is bounded and the assertion
	// does not depend on the default window.
	h.cp.CapabilityTTL = 15 * time.Second

	if err := h.cp.CheckpointWithTrigger(context.Background(), harnessTenant, harnessSession, checkpoint.TriggerPeriodic); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	grants := sa.grantsReceived()
	if len(grants) != len(sa.chunks) {
		t.Fatalf("grants received = %d, want %d (one per declared chunk)", len(grants), len(sa.chunks))
	}
	for _, g := range grants {
		ts := g.GetExpiresAt()
		if ts == nil {
			t.Fatalf("chunk %d grant carries no expires_at; the adapter's grant-expiry re-mint path is dead without it", g.GetIndex())
		}
		want, ok := h.presigner.expiryFor(g.GetUrl())
		if !ok {
			t.Fatalf("chunk %d grant URL %q was never minted by the presigner", g.GetIndex(), g.GetUrl())
		}
		if got := ts.AsTime(); !got.Equal(want) {
			t.Errorf("chunk %d grant expires_at = %s, want the signed capability expiry %s", g.GetIndex(), got, want)
		}
	}
}
