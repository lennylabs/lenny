// SPDX-License-Identifier: MIT

package bindattempt

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/slotlayout"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// slotPaths resolves id's per-slot tree under the fixture's roots.
func (f *Fixture) slotPaths(t *testing.T, id string) slotlayout.SlotPaths {
	t.Helper()
	p, err := slotlayout.Resolve(f.Roots, id)
	if err != nil {
		t.Fatalf("resolve slot paths for %s: %v", id, err)
	}
	return p
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// wantTreeIntact fails the case unless id's cwd and, when withCredentials,
// its credential file are both on disk.
func (f *Fixture) wantTreeIntact(t *testing.T, id string, withCredentials bool) {
	t.Helper()
	p := f.slotPaths(t, id)
	if !exists(p.Current) {
		t.Errorf("%s's current directory was removed", id)
	}
	if withCredentials && !exists(p.CredentialsFile) {
		t.Errorf("%s's credentials.json was removed", id)
	}
}

// wantTreeRemoved fails the case unless id's cwd and credential directory
// are both gone, which is the slot release a removing rule performs.
func (f *Fixture) wantTreeRemoved(t *testing.T, id string) {
	t.Helper()
	p := f.slotPaths(t, id)
	if exists(p.Current) || exists(p.CredentialsDir) {
		t.Errorf("%s's per-slot tree survived the slot release", id)
	}
}

// wantNoTree fails the case unless no part of id's per-slot tree exists.
func (f *Fixture) wantNoTree(t *testing.T, id string) {
	t.Helper()
	p := f.slotPaths(t, id)
	for _, path := range []string{p.Current, p.Staging, p.Sessions, p.Artifacts, p.CredentialsDir} {
		if exists(path) {
			t.Errorf("%s exists; a refused request created part of %s's tree", path, id)
		}
	}
}

// parkedCleanup is a Shutdown whose runtime close is held open, so the slot
// identifier stays held by the §5.2 reclaim hold until the case releases it.
type parkedCleanup struct {
	release chan struct{}
	once    sync.Once
	done    chan *shutdownResult
}

// unpark releases the runtime close. It is idempotent, so the cleanup a
// failed case registers cannot close the channel twice.
func (pc *parkedCleanup) unpark() { pc.once.Do(func() { close(pc.release) }) }

type shutdownResult struct {
	resp *adapterv1.ShutdownResponse
	err  error
}

// park starts an unconditional Shutdown of id, which must be running, and
// returns once the Shutdown is inside the runtime close, after the entry
// has been deregistered and the hold opened.
func (f *Fixture) park(t *testing.T, id string) *parkedCleanup {
	t.Helper()
	entered := make(chan struct{})
	pc := &parkedCleanup{release: make(chan struct{}), done: make(chan *shutdownResult, 1)}
	f.Runtime.SetOnClose(func(closing string) {
		if closing != id {
			return
		}
		close(entered)
		<-pc.release
	})
	t.Cleanup(pc.unpark)
	pod := f.Dial(t)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*callTimeout)
		defer cancel()
		resp, err := pod.Shutdown(ctx, unconditionalReq(id))
		pc.done <- &shutdownResult{resp: resp, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(callTimeout):
		t.Fatalf("the Shutdown of %s never reached its runtime close", id)
	}
	return pc
}

// finish lets the parked cleanup complete and fails the case unless it
// reclaimed the entry.
func (f *Fixture) finish(t *testing.T, pc *parkedCleanup) {
	t.Helper()
	pc.unpark()
	var r *shutdownResult
	select {
	case r = <-pc.done:
	case <-time.After(callTimeout):
		t.Fatal("the parked Shutdown did not return once released")
	}
	f.Runtime.SetOnClose(nil)
	if r.err != nil || r.resp.GetSlotReclaim() != reclaimed {
		t.Fatalf("the parked Shutdown = %v, %v; want RECLAIMED", r.resp.GetSlotReclaim(), r.err)
	}
}
