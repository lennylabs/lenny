// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// goneReleaseExecutor records each Release and removes the released
// binding from the registry, as the pod executor does.
type goneReleaseExecutor struct {
	registry *podsession.Registry
	mu       sync.Mutex
	released []string
}

func (e *goneReleaseExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (e *goneReleaseExecutor) Close(ctx context.Context, id string) error {
	return e.Release(ctx, id, "")
}

func (e *goneReleaseExecutor) Release(_ context.Context, id string, d executor.Disposition) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.released = append(e.released, id+":"+string(d))
	e.registry.Remove(id)
	return nil
}

func (e *goneReleaseExecutor) releases() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.released...)
}

// TestCheckpointerReleaseGoneIsWiredToTheSessionServer_spec_7_1 pins the
// production wiring of the checkpointer's ReleaseGone seam: buildPodLifecycle
// sets it on the Checkpointer it builds, a call before the session server is
// set returns at once and does nothing, and once the server is set concurrent
// calls for a session whose row is absent release the binding this replica
// holds exactly once, with the failed disposition.
//
// spec: 7.1 (Seal-and-export invariant), 10.1.7 (preStop Hook Drain)
func TestCheckpointerReleaseGoneIsWiredToTheSessionServer_spec_7_1(t *testing.T) {
	src, err := os.ReadFile("pod_lifecycle.go")
	if err != nil {
		t.Fatalf("read pod_lifecycle.go: %v", err)
	}
	flat := regexp.MustCompile(`\s+`).ReplaceAllString(string(src), " ")
	if !strings.Contains(flat, "ReleaseGone: goneBindingReleaserSeam{w: w}.ReleaseGone,") {
		t.Fatal("pod_lifecycle.go: the Checkpointer is built without the ReleaseGone seam")
	}

	w := &gatewayWiring{}
	release := goneBindingReleaserSeam{w: w}.ReleaseGone
	done := make(chan struct{})
	go func() {
		release("acme", "s1")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a ReleaseGone call before the session server is set blocked")
	}

	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme", CoordinationGeneration: 1})
	exec := &goneReleaseExecutor{registry: registry}
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true, Executor: exec, PodRegistry: registry,
	})
	t.Cleanup(srv.Close)
	w.sessionSrv = srv

	const callers = 32
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release("acme", "s1")
		}()
	}
	wg.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for len(exec.releases()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the binding was not released")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Calls after the release find no binding and release nothing.
	for range callers {
		release("acme", "s1")
	}
	time.Sleep(100 * time.Millisecond)
	got := exec.releases()
	want := "s1:" + string(executor.DispositionFailed)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("releases = %v, want exactly [%s]", got, want)
	}
	if _, ok := registry.Get("s1"); ok {
		t.Error("the binding is still registered after the release")
	}
}
