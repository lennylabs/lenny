// SPDX-License-Identifier: MIT

package executor_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
)

// sealingRuntime answers every message with a response. On its first message
// it waits past the idle window, has the gateway seal the session through
// seal, and only then answers, so the batch's next message meets the seal.
type sealingRuntime struct {
	out      chan []byte
	seal     func()
	messages atomic.Int32
	once     sync.Once
}

func (r *sealingRuntime) TapOutput(func([]byte)) {}

func (r *sealingRuntime) Start(context.Context, string) error { return nil }

func (r *sealingRuntime) WriteEnvelope(_ string, envelope []byte) error {
	if !jsonHasType(envelope, "message") {
		return nil
	}
	if r.messages.Add(1) == 1 {
		go r.once.Do(func() {
			time.Sleep(1100 * time.Millisecond)
			r.seal()
			r.out <- []byte(`{"type":"response","text":"first"}`)
		})
		return nil
	}
	r.out <- []byte(`{"type":"response","text":"later"}`)
	return nil
}

func (r *sealingRuntime) Output(context.Context, string) (<-chan []byte, error) { return r.out, nil }
func (r *sealingRuntime) Interrupt(context.Context, string, bool) error         { return nil }
func (r *sealingRuntime) Close(context.Context, string) error                   { return nil }

// spec: §28.5.1 (CH-ATTACH)
// The executor's Send returns an error satisfying ErrSessionSealed with the
// delivered count and the delivered turns' response when the adapter ends the
// stream as sealed, calls no stream-failure handler, and opens a new stream
// on the next Send.
func TestPodExecutorSendReportsASealedRefusal_spec_28_5_1(t *testing.T) {
	rt := &sealingRuntime{out: make(chan []byte, 8)}
	cl := dialPodAdapter(t, rt)
	rt.seal = func() {
		sealed, _, err := cl.SealSession(context.Background(), "sess-pod", 0, 1)
		if err != nil || !sealed {
			t.Errorf("SealSession = %v, %v; want sealed", sealed, err)
		}
	}
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess-pod", Adapter: cl})
	pe := executor.NewPodExecutor(reg, nil)
	var failures atomic.Int32
	pe.SetStreamFailureHandler(func(string, string, string, error) { failures.Add(1) })

	_, err := pe.Send(context.Background(), "sess-pod", []executor.Message{
		{Role: "user", Content: "one"}, {Role: "user", Content: "two"},
	})
	var sealed *executor.SealedError
	if !errors.As(err, &sealed) || !errors.Is(err, executor.ErrSessionSealed) {
		t.Fatalf("Send = %v, want a *SealedError satisfying ErrSessionSealed", err)
	}
	if sealed.Delivered != 1 || len(sealed.Response.Parts) != 1 || sealed.Response.Parts[0].Text != "first" {
		t.Fatalf("SealedError = %+v, want one delivered message answered \"first\"", sealed)
	}
	if n := failures.Load(); n != 0 {
		t.Fatalf("stream-failure handler ran %d times for a sealed refusal, want 0", n)
	}
	if err := cl.UnsealSession(context.Background(), "sess-pod", 0); err != nil {
		t.Fatalf("UnsealSession: %v", err)
	}
	out, err := pe.Send(context.Background(), "sess-pod", []executor.Message{{Role: "user", Content: "three"}})
	if err != nil || len(out.Parts) != 1 || out.Parts[0].Text != "later" {
		t.Fatalf("Send after the unseal = %+v, %v; want a new stream that delivers", out, err)
	}
}

// jsonHasType reports whether a JSONL frame's type is ty.
func jsonHasType(frame []byte, ty string) bool {
	var probe struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(frame, &probe) == nil && probe.Type == ty
}
