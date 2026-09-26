// SPDX-License-Identifier: MIT

// Package bindattempt is the shared fixture and case battery for the §4.7.1
// bind attempt contract: the numbered admission rules, the numbered Shutdown
// rules, the stamp-once rule, and the registry critical section. The battery
// runs twice, once over the real gRPC transport in the Tier 3 contract suite
// and once in process in the Tier 10 conformance battery, so each rule is
// written here once and each tier supplies only the transport.
//
// Every assertion reads a result a caller can observe: the status and the
// adapterv1.Error detail a request is answered with, the reclaim outcome a
// Shutdown reports, the per-slot tree on disk, the fake runtime's calls, and
// the session scrub reports the adapter files. None reads the adapter's
// unexported registry, because §15.4 publishes the rules for a third-party
// adapter and the battery states them in the terms such an adapter is judged
// by. Registry state is read back through probe Shutdown outcomes, which a
// caller of any conforming adapter can observe.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
package bindattempt

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	"github.com/lennylabs/lenny/pkg/adapter/slotlayout"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// Pod is the adapter surface the battery drives: the requests §4.7.1's
// admission rules govern and the Shutdown its teardown rules govern.
// PrepareWorkspace takes the frames of one client-streaming call.
type Pod interface {
	PrepareWorkspace(ctx context.Context, frames ...*adapterv1.PrepareWorkspaceRequest) (*adapterv1.PrepareWorkspaceResponse, error)
	FinalizeWorkspace(ctx context.Context, req *adapterv1.FinalizeWorkspaceRequest) (*adapterv1.FinalizeWorkspaceResponse, error)
	RunSetup(ctx context.Context, req *adapterv1.RunSetupRequest) (*adapterv1.RunSetupResponse, error)
	AssignCredentials(ctx context.Context, req *adapterv1.AssignCredentialsRequest) (*adapterv1.AssignCredentialsResponse, error)
	StartSession(ctx context.Context, req *adapterv1.StartSessionRequest) (*adapterv1.StartSessionResponse, error)
	Resume(ctx context.Context, req *adapterv1.ResumeRequest) (*adapterv1.ResumeResponse, error)
	ConfigureWorkspace(ctx context.Context, req *adapterv1.ConfigureWorkspaceRequest) (*adapterv1.ConfigureWorkspaceResponse, error)
	Shutdown(ctx context.Context, req *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error)
}

// Transport connects the battery to an adapter Server. It is called once per
// fixture and returns a dialer; each dialer call opens a further client, so a
// case that needs two callers at one slot identifier holds two of them.
type Transport func(t *testing.T, s *adapter.Server) func(t *testing.T) Pod

// InProcess drives the exported Server's handlers directly.
func InProcess(_ *testing.T, s *adapter.Server) func(t *testing.T) Pod {
	return func(*testing.T) Pod { return inProcessPod{s} }
}

// OverGRPC serves s over an in-memory listener with the adapter's production
// gRPC server, interceptors included, and dials one client connection per
// dialer call.
func OverGRPC(t *testing.T, s *adapter.Server) func(t *testing.T) Pod {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return func(t *testing.T) Pod {
		t.Helper()
		conn, err := grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatalf("dial adapter: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return grpcPod{adapterv1.NewAdapterClient(conn)}
	}
}

// Fixture is one adapter under test with its fake runtime, its session scrub
// report recorder, and the roots its per-slot trees nest under.
type Fixture struct {
	Server  *adapter.Server
	Runtime *Runtime
	Reports *ScrubReports
	Roots   slotlayout.Roots
	// Pod is the first client the transport opened.
	Pod  Pod
	dial func(t *testing.T) Pod
}

// fixturePodID is the pod name the adapter reads from its Downward API
// variable. The adapter withholds every session scrub report when it has
// none, so a fixture without it could not observe that a report is filed.
const fixturePodID = "pod-bind-attempt"

// New builds a fixture over transport. It sets POD_NAME for the test, so a
// case using it cannot run in parallel with another that sets it.
func New(t *testing.T, transport Transport) *Fixture {
	t.Helper()
	t.Setenv("POD_NAME", fixturePodID)
	base := t.TempDir()
	roots := slotlayout.Roots{
		Workspace:   filepath.Join(base, "workspace"),
		Sessions:    filepath.Join(base, "sessions"),
		Artifacts:   filepath.Join(base, "artifacts"),
		Credentials: filepath.Join(base, "run", "lenny"),
	}
	s := adapter.New("bind-attempt-battery")
	s.WorkspaceBase = roots.Workspace
	s.SessionsRoot = roots.Sessions
	s.ArtifactsRoot = roots.Artifacts
	s.CredentialsDir = roots.Credentials
	rt := &Runtime{}
	s.Runtime = rt
	reports := &ScrubReports{}
	s.SessionScrubReporter = reports
	dial := transport(t, s)
	return &Fixture{Server: s, Runtime: rt, Reports: reports, Roots: roots, Pod: dial(t), dial: dial}
}

// Dial opens a further client to the fixture's adapter. Over gRPC it is a
// separate connection.
func (f *Fixture) Dial(t *testing.T) Pod {
	t.Helper()
	return f.dial(t)
}

// Runtime is an SDK-warm fake runtime, so every request the admission rules
// govern, ConfigureWorkspace included, is reachable on one adapter. It records
// the sessions it closed, and a case can park a Start or a Close for one
// session through a hook.
type Runtime struct {
	mu      sync.Mutex
	closed  []string
	onStart func(sessionID string)
	onClose func(sessionID string)
}

// Start runs the start hook, which a case uses to hold a start in flight.
func (r *Runtime) Start(_ context.Context, sessionID string) error {
	r.mu.Lock()
	hook := r.onStart
	r.mu.Unlock()
	if hook != nil {
		hook(sessionID)
	}
	return nil
}

// Close records sessionID and runs the close hook, which a case uses to hold
// a cleanup in flight.
func (r *Runtime) Close(_ context.Context, sessionID string) error {
	r.mu.Lock()
	r.closed = append(r.closed, sessionID)
	hook := r.onClose
	r.mu.Unlock()
	if hook != nil {
		hook(sessionID)
	}
	return nil
}

// WriteEnvelope accepts and discards a frame.
func (r *Runtime) WriteEnvelope(string, []byte) error { return nil }

// Interrupt is a no-op.
func (r *Runtime) Interrupt(context.Context, string, bool) error { return nil }

// Output returns a closed channel: the fake runtime emits nothing.
func (r *Runtime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

// PreConnect is a no-op success.
func (r *Runtime) PreConnect(context.Context) error { return nil }

// ConfigureWorkspace is a no-op success.
func (r *Runtime) ConfigureWorkspace(context.Context, string, string) error { return nil }

// DemoteSDK is a no-op success.
func (r *Runtime) DemoteSDK(context.Context) error { return nil }

// Closes returns how many times the runtime closed sessionID.
func (r *Runtime) Closes(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, id := range r.closed {
		if id == sessionID {
			n++
		}
	}
	return n
}

// SetOnStart installs or clears the start hook.
func (r *Runtime) SetOnStart(f func(sessionID string)) {
	r.mu.Lock()
	r.onStart = f
	r.mu.Unlock()
}

// SetOnClose installs or clears the close hook.
func (r *Runtime) SetOnClose(f func(sessionID string)) {
	r.mu.Lock()
	r.onClose = f
	r.mu.Unlock()
}

// ScrubReports records every session scrub report the adapter files.
type ScrubReports struct {
	mu       sync.Mutex
	sessions []string
}

// ReportSessionScrub records the report.
func (r *ScrubReports) ReportSessionScrub(_ context.Context, _, sessionID string, _ gatewaycontrol.SessionScrubOutcome) error {
	r.mu.Lock()
	r.sessions = append(r.sessions, sessionID)
	r.mu.Unlock()
	return nil
}

// For returns how many reports named sessionID.
func (r *ScrubReports) For(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, id := range r.sessions {
		if id == sessionID {
			n++
		}
	}
	return n
}

// inProcessPod calls the Server's handlers directly. The embedded Server
// supplies every method but PrepareWorkspace, which takes a stream.
type inProcessPod struct{ *adapter.Server }

// PrepareWorkspace runs the handler over an in-memory stream carrying frames.
func (p inProcessPod) PrepareWorkspace(ctx context.Context, frames ...*adapterv1.PrepareWorkspaceRequest) (*adapterv1.PrepareWorkspaceResponse, error) {
	stream := &prepareStream{ctx: ctx, frames: frames}
	if err := p.Server.PrepareWorkspace(stream); err != nil {
		return nil, err
	}
	return stream.resp, nil
}

// prepareStream is the server side of one PrepareWorkspace call, fed from a
// fixed frame list. Only Context, Recv and SendAndClose are reached.
type prepareStream struct {
	grpc.ServerStream
	ctx    context.Context
	frames []*adapterv1.PrepareWorkspaceRequest
	resp   *adapterv1.PrepareWorkspaceResponse
}

func (s *prepareStream) Context() context.Context { return s.ctx }

func (s *prepareStream) Recv() (*adapterv1.PrepareWorkspaceRequest, error) {
	if len(s.frames) == 0 {
		return nil, io.EOF
	}
	f := s.frames[0]
	s.frames = s.frames[1:]
	return f, nil
}

func (s *prepareStream) SendAndClose(resp *adapterv1.PrepareWorkspaceResponse) error {
	s.resp = resp
	return nil
}

func (s *prepareStream) SetHeader(metadata.MD) error  { return nil }
func (s *prepareStream) SendHeader(metadata.MD) error { return nil }
func (s *prepareStream) SetTrailer(metadata.MD)       {}

// grpcPod drives the adapter through a generated client.
type grpcPod struct{ c adapterv1.AdapterClient }

func (p grpcPod) PrepareWorkspace(ctx context.Context, frames ...*adapterv1.PrepareWorkspaceRequest) (*adapterv1.PrepareWorkspaceResponse, error) {
	stream, err := p.c.PrepareWorkspace(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range frames {
		// A send error means the server has already answered; the answer
		// is read from CloseAndRecv below.
		if err := stream.Send(f); err != nil {
			break
		}
	}
	return stream.CloseAndRecv()
}

func (p grpcPod) FinalizeWorkspace(ctx context.Context, req *adapterv1.FinalizeWorkspaceRequest) (*adapterv1.FinalizeWorkspaceResponse, error) {
	return p.c.FinalizeWorkspace(ctx, req)
}

func (p grpcPod) RunSetup(ctx context.Context, req *adapterv1.RunSetupRequest) (*adapterv1.RunSetupResponse, error) {
	return p.c.RunSetup(ctx, req)
}

func (p grpcPod) AssignCredentials(ctx context.Context, req *adapterv1.AssignCredentialsRequest) (*adapterv1.AssignCredentialsResponse, error) {
	return p.c.AssignCredentials(ctx, req)
}

func (p grpcPod) StartSession(ctx context.Context, req *adapterv1.StartSessionRequest) (*adapterv1.StartSessionResponse, error) {
	return p.c.StartSession(ctx, req)
}

func (p grpcPod) Resume(ctx context.Context, req *adapterv1.ResumeRequest) (*adapterv1.ResumeResponse, error) {
	return p.c.Resume(ctx, req)
}

func (p grpcPod) ConfigureWorkspace(ctx context.Context, req *adapterv1.ConfigureWorkspaceRequest) (*adapterv1.ConfigureWorkspaceResponse, error) {
	return p.c.ConfigureWorkspace(ctx, req)
}

func (p grpcPod) Shutdown(ctx context.Context, req *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error) {
	return p.c.Shutdown(ctx, req)
}
