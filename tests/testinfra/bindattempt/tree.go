// SPDX-License-Identifier: MIT

package bindattempt

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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

// fileStamp is what treeSnapshot records for one path: a write, a
// truncation, a mode change, or an entry added to or removed from a
// directory each changes one of these fields.
type fileStamp struct {
	mode fs.FileMode
	size int64
	mod  time.Time
}

// treeSnapshot maps every path under a slot's per-slot tree to its stamp.
type treeSnapshot map[string]fileStamp

// snapshotTree records every path under id's per-slot tree: the slot's
// workspace directory holding current and staging, its session and artifact
// trees, and its credential directory. A root that does not exist
// contributes nothing, so a later path under it shows up as added.
func (f *Fixture) snapshotTree(t *testing.T, id string) treeSnapshot {
	t.Helper()
	p := f.slotPaths(t, id)
	snap := treeSnapshot{}
	for _, root := range []string{filepath.Dir(p.Current), p.Sessions, p.Artifacts, p.CredentialsDir} {
		if root == "" || root == "." {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			snap[path] = fileStamp{mode: info.Mode(), size: info.Size(), mod: info.ModTime()}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("snapshot %s's tree under %s: %v", id, root, err)
		}
	}
	return snap
}

// wantTreeUnchanged fails the case unless id's per-slot tree is exactly as
// before recorded it, naming each path that was added, removed or modified. It
// is how a case sees a write a refused request made while a parked cleanup
// holds the tree, before that cleanup removes the tree and the write with
// it.
func (f *Fixture) wantTreeUnchanged(t *testing.T, what, id string, before treeSnapshot) {
	t.Helper()
	after := f.snapshotTree(t, id)
	for path, stamp := range after {
		was, ok := before[path]
		switch {
		case !ok:
			t.Errorf("%s: %s was created in %s's tree", what, path, id)
		case was != stamp:
			t.Errorf("%s: %s in %s's tree was modified", what, path, id)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Errorf("%s: %s was removed from %s's tree", what, path, id)
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
