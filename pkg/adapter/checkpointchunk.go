// SPDX-License-Identifier: MIT

package adapter

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// checkpointBufferMemoryBytes is the largest chunk the adapter holds on
// the heap. A chunk_size_bytes above this threshold spills the one-chunk
// retry buffer to a file under StagingDir so the agent container's memory
// limit bounds the checkpoint path (§4.4 line 255 workspace budget).
const checkpointBufferMemoryBytes = 8 << 20 // 8 MiB

// defaultCheckpointChunkSize is the chunk size the adapter buffers when
// the gateway's CheckpointStart leaves chunk_size_bytes unset.
const defaultCheckpointChunkSize = 4 << 20 // 4 MiB

// chunkBuffer is a re-readable hold of exactly one checkpoint chunk. The
// §4.4 retry budget needs the chunk body re-readable across PUT attempts,
// and ChunkReady must declare the chunk's exact length before the gateway
// signs its grant, so the adapter buffers one whole chunk before it
// declares it. A chunk larger than checkpointBufferMemoryBytes lives in a
// spill file under stagingDir; a smaller one stays on the heap.
type chunkBuffer struct {
	mem  []byte
	path string
	n    int64
}

// readChunk buffers up to max bytes from r into a fresh chunkBuffer,
// spilling to a file under stagingDir when max exceeds memThreshold. It
// returns (buffer, true, err) when at least one byte was read, and
// (nil, false, nil) at a clean end of stream. A non-EOF read error (the
// archive goroutine closed the pipe with its error) is returned so the
// caller can abort the checkpoint.
func readChunk(r io.Reader, max, memThreshold int64, stagingDir string) (*chunkBuffer, bool, error) {
	if max > memThreshold && stagingDir != "" {
		return readChunkToFile(r, max, stagingDir)
	}
	buf := make([]byte, max)
	n, err := io.ReadFull(r, buf)
	if n == 0 {
		if err == io.EOF {
			return nil, false, nil
		}
		if err == io.ErrUnexpectedEOF {
			return nil, false, nil
		}
		return nil, false, err
	}
	// A short read (io.ErrUnexpectedEOF) is the final chunk; a full read
	// (nil) is a full-size chunk. Any other error is the archive failure.
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, false, err
	}
	return &chunkBuffer{mem: buf[:n], n: int64(n)}, true, nil
}

// readChunkToFile spills one chunk to a temp file under stagingDir.
func readChunkToFile(r io.Reader, max int64, stagingDir string) (*chunkBuffer, bool, error) {
	f, err := os.CreateTemp(stagingDir, "checkpoint-chunk-*")
	if err != nil {
		return nil, false, fmt.Errorf("create checkpoint spill file: %w", err)
	}
	path := f.Name()
	n, copyErr := io.CopyN(f, r, max)
	closeErr := f.Close()
	if copyErr != nil && copyErr != io.EOF {
		_ = os.Remove(path)
		return nil, false, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return nil, false, fmt.Errorf("close checkpoint spill file: %w", closeErr)
	}
	if n == 0 {
		_ = os.Remove(path)
		return nil, false, nil
	}
	return &chunkBuffer{path: path, n: n}, true, nil
}

// len returns the chunk's exact byte length, declared to the gateway on
// ChunkReady before the grant is signed.
func (c *chunkBuffer) len() int64 { return c.n }

// reopen returns a fresh reader over the buffered chunk for one PUT
// attempt. The caller closes it. Retries call reopen once per attempt so
// the body is re-read from the start.
func (c *chunkBuffer) reopen() (io.ReadCloser, error) {
	if c.path != "" {
		return os.Open(c.path)
	}
	return io.NopCloser(bytes.NewReader(c.mem)), nil
}

// close removes the spill file, if any. The heap-backed form is a no-op.
func (c *chunkBuffer) close() {
	if c.path != "" {
		_ = os.Remove(c.path)
	}
}

// spillDir is the directory the adapter spills oversized checkpoint
// chunks into. It reuses StagingDir (the pod's workspace staging area);
// an empty StagingDir keeps every chunk on the heap.
func (s *Server) spillDir() string {
	if s.StagingDir == "" {
		return ""
	}
	// A dedicated subdirectory keeps checkpoint spill files from
	// colliding with the upload-staging tree FinalizeWorkspace manages.
	dir := filepath.Join(s.StagingDir, "checkpoint")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return dir
}
