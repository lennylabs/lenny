// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local reliability coverage for the §25.5 polling resume against
// a Redis ops:events:stream held at its retention ceiling.
//
// Every non-tailing Redis read a poll issues carries a deadline of its own, so
// the read surface degrades rather than blocking. The cost of resolving the
// incoming cursor therefore has to be bounded by the position it carries rather
// than by the length of the retained stream: a cursor the Redis source minted
// carries the stream ID it sits at and resumes there directly, while only a
// cursor from another source pays a scan of the retained window. Resolving
// every cursor by scanning turns a stream at its retention ceiling into a
// self-inflicted outage: the resolve outruns the read deadline, the poll
// reports a gap it did not observe, and the request is re-classified onto the
// gateway-buffer fall-back although Redis is healthy.
//
// spec: §25.5 (a redis cursor reads Redis by stream ID; the read surface
// degrades rather than blocking).

package tier7a_load_local_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	gwevents "github.com/lennylabs/lenny/pkg/events"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
)

// redisReadDeadline mirrors the per-request deadline the §25.5 read path puts
// on every non-tailing Redis read. It is restated here because the test asserts
// against the budget an operator observes, rather than against the constant.
const redisReadDeadline = 2 * time.Second

// maxLenEntries is the §25.5 default ops:events:stream retention ceiling
// (events-stream-max-len), the length a busy stream sits at in steady state.
const maxLenEntries = 10000

// perEntryServeCost is the wire and decode cost this fake charges for each
// entry an XRANGE returns. At the retention ceiling a scan of the whole window
// costs maxLenEntries × perEntryServeCost, which is above the read deadline,
// while a positioned resume touches a page of entries and costs a fraction of
// it. The gap between the two is two orders of magnitude, so the assertion does
// not turn on host timing.
const perEntryServeCost = 250 * time.Microsecond

// maxLenStream is a RedisStreamClient serving a stream held at its retention
// ceiling, charging a per-entry cost for everything an XRANGE returns. It is
// what makes the difference between a positioned resume and a whole-window scan
// observable as latency rather than as a call count alone.
type maxLenStream struct {
	entries []redis.XMessage

	mu    sync.Mutex
	scans int
}

func newMaxLenStream(n int) *maxLenStream {
	s := &maxLenStream{entries: make([]redis.XMessage, 0, n)}
	for i := 1; i <= n; i++ {
		ev := gwevents.OperationalEvent{
			ID:          fmt.Sprintf("gw-1:%d:%d", 1700000000000+i, i),
			Type:        "dev.lenny.alert_fired",
			SpecVersion: gwevents.CloudEventsSpecVersion,
			Source:      "//lenny.dev/gateway/1",
			Time:        time.Unix(0, int64(i)).UTC(),
		}
		body, _ := json.Marshal(ev)
		s.entries = append(s.entries, redis.XMessage{
			ID:     fmt.Sprintf("%d-0", i),
			Values: map[string]any{"event": string(body)},
		})
	}
	return s
}

// windowScans reports how many XRANGEs read more than one entry of the whole
// retained window, which is what resolving a cursor by eventKey costs.
func (s *maxLenStream) windowScans() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scans
}

// resetScans drops the recorded count so the test measures one request in
// isolation. The first page is read from the start of the stream, which is a
// whole-window read by construction and says nothing about cursor resolution.
func (s *maxLenStream) resetScans() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scans = 0
}

func (s *maxLenStream) XRangeN(ctx context.Context, _, start, stop string, count int64) *redis.XMessageSliceCmd {
	cmd := redis.NewXMessageSliceCmd(ctx)
	if start == "-" && stop == "+" && count > 1 {
		s.mu.Lock()
		s.scans++
		s.mu.Unlock()
	}
	out := make([]redis.XMessage, 0, 16)
	for _, m := range s.entries {
		if !streamIDInRange(m.ID, start, stop) {
			continue
		}
		out = append(out, m)
		if count > 0 && int64(len(out)) >= int64(count) {
			break
		}
	}
	if err := chargeServeCost(ctx, len(out)); err != nil {
		cmd.SetErr(err)
		return cmd
	}
	cmd.SetVal(out)
	return cmd
}

func (s *maxLenStream) XRevRangeN(ctx context.Context, _, _, _ string, count int64) *redis.XMessageSliceCmd {
	cmd := redis.NewXMessageSliceCmd(ctx)
	out := make([]redis.XMessage, 0, 1)
	for i := len(s.entries) - 1; i >= 0 && (count <= 0 || int64(len(out)) < count); i-- {
		out = append(out, s.entries[i])
	}
	if err := chargeServeCost(ctx, len(out)); err != nil {
		cmd.SetErr(err)
		return cmd
	}
	cmd.SetVal(out)
	return cmd
}

func (s *maxLenStream) TailClient() (opsstream.RedisTailClient, error) {
	return nil, errors.New("the poll path serves this test; no live tail is opened")
}

// chargeServeCost blocks for the cost of serving n entries, honouring the
// caller's deadline the way a real read does.
func chargeServeCost(ctx context.Context, n int) error {
	if n == 0 {
		return nil
	}
	t := time.NewTimer(time.Duration(n) * perEntryServeCost)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// streamIDInRange applies the XRANGE bounds, including the "(" exclusive-start
// prefix the resume uses.
func streamIDInRange(id, start, stop string) bool {
	if start != "-" {
		if strings.HasPrefix(start, "(") {
			if !streamIDBefore(start[1:], id) {
				return false
			}
		} else if streamIDBefore(id, start) {
			return false
		}
	}
	if stop != "+" && streamIDBefore(stop, id) {
		return false
	}
	return true
}

// streamIDBefore orders two "ms-seq" Redis stream IDs.
func streamIDBefore(a, b string) bool {
	ams, aseq := splitRedisStreamID(a)
	bms, bseq := splitRedisStreamID(b)
	if ams != bms {
		return ams < bms
	}
	return aseq < bseq
}

func splitRedisStreamID(id string) (ms, seq uint64) {
	msPart, seqPart, found := strings.Cut(id, "-")
	fmt.Sscanf(msPart, "%d", &ms)
	if found {
		fmt.Sscanf(seqPart, "%d", &seq)
	}
	return ms, seq
}

// pollPageAt drives GET /v1/admin/events with the given cursor as a
// platform-admin read caller and returns the decoded envelope with the wall
// time the request took.
func pollPageAt(t *testing.T, svc *opsstream.Service, cursor string) (map[string]any, time.Duration) {
	t.Helper()
	target := "/v1/admin/events?limit=100"
	if cursor != "" {
		target += "&cursor=" + url.QueryEscape(cursor)
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req = req.WithContext(opsstream.WithReaderScope(req.Context(), "alice@acme.com", "", true))
	rec := httptest.NewRecorder()

	started := time.Now()
	svc.HandlePoll(rec, req)
	elapsed := time.Since(started)

	if rec.Code != http.StatusOK {
		t.Fatalf("poll %s status = %d, want 200; body=%s", target, rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode poll envelope: %v (body=%s)", err, rec.Body.String())
	}
	return body, elapsed
}

// TestOpsEventPollResumesInsideTheReadDeadlineOnAMaxLenStream pins that a
// steady-state poll against a Redis stream sitting at its retention ceiling
// resolves its cursor inside the per-request read deadline and reports no gap.
//
// spec: 25.5 (a redis cursor reads Redis by stream ID; the read surface
// degrades rather than blocking)
// diagnosis: A failure means cursor resolution costs the length of the retained
// stream rather than the position the cursor carries. On a busy stream the
// resolve outruns the per-request read deadline, so a healthy Redis is reported
// as a gap and the request is re-classified onto the gateway-buffer fall-back,
// replaying the whole retained window on every poll.
func TestOpsEventPollResumesInsideTheReadDeadlineOnAMaxLenStream(t *testing.T) {
	stream := newMaxLenStream(maxLenEntries)
	svc := opsstream.New(opsstream.Options{
		RedisClient:  stream,
		SourceHealth: opsstream.StaticSourceHealth{Redis: true, Gateway: true},
	})

	// The first page starts at the oldest retained entry, so it needs no
	// resolve and establishes the continuation cursor.
	first, _ := pollPageAt(t, svc, "")
	cursor := paginationString(t, first, "cursor")
	if cursor == "" {
		t.Fatal("first page returned no continuation cursor")
	}

	// The steady-state poll: resume from the cursor the previous page minted.
	stream.resetScans()
	second, elapsed := pollPageAt(t, svc, cursor)
	if elapsed >= redisReadDeadline {
		t.Errorf("resuming on a %d-entry stream took %s, at or beyond the %s per-request Redis read deadline",
			maxLenEntries, elapsed, redisReadDeadline)
	}
	if gap, _ := second["pagination"].(map[string]any)["gapDetected"].(bool); gap {
		t.Errorf("a healthy Redis stream at its retention ceiling reported gapDetected: %v", second["pagination"])
	}
	if items, _ := second["items"].([]any); len(items) == 0 {
		t.Errorf("the continuation page served no items: %v", second)
	}
	if second["degradation"] != nil {
		t.Errorf("a poll served by a healthy Redis carried a degradation envelope: %v", second["degradation"])
	}
	if n := stream.windowScans(); n != 0 {
		t.Errorf("the poll issued %d whole-window scan(s) against a %d-entry stream; a same-source resume reads by stream ID",
			n, maxLenEntries)
	}
}

// paginationString reads one string field out of the §25.2 pagination envelope.
func paginationString(t *testing.T, body map[string]any, field string) string {
	t.Helper()
	pag, ok := body["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("poll response missing the pagination envelope: %v", body)
	}
	s, _ := pag[field].(string)
	return s
}
