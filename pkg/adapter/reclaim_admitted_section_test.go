// SPDX-License-Identifier: MIT

//go:build unix

package adapter

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// The cases below pin, deterministically and in-process, what a reclaim does
// to a section the per-slot guard admitted before the reclaim opened its
// hold: a mid-session FinalizeWorkspace parked inside its materialization,
// and a Resume parked inside the checkpoint restore. The hold cannot reach a
// section already past its resolve, so the guard is what keeps the reclaim's
// tree removal from running beside that section's writes. Each park uses a
// seam the production path already reads: a named pipe standing in for a
// staged upload, whose open and read block inside workspace materialization,
// and a CheckpointTransport whose second chunk withholds the rest of the
// archive, which blocks workspace.ExtractTree mid-stream.

// guardParkTimeout bounds every wait for a parked section to be reached or
// to return, so a regression fails the case rather than hanging the binary.
const guardParkTimeout = 10 * time.Second

// stagingDirOf returns the staging directory of sessionID's registry entry.
func stagingDirOf(t *testing.T, s *Server, sessionID string) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slots[sessionID]
	if !ok || st.paths.Staging == "" {
		t.Fatalf("%s has no registry entry with a staging directory", sessionID)
	}
	return st.paths.Staging
}

// currentDirOf returns the path of sessionID's current workspace tree under
// the pod's workspace base, whether or not the tree exists.
func currentDirOf(s *Server, sessionID string) string {
	return filepath.Join(s.WorkspaceBase, "slots", sessionID, "current")
}

// openFIFOWriterOnceRead waits until a reader has opened the named pipe at
// path and returns the write end. Opening a pipe's write end without
// blocking fails with ENXIO while no reader has it open, so the first
// successful open marks the point at which the reader is inside its open or
// its first read, and the reader stays parked until the write end is closed.
func openFIFOWriterOnceRead(t *testing.T, path string) *os.File {
	t.Helper()
	deadline := time.Now().Add(guardParkTimeout)
	for {
		fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			return os.NewFile(uintptr(fd), path)
		}
		if !errors.Is(err, syscall.ENXIO) {
			t.Fatalf("open the write end of %s: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reader opened %s", path)
		}
		time.Sleep(time.Millisecond)
	}
}

// removalAfter wraps the slot tree removal so that it records, at the
// moment the reclaim removes sessionID's tree, whether the file at marker
// already exists. A reclaim that removed the tree while the parked section
// was still writing finds the marker absent.
type removalAfter struct {
	mu            sync.Mutex
	markerPresent map[string]bool
}

func (r *removalAfter) install(s *Server, markers map[string]string) {
	r.markerPresent = map[string]bool{}
	s.removeSlotTreeFn = func(st *slotState) error {
		if marker, ok := markers[st.sessionID]; ok {
			_, err := os.Stat(marker)
			r.mu.Lock()
			r.markerPresent[st.sessionID] = err == nil
			r.mu.Unlock()
		}
		return removeSlotTree(st)
	}
}

// sawMarker reports whether the removal of sessionID's tree ran and, when it
// did, whether the marker existed at that moment.
func (r *removalAfter) sawMarker(sessionID string) (present, removed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	present, removed = r.markerPresent[sessionID]
	return present, removed
}

// wantStillRunning fails the case when done has already delivered, which
// means the call it belongs to returned while it should still be waiting.
func wantStillRunning[T any](t *testing.T, what string, done <-chan T) {
	t.Helper()
	select {
	case v := <-done:
		t.Fatalf("%s returned (%v) while the admitted section held the slot's guard", what, v)
	default:
	}
}

// receiveWithin returns the value done delivers, failing the case when it
// does not deliver within guardParkTimeout.
func receiveWithin[T any](t *testing.T, what string, done <-chan T) T {
	t.Helper()
	select {
	case v := <-done:
		return v
	case <-time.After(guardParkTimeout):
		t.Fatalf("%s did not return once the parked section was released", what)
		var zero T
		return zero
	}
}

// spec: §5.2 (pool configuration and execution modes); §4.7.1 (role and gateway RPC contract); §7.4 (upload safety)
//
// A mid-session FinalizeWorkspace admitted onto a live session holds the
// slot's guard across its materialization. A matching Shutdown that arrives
// while the materialization is parked waits for the guard: the entry is not
// deregistered and the tree is not removed while the finalize is still
// writing. Once the finalize returns, the reclaim removes the entry and the
// whole tree, including what the finalize promoted, and nothing the finalize
// wrote survives the reclaim or re-creates the tree afterwards. The
// completed reclaim releases the identifier, so a later attempt binds it.
func TestAReclaimWaitsForAnAdmittedMidSessionFinalize_spec_5_2(t *testing.T) {
	s, _ := orderingPod(t)
	if err := assign(s, "alice", attempt1); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := start(s, "alice"); err != nil {
		t.Fatalf("start: %v", err)
	}
	const ref = "lenny-blob://acme/parked"
	staged, err := workspace.StagingPath(stagingDirOf(t, s, "alice"), ref)
	if err != nil {
		t.Fatalf("staging path: %v", err)
	}
	if err := makeFIFO(staged); err != nil {
		t.Fatalf("make the parking pipe: %v", err)
	}
	promoted := filepath.Join(currentDirOf(s, "alice"), "docs", "parked.md")
	var removal removalAfter
	removal.install(s, map[string]string{"alice": promoted})

	finalizeDone := make(chan error, 1)
	go func() {
		_, ferr := s.FinalizeWorkspace(context.Background(), &adapterv1.FinalizeWorkspaceRequest{
			SessionId:  &adapterv1.SessionId{Value: "alice"},
			MidSession: true,
			WorkspacePlan: &adapterv1.WorkspacePlan{SchemaVersion: 1, Sources: []*adapterv1.WorkspaceSource{
				{Type: "uploadFile", Path: "docs/parked.md", UploadRef: ref, Mode: "644"},
			}},
		})
		finalizeDone <- ferr
	}()
	writer := openFIFOWriterOnceRead(t, staged)
	defer writer.Close()

	reclaimDone := make(chan *adapterv1.ShutdownResponse, 1)
	go func() {
		resp, serr := s.Shutdown(context.Background(), fencedShutdown("alice", attempt1))
		if serr != nil {
			t.Errorf("Shutdown: %v", serr)
		}
		reclaimDone <- resp
	}()
	waitForGuardWaiter(t)
	wantStillRunning(t, "the matching Shutdown", reclaimDone)
	if !hasEntry(s, "alice") {
		t.Error("the reclaim deregistered the entry while the finalize held the slot's guard")
	}
	if !slotDirExists(s, "alice") {
		t.Error("the reclaim removed the tree while the finalize held the slot's guard")
	}

	if _, err := writer.Write([]byte("uploaded")); err != nil {
		t.Fatalf("write the staged upload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close the staged upload: %v", err)
	}
	if ferr := receiveWithin(t, "FinalizeWorkspace", finalizeDone); ferr != nil {
		t.Fatalf("FinalizeWorkspace = %v, want the admitted finalize to complete", ferr)
	}
	resp := receiveWithin(t, "the matching Shutdown", reclaimDone)
	if resp.GetSlotReclaim() != outcomeReclaimed {
		t.Errorf("slot_reclaim = %v, want RECLAIMED", resp.GetSlotReclaim())
	}
	if present, removed := removal.sawMarker("alice"); !removed || !present {
		t.Errorf("tree removal ran = %v with the promoted file present = %v, want the removal after the finalize's promotion", removed, present)
	}
	if hasEntry(s, "alice") || slotDirExists(s, "alice") {
		t.Error("an entry or a tree for alice survived the reclaim")
	}
	if err := assign(s, "alice", attempt2); err != nil {
		t.Errorf("a later attempt after the completed reclaim = %v, want admitted", err)
	}
}

// parkingTransport is a CheckpointTransport that serves a checkpoint archive
// as two chunks and withholds the second until the case releases it. The
// restore pipeline concatenates the chunks into one stream, so
// workspace.ExtractTree has consumed the whole first chunk, and written every
// entry it holds, by the time the second chunk is requested, and it stays
// parked inside its read until the release.
type parkingTransport struct {
	first, second []byte
	requested     chan struct{}
	release       chan struct{}
	once          sync.Once
}

func newParkingTransport(first, second []byte) *parkingTransport {
	return &parkingTransport{
		first: first, second: second,
		requested: make(chan struct{}), release: make(chan struct{}),
	}
}

func (p *parkingTransport) PutChunk(context.Context, string, map[string]string, int64, io.Reader) (int, string, error) {
	return 200, "", nil
}

func (p *parkingTransport) GetChunk(ctx context.Context, url string, _ map[string]string) (io.ReadCloser, error) {
	if url == parkingChunk0 {
		return io.NopCloser(bytes.NewReader(p.first)), nil
	}
	p.once.Do(func() { close(p.requested) })
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return io.NopCloser(bytes.NewReader(p.second)), nil
}

// The presigned URLs of the two chunks the parking transport serves.
const (
	parkingChunk0 = "https://objectstore.example/chunk-0"
	parkingChunk1 = "https://objectstore.example/chunk-1"
)

// parkingChunks is the chunk set naming the parking transport's two chunks.
func parkingChunks() []*adapterv1.ChunkGrant {
	return []*adapterv1.ChunkGrant{
		{Index: 0, Url: parkingChunk0},
		{Index: 1, Url: parkingChunk1},
	}
}

// splitCheckpointArchive builds a gzip-compressed checkpoint bundle holding
// workspace/first.txt and workspace/second.txt, flushed after the first
// entry, and returns it split at the flush point. The first part decodes to
// the complete first entry on its own, so ExtractTree writes first.txt and
// then parks waiting for the second part.
func splitCheckpointArchive(t *testing.T) (first, second []byte) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	writeEntry := func(name, content string) {
		if err := tw.WriteHeader(&tar.Header{
			Name: workspace.WorkspacePrefix + "/" + name, Typeflag: tar.TypeReg,
			Mode: 0o644, Size: int64(len(content)),
		}); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar body %s: %v", name, err)
		}
	}
	writeEntry("first.txt", "restored before the park")
	if err := tw.Flush(); err != nil {
		t.Fatalf("tar flush: %v", err)
	}
	if err := gz.Flush(); err != nil {
		t.Fatalf("gzip flush: %v", err)
	}
	split := buf.Len()
	writeEntry("second.txt", "restored after the park")
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	all := buf.Bytes()
	return append([]byte(nil), all[:split]...), append([]byte(nil), all[split:]...)
}

// resumeParkedInExtract starts a Resume for sessionID naming token whose
// restore parks inside workspace.ExtractTree, waits until it is parked with
// first.txt restored, and returns the channel its result arrives on and
// the transport whose release channel lets the restore finish.
func resumeParkedInExtract(t *testing.T, s *Server, sessionID, token string) (<-chan error, *parkingTransport) {
	t.Helper()
	first, second := splitCheckpointArchive(t)
	tr := newParkingTransport(first, second)
	s.CheckpointTransport = tr
	done := make(chan error, 1)
	go func() {
		_, err := s.Resume(context.Background(), &adapterv1.ResumeRequest{
			SessionId:    &adapterv1.SessionId{Value: sessionID},
			CheckpointId: "ckpt-1",
			BindAttempt:  token,
			Chunks:       parkingChunks(),
		})
		done <- err
	}()
	select {
	case <-tr.requested:
	case err := <-done:
		t.Fatalf("Resume returned %v before its restore parked", err)
	case <-time.After(guardParkTimeout):
		t.Fatal("the Resume never requested the withheld chunk")
	}
	// The pipe hands ExtractTree the first chunk synchronously, but the
	// write of first.txt can still be in flight when the second chunk is
	// requested, so wait for it to land.
	firstFile := filepath.Join(currentDirOf(s, sessionID), "first.txt")
	deadline := time.Now().Add(guardParkTimeout)
	for {
		if b, err := os.ReadFile(firstFile); err == nil && string(b) == "restored before the park" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the parked restore never wrote first.txt")
		}
		time.Sleep(time.Millisecond)
	}
	return done, tr
}

// spec: §5.2 (pool configuration and execution modes); §4.7.1 (role and gateway RPC contract)
//
// A Resume admitted before the reclaim opened its hold holds the slot's
// guard across its checkpoint restore. A matching Shutdown that arrives
// while workspace.ExtractTree is parked mid-archive waits for the guard, so
// the reclaim neither deregisters the entry nor removes the tree while the
// restore is writing into it. Once the Resume returns, the reclaim tears the
// session down and removes the whole restored tree, and no partially
// restored workspace survives.
func TestAReclaimWaitsForAResumeParkedInsideTheRestore_spec_5_2(t *testing.T) {
	s, _ := orderingPod(t)
	second := filepath.Join(currentDirOf(s, "alice"), "second.txt")
	var removal removalAfter
	removal.install(s, map[string]string{"alice": second})
	resumeDone, tr := resumeParkedInExtract(t, s, "alice", attempt1)

	reclaimDone := make(chan *adapterv1.ShutdownResponse, 1)
	go func() {
		resp, err := s.Shutdown(context.Background(), fencedShutdown("alice", attempt1))
		if err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		reclaimDone <- resp
	}()
	waitForGuardWaiter(t)
	wantStillRunning(t, "the matching Shutdown", reclaimDone)
	if !hasEntry(s, "alice") || !slotDirExists(s, "alice") {
		t.Error("the reclaim deregistered the entry or removed the tree while the restore held the slot's guard")
	}

	close(tr.release)
	if err := receiveWithin(t, "Resume", resumeDone); err != nil {
		t.Fatalf("Resume = %v, want the admitted restore to complete", err)
	}
	resp := receiveWithin(t, "the matching Shutdown", reclaimDone)
	if resp.GetSlotReclaim() != outcomeReclaimed {
		t.Errorf("slot_reclaim = %v, want RECLAIMED", resp.GetSlotReclaim())
	}
	if present, removed := removal.sawMarker("alice"); !removed || !present {
		t.Errorf("tree removal ran = %v with the restore's last file present = %v, want the removal after the restore finished", removed, present)
	}
	if hasEntry(s, "alice") || slotDirExists(s, "alice") {
		t.Error("an entry or a partially restored tree for alice survived the reclaim")
	}
}

// spec: §10.1.4 (coordinator-loss detection and hold state); §5.2 (pool configuration and execution modes); §4.7.1 (role and gateway RPC contract)
//
// The §10.1.4 hold-timeout termination destroys a member's tree from a
// goroutine no request drives, so it takes each member's slot guard ahead of
// the destructive section. With two started members on the pod and the
// first parked inside workspace.ExtractTree under its own guard, the pass
// deregisters both, then waits on the first member's guard for a span
// shorter than its acquisition deadline, so the guard is genuinely acquired.
// The tree is not removed while the restore is writing. The Resume, whose
// entry left the registry while it ran, fails its start confirmation and is
// refused on Aborted rather than left holding a session no entry records,
// and once both have returned neither member
// keeps an entry, a slot tree, or a partially restored workspace. Each
// member's cleanup completed under its guard, so its identifier is released
// and a later bind naming it is admitted.
func TestTheHoldTerminationWaitsForAResumeParkedInsideTheRestore_spec_10_1_4(t *testing.T) {
	setCoordinatorHold(false)
	t.Cleanup(func() { setCoordinatorHold(false) })
	s, _ := orderingPod(t)
	s.PostMortemDir = t.TempDir()
	clk := &fakeExpiryClock{}
	s.HoldAfterFunc = clk.After
	bindAndStart(t, s, "bob")

	second := filepath.Join(currentDirOf(s, "alice"), "second.txt")
	var removal removalAfter
	removal.install(s, map[string]string{"alice": second})
	resumeDone, tr := resumeParkedInExtract(t, s, "alice", attempt1)

	passDone := make(chan struct{})
	go func() {
		defer close(passDone)
		fireHoldTimeout(t, s, clk)
	}()
	waitForGuardWaiter(t)
	wantStillRunning(t, "the hold termination", passDone)
	if !slotDirExists(s, "alice") {
		t.Error("the hold termination removed alice's tree while the restore held the slot's guard")
	}

	close(tr.release)
	rerr := receiveWithin(t, "Resume", resumeDone)
	if status.Code(rerr) != codes.Aborted {
		t.Errorf("Resume whose entry the hold termination deregistered = %v, want the unconfirmed-start refusal on Aborted", rerr)
	}
	receiveWithin(t, "the hold termination", passDone)
	if present, removed := removal.sawMarker("alice"); !removed || !present {
		t.Errorf("alice's tree removal ran = %v with the restore's last file present = %v, want the removal after the restore finished", removed, present)
	}
	for _, id := range []string{"alice", "bob"} {
		if hasEntry(s, id) || slotDirExists(s, id) {
			t.Errorf("an entry or a tree for %s survived the hold termination", id)
		}
		if _, err := s.ensureSlotPaths(id, slotResolve{bindAttempt: attempt2, allowCreate: true}); err != nil {
			t.Errorf("a bind naming %s after its completed termination = %v, want admitted", id, err)
		}
	}
}
