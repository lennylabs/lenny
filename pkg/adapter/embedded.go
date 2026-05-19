// SPDX-License-Identifier: MIT

package adapter

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"
)

// RuntimeLoop is the §15.4.1 JSONL processing loop a first-party runtime
// implements for the §4.7 embedded deployment model. It is the same
// stdin/stdout contract a sidecar runtime implements, expressed as a
// function: in carries inbound frames (adapter → runtime), out carries
// outbound frames (runtime → adapter). The loop returns when in reaches
// EOF (the §15.4 clean-exit signal) or it hits an unrecoverable error.
//
// A runtime that already has a `run(stdin io.Reader, stdout io.Writer)`
// entry point — every reference runtime does — satisfies RuntimeLoop
// with no change: the embedded entrypoint passes the in-process pipe
// ends as stdin and stdout.
type RuntimeLoop func(ctx context.Context, in io.Reader, out io.Writer) error

// InProcessRuntime is the §4.7 embedded-model RuntimeProcess: the
// runtime logic runs in the adapter's own process, and the §15.4.1
// JSONL frames travel over an in-memory pipe instead of a socket or the
// runtime's stdin/stdout. It is the third transport behind the
// RuntimeProcess contract, beside the stdin/stdout SubprocessExecutor
// and the abstract-socket SocketRuntimeProcess: the framing is
// identical, only the byte transport differs.
//
// The embedded model has no separate adapter container and no
// adapter↔runtime trust boundary — the runtime is first-party and
// linked into the adapter binary — so InProcessRuntime carries none of
// the SO_PEERCRED or nonce-handshake machinery. The §4.7 trade-off
// table is explicit that the embedded model trades that process
// isolation for a single process with shared memory.
type InProcessRuntime struct {
	loop RuntimeLoop

	mu      sync.Mutex
	session string
	// inWriter is the adapter's write end: WriteEnvelope writes frames
	// the runtime loop reads.
	inWriter *io.PipeWriter
	// outReader is the adapter's read end: Output streams frames the
	// runtime loop wrote.
	outReader *io.PipeReader
	loopDone  chan struct{}
}

// NewInProcessRuntime returns an embedded-model RuntimeProcess that runs
// loop in-process. loop is the runtime's §15.4.1 JSONL handler.
func NewInProcessRuntime(loop RuntimeLoop) *InProcessRuntime {
	return &InProcessRuntime{loop: loop}
}

// Start launches the runtime loop for the session. It wires an
// in-memory pipe pair between the adapter and the loop and runs the
// loop in a goroutine. Start is idempotent for the same session.
func (r *InProcessRuntime) Start(ctx context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session == sessionID && r.inWriter != nil {
		return nil
	}
	if r.session != "" && r.session != sessionID {
		return fmt.Errorf("adapter: embedded runtime already bound to session %s", r.session)
	}
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	r.session = sessionID
	r.inWriter = inWriter
	r.outReader = outReader
	r.loopDone = make(chan struct{})
	done := r.loopDone

	go func() {
		defer close(done)
		err := r.loop(ctx, inReader, outWriter)
		// Closing the write end signals §15.4 output EOF to Output;
		// closing the read end unblocks any pending WriteEnvelope.
		_ = outWriter.CloseWithError(err)
		_ = inReader.CloseWithError(io.EOF)
	}()
	return nil
}

// WriteEnvelope forwards a pre-encoded §15.4.1 message envelope to the
// runtime loop over the in-memory pipe, terminated by a newline.
func (r *InProcessRuntime) WriteEnvelope(sessionID string, envelope []byte) error {
	r.mu.Lock()
	w := r.inWriter
	bound := r.session
	r.mu.Unlock()
	if bound != sessionID {
		return fmt.Errorf("adapter: session %s is not bound to this embedded runtime", sessionID)
	}
	if w == nil {
		return fmt.Errorf("adapter: embedded runtime for session %s is not started", sessionID)
	}
	if _, err := w.Write(append(envelope, '\n')); err != nil {
		return fmt.Errorf("adapter: write envelope to embedded runtime: %w", err)
	}
	return nil
}

// Output streams every §15.4.1 JSONL frame the runtime loop writes. The
// channel closes when the loop returns; ctx cancellation stops the
// reader so a stalled consumer does not leak the goroutine.
func (r *InProcessRuntime) Output(ctx context.Context, sessionID string) (<-chan []byte, error) {
	r.mu.Lock()
	reader := r.outReader
	bound := r.session
	r.mu.Unlock()
	if bound != sessionID {
		return nil, fmt.Errorf("adapter: session %s is not bound to this embedded runtime", sessionID)
	}
	if reader == nil {
		return nil, fmt.Errorf("adapter: embedded runtime for session %s is not started", sessionID)
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	ch := make(chan []byte)
	go func() {
		defer close(ch)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case ch <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// Interrupt stops the runtime loop. The embedded runtime shares the
// adapter's process, so there is no signal to deliver: Interrupt closes
// the inbound pipe, which the loop observes as §15.4 stdin EOF and
// exits. hard is accepted for RuntimeProcess parity; the embedded loop
// has a single termination path.
func (r *InProcessRuntime) Interrupt(_ context.Context, sessionID string, _ bool) error {
	r.mu.Lock()
	w := r.inWriter
	bound := r.session
	r.mu.Unlock()
	if bound != sessionID {
		return fmt.Errorf("adapter: session %s is not bound to this embedded runtime", sessionID)
	}
	if w != nil {
		_ = w.Close()
	}
	return nil
}

// Close stops the runtime loop and waits briefly for it to return. It
// closes the inbound pipe (the §15.4 clean-exit signal) and drains the
// loop's completion.
func (r *InProcessRuntime) Close(_ context.Context, sessionID string) error {
	r.mu.Lock()
	w := r.inWriter
	done := r.loopDone
	r.inWriter = nil
	r.outReader = nil
	r.loopDone = nil
	r.session = ""
	r.mu.Unlock()
	if w != nil {
		_ = w.Close()
	}
	if done != nil {
		<-done
	}
	return nil
}

// compile-time assertion that InProcessRuntime satisfies the
// RuntimeProcess contract the §4.7 adapter drives.
var _ RuntimeProcess = (*InProcessRuntime)(nil)
