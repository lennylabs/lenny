// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the §25.5 SSE source loop when the live Redis
// tail cannot be established. SourceHealth reports Redis reachable, so the loop
// selects the Redis source, but checking out the per-connection tail client
// fails. The correctness property is a rate one: the connection must not
// re-enter the same unstartable source without bound, because each re-entry
// re-runs the full retained-window resume scan, the head read, and the backlog
// read against Redis while delivering nothing to the client. It must instead
// back off, give up on that source, and fall through to the degraded source
// with the corresponding announcement.
//
// spec: §25.5 (XREAD BLOCK 0 per-connection live tail; the source loop switches
// on a SourceHealth transition or a closed connection; actualSource names the
// source the response was served from).

package tier7a_load_local_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
)

// unstartableTailStream is a §25.5 RedisStreamClient whose range reads succeed
// against an empty stream and whose per-connection tail client can never be
// checked out, the condition a Redis topology exposing no usable tail client
// produces. It counts every range read so the test can bound the scan volume one
// connection issues.
type unstartableTailStream struct{ rangeReads atomic.Int64 }

func (u *unstartableTailStream) XRangeN(ctx context.Context, _, _, _ string, _ int64) *redis.XMessageSliceCmd {
	u.rangeReads.Add(1)
	return redis.NewXMessageSliceCmd(ctx)
}

func (u *unstartableTailStream) XRevRangeN(ctx context.Context, _, _, _ string, _ int64) *redis.XMessageSliceCmd {
	u.rangeReads.Add(1)
	return redis.NewXMessageSliceCmd(ctx)
}

func (u *unstartableTailStream) TailClient() (opsstream.RedisTailClient, error) {
	return nil, errors.New("no per-tail client can be checked out")
}

// blockingRecorder is an http.ResponseWriter that records what the handler
// writes and satisfies http.Flusher, safe for the handler goroutine to write to
// while the test reads.
type blockingRecorder struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *blockingRecorder) Header() http.Header { return http.Header{} }

func (b *blockingRecorder) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *blockingRecorder) WriteHeader(int) {}

func (b *blockingRecorder) Flush() {}

func (b *blockingRecorder) body() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestOpsEventStreamTailUnavailableDoesNotSpinAndFallsBack drives one SSE
// connection against a Redis source whose live tail can never be started and
// asserts the session neither spins on that source nor stalls: the Redis scan
// volume stays bounded over a fixed wall-clock window, and the connection ends
// up announcing the degraded source it fell through to.
//
// spec: 25.5 (XREAD BLOCK 0 per-connection live tail; the SSE source loop
// switches on a SourceHealth transition or a closed connection; the degradation
// envelope's actualSource names the source the response was served from)
// diagnosis: A failure means one SSE connection whose tail cannot be started
// re-enters the Redis source without bound. Each iteration issues a full
// retained-window scan and a head read at Redis and writes nothing to the
// client, so a single connected consumer burns a core and floods Redis with
// full-window scans for as long as it stays connected, silently, since the
// per-connection dedup suppresses any output.
func TestOpsEventStreamTailUnavailableDoesNotSpinAndFallsBack(t *testing.T) {
	stream := &unstartableTailStream{}
	svc := opsstream.New(opsstream.Options{
		RedisClient: stream,
		// Redis reads as reachable, so the matrix selects the Redis source and
		// nothing but the failing tail moves the connection off it. No gateway
		// fan-out source is wired, so the fall-back is this replica's own ring
		// under the dual-outage envelope.
		SourceHealth: opsstream.StaticSourceHealth{Redis: true, Gateway: true},
	})

	// The window is long relative to the bounded backoff, so a session that
	// gives up on the untailable source settles well inside it while a spinning
	// one accumulates scans for its whole duration.
	const window = 3 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), window)
	defer cancel()

	rec := &blockingRecorder{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream", nil)
		svc.HandleStream(rec, platformAdminReq(req.WithContext(ctx)))
	}()

	select {
	case <-done:
	case <-time.After(window + 10*time.Second):
		t.Fatal("HandleStream did not return after its request context was cancelled; the session stalled")
	}

	// Each Redis stint issues a bounded number of range reads (the backlog read
	// and the head read, plus the resume scan once a position is carried), and
	// the session gives up on the source after a small number of consecutive
	// failed tail starts. A generous ceiling still separates that from a hot
	// loop, which issues thousands over the same window.
	const maxRangeReads = 20
	if got := stream.rangeReads.Load(); got > maxRangeReads {
		t.Errorf("connection issued %d Redis range reads over %s, want at most %d; the session is re-entering a source whose tail cannot be started without bound", got, window, maxRangeReads)
	}

	body := rec.body()
	if !strings.Contains(body, sourceOpsLocalBuffer) {
		t.Errorf("connection never announced the degraded source it fell through to; body=%q", body)
	}
}
