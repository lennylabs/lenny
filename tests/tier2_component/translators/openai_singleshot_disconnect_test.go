// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test for a client disconnect that lands mid-turn on the
// built-in OpenAI-dialect adapter's single-shot dispatch, run against the
// real PodExecutor and a real adapter on an envtest-backed binder.
//
// The executor holds the session's Attach stream on a context detached from
// the delivering request. A request that ends mid-turn therefore returns its
// context error promptly without ending the stream, and the stream ends only
// when the release the handler runs on a detached context evicts it. The
// release itself is covered by
// TestSingleShotReleaseDrainsOnDetachedContextAfterTimeout_spec_15.
//
// spec: §28.5.1 (Gateway-to-pod, CH-ATTACH), §7.2 (Interactive Session
// Model), §15 (single-shot release on request timeout).
package translators_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// ssSilentRuntime is an adapter.RuntimeProcess that never answers a
// message, so a turn stays in flight until the request that sent it ends.
// It signals received when the first message reaches it.
type ssSilentRuntime struct {
	out      chan []byte
	once     sync.Once
	received chan struct{}
}

func (r *ssSilentRuntime) Start(context.Context, string) error { return nil }
func (r *ssSilentRuntime) WriteEnvelope(_ string, envelope []byte) error {
	var probe struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(envelope, &probe)
	if probe.Type == "message" {
		r.once.Do(func() { close(r.received) })
	}
	return nil
}

func (r *ssSilentRuntime) Output(context.Context, string) (<-chan []byte, error) {
	return r.out, nil
}
func (r *ssSilentRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (r *ssSilentRuntime) Close(context.Context, string) error           { return nil }

// ssAttachEndRecorder is a stream interceptor that records when the
// server-side context of each Attach RPC ends.
type ssAttachEndRecorder struct {
	mu    sync.Mutex
	ended chan struct{}
	at    time.Time
}

func newSSAttachEndRecorder() *ssAttachEndRecorder {
	return &ssAttachEndRecorder{ended: make(chan struct{})}
}

func (r *ssAttachEndRecorder) intercept(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if strings.HasSuffix(info.FullMethod, "/Attach") {
		go func() {
			<-ss.Context().Done()
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.at.IsZero() {
				r.at = time.Now()
				close(r.ended)
			}
		}()
	}
	return handler(srv, ss)
}

func (r *ssAttachEndRecorder) endedAt() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.at
}

// ssDisconnectSpy wraps the real PodExecutor and records the dispatch's
// outcome: the Send's error, how long after the request context ended it
// returned, and whether the adapter had already seen the Attach stream end
// by the time the release began.
type ssDisconnectSpy struct {
	inner    *executor.PodExecutor
	attach   *ssAttachEndRecorder
	received <-chan struct{}
	cancel   context.CancelFunc

	sendErr            error
	returnedAfter      time.Duration
	endedBeforeRelease bool
	releasedAt         time.Time
}

func (e *ssDisconnectSpy) Send(ctx context.Context, sessionID string, msgs []executor.Message) (executor.Response, error) {
	// The client disconnects once the message has reached the runtime, so
	// the turn is in flight when the request context ends.
	cancelledAt := make(chan time.Time, 1)
	go func() {
		<-e.received
		cancelledAt <- time.Now()
		e.cancel()
	}()
	resp, err := e.inner.Send(ctx, sessionID, msgs)
	e.sendErr = err
	select {
	case at := <-cancelledAt:
		e.returnedAfter = time.Since(at)
	default:
	}
	// A stream opened on the request context would end on the adapter
	// shortly after the cancellation; give that end time to arrive before
	// the release runs, so its absence is meaningful.
	time.Sleep(300 * time.Millisecond)
	e.endedBeforeRelease = !e.attach.endedAt().IsZero()
	return resp, err
}

func (e *ssDisconnectSpy) Close(ctx context.Context, sessionID string) error {
	return e.inner.Close(ctx, sessionID)
}

func (e *ssDisconnectSpy) Release(ctx context.Context, sessionID string, disp executor.Disposition) error {
	e.releasedAt = time.Now()
	return e.inner.Release(ctx, sessionID, disp)
}

var _ executor.SessionReleaser = (*ssDisconnectSpy)(nil)

// spec: 28.5.1 (Gateway-to-pod), 7.2 (Interactive Session Model), 15
// (single-shot release on request timeout)
// diagnosis: a client disconnect mid-turn either kept the single-shot
// dispatch waiting on a reply that will never come (Send did not return the
// context error promptly), ended the session's Attach stream with the
// request rather than with the release (the stream was opened on the
// request context, which is the defect that broke every later message on a
// session), or left the stream open after the release (the release did not
// evict it, so the adapter keeps a consumer for a released session).
func TestSingleShotDisconnectMidTurnEndsTheStreamOnlyAtRelease_spec_28_5_1(t *testing.T) {
	cluster := ssEnvClient(
		t,
		ssWarmPool("echo-pool", "echo-tmpl"),
		ssTemplate("echo-tmpl", "echo"),
		ssIdleSandbox("sbx-1", "echo-pool", "10.244.2.5"),
	)
	rt := &ssSilentRuntime{out: make(chan []byte, 8), received: make(chan struct{})}
	attach := newSSAttachEndRecorder()
	binder := ssBinder(cluster, ssAdapterDialer(t, rt, grpc.ChainStreamInterceptor(attach.intercept)))
	binder.RecycleBoundary = ssRecycleBoundary{}
	registry := podsession.NewRegistry()
	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), ssRecyclingPool("echo-pool", "echo")); err != nil {
		t.Fatalf("create recycling pool: %v", err)
	}
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                  func() string { return "sess-ss-disconnect" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          ssNS,
		Pools:                   pools,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	spy := &ssDisconnectSpy{
		inner:    executor.NewPodExecutor(registry, binder),
		attach:   attach,
		received: rt.received,
		cancel:   cancel,
	}
	handler := translator.NewOpenAIChatHandler(store, spy, translator.OpenAIChatOptions{
		SingleShotBinder: ssSingleShotBinder{srv: srv},
		DefaultRuntime:   "echo",
	}).Handler()

	rr := ssDriveChat(t, handler, "echo", func(r *http.Request) *http.Request {
		return r.WithContext(ctx)
	})
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("disconnected dispatch status = %d, want 500; body=%s", rr.Code, rr.Body.String())
	}
	if !errors.Is(spy.sendErr, context.Canceled) {
		t.Fatalf("Send after the client disconnected = %v, want the request's context.Canceled", spy.sendErr)
	}
	if spy.returnedAfter > 2*time.Second {
		t.Errorf("Send returned %v after the client disconnected, want promptly", spy.returnedAfter)
	}
	if spy.endedBeforeRelease {
		t.Error("the adapter saw the Attach stream end before the release ran; the stream followed the request context")
	}
	select {
	case <-attach.ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the adapter never saw the Attach stream end after the release")
	}
	if spy.releasedAt.IsZero() || attach.endedAt().Before(spy.releasedAt) {
		t.Errorf("Attach stream ended at %v, release began at %v; want the end to follow the release",
			attach.endedAt(), spy.releasedAt)
	}
	if _, err := ssClaim(t, cluster, "sbx-1"); !apierrors.IsNotFound(err) {
		t.Errorf("per-pod claim get after the disconnect release = %v, want NotFound", err)
	}
}
