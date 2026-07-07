// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: drives the real §7.2/§10.4 client-reconnect-
// with-Last-Event-ID journey against the cmd/lenny-gateway subprocess
// and its real in-memory session event bus
// (pkg/gateway/session/sessionevents), not just the in-process
// httptest handler unit tests in
// pkg/gateway/sessionserver/events_test.go and the tier-7a throughput
// scenarios (which measure load, not per-event replay correctness).
//
// The test opens the SSE events stream, reads two live frames, drops
// the connection (a real client mid-stream disconnect: the request
// context is cancelled and the TCP connection torn down), drives
// further lifecycle transitions while no subscriber is attached, then
// reconnects with the Last-Event-ID header set to the last seq the
// client saw. It asserts the replayed tail is contiguous from that
// cursor: nothing at or before the cursor repeats (no duplicate) and
// no seq in between is skipped (no gap).

package tier4_integration_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// sseFrame is one decoded id/event/data frame from the §15.1 SSE
// stream. Seq is 0 when the frame carries no `id:` line — the gateway
// writes gap_detected / checkpoint_boundary / handoff-reattach markers
// that way (they are stream-control signals, not SessionEvents).
type sseFrame struct {
	Seq  uint64
	Type string
	Data string
}

// decodeStreamingReconnectSSE reads id/event/data frames from r onto
// ch until r returns EOF or an error (closing the response body on
// disconnect causes this). It mirrors the frame grammar
// tests/testinfra/sessiondriver's readSSE decodes for the Kind-backed
// driver; this tier-4 test cannot reuse that helper directly because
// sessiondriver is wired to a kind.Cluster rather than a gateway
// subprocess.
func decodeStreamingReconnectSSE(r io.Reader, ch chan<- sseFrame) {
	defer close(ch)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var cur sseFrame
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if cur.Type != "" {
				ch <- cur
			}
			cur = sseFrame{}
			continue
		}
		switch {
		case strings.HasPrefix(line, "id:"):
			if n, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "id:")), 10, 64); err == nil {
				cur.Seq = n
			}
		case strings.HasPrefix(line, "event:"):
			cur.Type = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			cur.Data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
}

// openStreamingReconnectEvents issues GET /v1/sessions/{id}/events
// against the gateway with the given Last-Event-ID (0 means fresh
// connect) and returns the decoded frame channel plus a cancel func
// that tears down the underlying connection. The caller must call
// cancel to release the connection and must not rely on ch after
// cancel (any frame already in flight on the wire is discarded).
func openStreamingReconnectEvents(t *testing.T, base, id string, lastEventID uint64) (<-chan sseFrame, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/sessions/"+id+"/events", nil)
	if err != nil {
		cancel()
		t.Fatalf("build events request: %v", err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Accept", "text/event-stream")
	if lastEventID > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatUint(lastEventID, 10))
	}
	// SSE has no fixed deadline; a timed client would abort a quiet
	// live tail.
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open events stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		t.Fatalf("events stream status %d: %s", resp.StatusCode, body)
	}
	ch := make(chan sseFrame, 32)
	go func() {
		defer resp.Body.Close()
		decodeStreamingReconnectSSE(resp.Body, ch)
	}()
	return ch, cancel
}

// waitStreamingReconnectFrame reads the next frame off ch, failing the
// test if none arrives within timeout.
func waitStreamingReconnectFrame(t *testing.T, ch <-chan sseFrame, timeout time.Duration) sseFrame {
	t.Helper()
	select {
	case f, ok := <-ch:
		if !ok {
			t.Fatalf("events stream closed before expected frame")
		}
		return f
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for an SSE frame")
	}
	return sseFrame{}
}

// spec: §7.2 (Interactive Session Model) — "Clients that reconnect via
// attach_session ... MAY include resumeFromSeq to receive buffered
// events with SeqNum > resumeFromSeq before live delivery resumes; on
// the SSE transport, the Last-Event-ID request header serves the same
// purpose implicitly."; §10.4 (Gateway Reliability) — "The coordinating
// gateway replica maintains a per-session ring buffer of the most
// recent SessionEvent envelopes ... so that a client that reconnects
// ... with resumeFromSeq ... receives every event with SeqNum >
// resumeFromSeq that is still retained, followed by live delivery."
//
// diagnosis: a failure here means the gateway's SSE reconnect-with-
// cursor path (pkg/gateway/sessionserver/events.go handleEvents) does
// not resume exactly from a real client's Last-Event-ID after a real
// mid-stream disconnect: either it re-sends an event already delivered
// before the drop (a duplicate) or it skips a still-retained seq (a
// spurious gap), rather than replaying precisely the events with
// SeqNum > cursor before resuming live delivery.
func TestStreamingReconnectResumesExactlyFromCursor(t *testing.T) {
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all")
	base := gw.BaseURL()

	post := func(path string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, base+path, nil)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		return resp
	}
	transition := func(id, step string) {
		t.Helper()
		resp := post(fmt.Sprintf("/v1/sessions/%s/%s", id, step))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("/%s: want 200, got %d (body %s)", step, resp.StatusCode, body)
		}
	}

	// Create — the create path publishes no session events, so the live
	// stream opened right after it sees only the finalize/start/
	// interrupt/terminate transitions below.
	createReq, _ := http.NewRequest(http.MethodPost, base+"/v1/sessions",
		strings.NewReader(`{"runtimeRef":"claude-code","userId":"alice@acme.com"}`))
	createReq.Header.Set("X-Lenny-Tenant-ID", "acme")
	createReq.Header.Set("Content-Type", "application/json")
	createResp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	createBody, _ := io.ReadAll(createResp.Body)
	_ = createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create session: want 201, got %d (body %s)", createResp.StatusCode, createBody)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(createBody, &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id := created.ID
	if id == "" {
		t.Fatalf("missing session id")
	}

	// Open the live stream from the start of the session (no cursor)
	// before driving any transitions, so the frames the test reads next
	// are genuine live delivery rather than backlog replay.
	live, cancelLive := openStreamingReconnectEvents(t, base, id, 0)

	transition(id, "finalize") // status_change(ready) — exactly one event.
	f1 := waitStreamingReconnectFrame(t, live, 5*time.Second)
	transition(id, "start") // status_change(running) — exactly one event.
	f2 := waitStreamingReconnectFrame(t, live, 5*time.Second)

	if f2.Seq <= f1.Seq {
		t.Fatalf("expected monotonically increasing seq, got %d then %d", f1.Seq, f2.Seq)
	}
	cursor := f2.Seq

	// Simulate the client dropping the connection mid-stream: cancel the
	// request context, tearing down the TCP connection without a
	// graceful client-side unsubscribe.
	cancelLive()

	// Drive further transitions while no subscriber is attached. The
	// gateway's per-session replay buffer (default depth 512, §10.4)
	// retains these events for the reconnecting client.
	transition(id, "interrupt") // status_change(suspended)
	transition(id, "terminate") // status_change(completed) + session_complete(result)

	// Reconnect with Last-Event-ID set to the last seq the client
	// processed before the drop.
	resumed, cancelResumed := openStreamingReconnectEvents(t, base, id, cursor)
	defer cancelResumed()

	var replayed []sseFrame
	deadline := time.After(5 * time.Second)
collect:
	for {
		select {
		case f, ok := <-resumed:
			if !ok {
				break collect
			}
			replayed = append(replayed, f)
			if f.Type == "session_complete" {
				// The terminate transition's terminal event; the
				// backlog is exhausted and nothing further is expected.
				break collect
			}
		case <-deadline:
			break collect
		}
	}
	cancelResumed()

	if len(replayed) == 0 {
		t.Fatalf("reconnect with Last-Event-ID: %d replayed no events", cursor)
	}

	prev := cursor
	for i, f := range replayed {
		if f.Type == "gap_detected" || f.Type == "checkpoint_boundary" {
			t.Fatalf("reconnect from a still-retained cursor produced a %q marker (frame %d: %+v); "+
				"the default 512-event replay buffer should have covered the two-transition gap", f.Type, i, f)
		}
		if f.Seq <= cursor {
			t.Fatalf("replayed frame %d (%+v) duplicates an event at or before the client's cursor %d", i, f, cursor)
		}
		if f.Seq != prev+1 {
			t.Fatalf("replayed frame %d (%+v) is not contiguous with the previous seq %d: missing seq %d..%d",
				i, f, prev, prev+1, f.Seq-1)
		}
		prev = f.Seq
	}
}
