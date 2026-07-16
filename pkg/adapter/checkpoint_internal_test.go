// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// TestReadChunkSpillsLargeChunkToFile pins the §4.4 line 255 memory bound:
// a chunk larger than the memory threshold is buffered in a StagingDir
// spill file rather than on the heap, and the spilled chunk is re-readable
// (the retry budget re-reads the body) and cleaned up on close.
func TestReadChunkSpillsLargeChunkToFile(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("payload"), 32)
	// Threshold below the chunk size forces the spill path.
	buf, more, err := readChunk(bytes.NewReader(data), int64(len(data)), 8, dir)
	if err != nil || !more {
		t.Fatalf("readChunk spill: more=%v err=%v", more, err)
	}
	if buf.path == "" {
		t.Fatal("expected the oversized chunk to spill to a file, got a heap buffer")
	}
	if buf.len() != int64(len(data)) {
		t.Fatalf("spilled chunk len = %d, want %d", buf.len(), len(data))
	}
	// The spilled chunk is re-readable across retry attempts.
	for attempt := 0; attempt < 2; attempt++ {
		rc, oerr := buf.reopen()
		if oerr != nil {
			t.Fatalf("reopen attempt %d: %v", attempt, oerr)
		}
		got, _ := io.ReadAll(rc)
		_ = rc.Close()
		if !bytes.Equal(got, data) {
			t.Fatalf("reopen attempt %d read %d bytes, want the spilled chunk", attempt, len(got))
		}
	}
	path := buf.path
	buf.close()
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("spill file %q was not removed on close (err %v)", path, statErr)
	}
}

// TestReadChunkHeapPathAndEOF pins the heap-buffer path and the clean
// end-of-stream signal.
func TestReadChunkHeapPathAndEOF(t *testing.T) {
	data := []byte("small chunk")
	r := bytes.NewReader(data)
	// Threshold above the chunk size keeps it on the heap.
	buf, more, err := readChunk(r, int64(len(data)), 1<<20, t.TempDir())
	if err != nil || !more {
		t.Fatalf("readChunk heap: more=%v err=%v", more, err)
	}
	if buf.path != "" {
		t.Fatalf("expected a heap buffer, got a spill file %q", buf.path)
	}
	rc, _ := buf.reopen()
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(got, data) {
		t.Fatalf("heap chunk = %q, want %q", got, data)
	}
	buf.close() // no-op for the heap form

	// A fully-consumed reader yields a clean end of stream.
	buf, more, err = readChunk(r, 4, 1<<20, t.TempDir())
	if err != nil {
		t.Fatalf("readChunk at EOF: %v", err)
	}
	if more || buf != nil {
		t.Fatalf("expected clean EOF (more=false, buf=nil), got more=%v buf=%v", more, buf)
	}
}
