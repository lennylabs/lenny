// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fixedClock returns a time source stepping by 1ms per read so the
// wall-clock accounting is deterministic and monotonic in tests.
func fixedClock() func() time.Time {
	base := time.Unix(1_700_000_000, 0)
	var mu sync.Mutex
	step := 0
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t := base.Add(time.Duration(step) * time.Millisecond)
		step++
		return t
	}
}

// spec: §4.7 (ReportUsage incremental contract), §11.2 (direct-mode usage)
// A sequence of accumulations reports the correct running total, and each
// default read returns only the tokens folded since the previous read.
func TestSessionUsageMeterDeltaAccounting_spec_4_7(t *testing.T) {
	m := NewSessionUsageMeter(fixedClock())
	const sid = "sess-delta"

	m.Add(sid, 10, 4)
	m.Add(sid, 5, 6)
	u, err := m.Usage(context.Background(), sid)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if u.InputTokens != 15 || u.OutputTokens != 10 {
		t.Fatalf("first delta = (%d,%d), want (15,10)", u.InputTokens, u.OutputTokens)
	}

	// A second read with no new frames reports a zero delta.
	u, _ = m.Usage(context.Background(), sid)
	if u.InputTokens != 0 || u.OutputTokens != 0 {
		t.Fatalf("second delta = (%d,%d), want (0,0)", u.InputTokens, u.OutputTokens)
	}

	// New frames after a read report only the newly-accumulated tokens.
	m.Add(sid, 3, 1)
	u, _ = m.Usage(context.Background(), sid)
	if u.InputTokens != 3 || u.OutputTokens != 1 {
		t.Fatalf("third delta = (%d,%d), want (3,1)", u.InputTokens, u.OutputTokens)
	}
}

// spec: §11.2 (pod-reported cumulative-usage re-report on reconnection)
// A cumulative read returns the session running total and advances the
// watermark to that total, so a reconnected gateway replica that seeds
// its counter from the cumulative total then reads a zero first delta and
// does not double-count the recovered tokens (the §11.2:46 crash-recovery
// MAX rule). This is the corrected recovery behavior; a delta-only meter
// or a meter that did not advance the watermark on the cumulative read
// would re-add the recovered tokens on the next poll.
func TestSessionUsageMeterCumulativeAdvancesWatermark_spec_11_2(t *testing.T) {
	m := NewSessionUsageMeter(fixedClock())
	const sid = "sess-recover"

	m.Add(sid, 100, 40)
	m.Add(sid, 20, 10)

	// A prior steady-state delta read moved the watermark part-way.
	if _, err := m.Usage(context.Background(), sid); err != nil {
		t.Fatalf("pre-crash Usage: %v", err)
	}
	m.Add(sid, 7, 3) // tokens the crashed replica never read

	// The reconnected replica pulls the cumulative total for the MAX rule.
	cum, err := m.Cumulative(context.Background(), sid)
	if err != nil {
		t.Fatalf("Cumulative: %v", err)
	}
	if cum.InputTokens != 127 || cum.OutputTokens != 53 {
		t.Fatalf("cumulative = (%d,%d), want (127,53)", cum.InputTokens, cum.OutputTokens)
	}

	// The first steady-state delta after recovery is zero: the watermark
	// advanced to the cumulative total, so the additive gateway sink does
	// not re-add the recovered tokens (no double-count).
	next, _ := m.Usage(context.Background(), sid)
	if next.InputTokens != 0 || next.OutputTokens != 0 {
		t.Fatalf("post-recovery first delta = (%d,%d), want (0,0)", next.InputTokens, next.OutputTokens)
	}

	// Fresh tokens after recovery flow through as a normal delta.
	m.Add(sid, 5, 2)
	after, _ := m.Usage(context.Background(), sid)
	if after.InputTokens != 5 || after.OutputTokens != 2 {
		t.Fatalf("post-recovery second delta = (%d,%d), want (5,2)", after.InputTokens, after.OutputTokens)
	}
}

// spec: §4.7 (ReportUsage), §11.2 (direct-mode usage)
// A session with no token-bearing frame reports a zero delta and a zero
// cumulative total rather than erroring, so the §11.2 anomaly detector
// observes a legitimate zero-token report.
func TestSessionUsageMeterEmptySessionIsZero_spec_11_2(t *testing.T) {
	m := NewSessionUsageMeter(fixedClock())

	u, err := m.Usage(context.Background(), "never-seen")
	if err != nil {
		t.Fatalf("Usage of unknown session: %v", err)
	}
	if u != (Usage{}) {
		t.Fatalf("unknown-session delta = %+v, want zero", u)
	}

	cum, err := m.Cumulative(context.Background(), "never-seen")
	if err != nil {
		t.Fatalf("Cumulative of unknown session: %v", err)
	}
	if cum != (Usage{}) {
		t.Fatalf("unknown-session cumulative = %+v, want zero", cum)
	}
}

// spec: §4.7 (llm_request_completed token fields)
// Negative token counts on a malformed frame are clamped to zero so a bad
// frame cannot decrement a session's cumulative total.
func TestSessionUsageMeterClampsNegative_spec_4_7(t *testing.T) {
	m := NewSessionUsageMeter(fixedClock())
	const sid = "sess-neg"
	m.Add(sid, 10, 5)
	m.Add(sid, -100, -100)
	u, _ := m.Usage(context.Background(), sid)
	if u.InputTokens != 10 || u.OutputTokens != 5 {
		t.Fatalf("delta after negative frame = (%d,%d), want (10,5)", u.InputTokens, u.OutputTokens)
	}
}

// spec: §4.7 (ReportUsage), §11.2 (direct-mode usage)
// The ReportUsage handler passes ReportUsageRequest.cumulative through:
// unset returns the incremental delta, set returns the session cumulative
// total. This pins the S3 wiring of req.GetCumulative() through the
// handler; before this step the handler had no cumulative branch.
func TestReportUsageHandlerPassesCumulativeFlag_spec_11_2(t *testing.T) {
	s := New("served")
	m := NewSessionUsageMeter(fixedClock())
	s.Usage = m
	s.mu.Lock()
	s.sessionID = "sess-h"
	s.mu.Unlock()
	m.Add("sess-h", 30, 12)

	ctx := context.Background()
	req := &adapterv1.ReportUsageRequest{SessionId: &adapterv1.SessionId{Value: "sess-h"}}

	// Default (delta) read drains the accumulation.
	resp, err := s.ReportUsage(ctx, req)
	if err != nil {
		t.Fatalf("ReportUsage delta: %v", err)
	}
	if resp.InputTokens != 30 || resp.OutputTokens != 12 {
		t.Fatalf("delta resp = (%d,%d), want (30,12)", resp.InputTokens, resp.OutputTokens)
	}

	// A cumulative read after more accumulation returns the running total.
	m.Add("sess-h", 5, 3)
	cumReq := &adapterv1.ReportUsageRequest{
		SessionId:  &adapterv1.SessionId{Value: "sess-h"},
		Cumulative: true,
	}
	cumResp, err := s.ReportUsage(ctx, cumReq)
	if err != nil {
		t.Fatalf("ReportUsage cumulative: %v", err)
	}
	if cumResp.InputTokens != 35 || cumResp.OutputTokens != 15 {
		t.Fatalf("cumulative resp = (%d,%d), want (35,15)", cumResp.InputTokens, cumResp.OutputTokens)
	}
}

// spec: §4.7 (ReportUsage)
// With a usage meter wired, ReportUsage no longer returns Unimplemented
// (F-15.3.7: the handler previously returned Unimplemented for every
// shipped runtime because no production meter was set). This asserts the
// corrected outcome: a configured meter yields a real accounting.
func TestReportUsageWithMeterIsImplemented_spec_4_7(t *testing.T) {
	s := New("served")
	s.Usage = NewSessionUsageMeter(fixedClock())
	s.mu.Lock()
	s.sessionID = "sess-impl"
	s.mu.Unlock()

	_, err := s.ReportUsage(context.Background(),
		&adapterv1.ReportUsageRequest{SessionId: &adapterv1.SessionId{Value: "sess-impl"}})
	if err != nil {
		t.Fatalf("ReportUsage with meter set: %v", err)
	}
	if status.Code(err) == codes.Unimplemented {
		t.Fatalf("ReportUsage returned Unimplemented with a meter configured")
	}
}

// spec: §4.7 (ReportUsage), §11.2 (direct-mode usage)
// WireDirectModeUsage is the single production wiring point
// cmd/lenny-adapter calls during server assembly (F-15.3.7). It must
// install the meter on the server so ReportUsage stops returning
// Unimplemented, and, when a lifecycle channel is present, wire the token
// sink onto that channel so llm_request_completed frames fold. This pins
// both effects; a nil-channel call still installs the meter (the
// Basic/Standard path).
func TestWireDirectModeUsageInstallsMeterAndSink_spec_11_2(t *testing.T) {
	// Nil lifecycle channel: the meter is still installed so ReportUsage is
	// implemented, and no sink is wired (the Basic/Standard path).
	sNoLC := New("served")
	meter := WireDirectModeUsage(sNoLC, nil)
	if meter == nil {
		t.Fatal("WireDirectModeUsage returned a nil meter")
	}
	if sNoLC.Usage == nil {
		t.Fatal("WireDirectModeUsage did not install the meter on s.Usage; ReportUsage would return Unimplemented")
	}

	// With a lifecycle channel, the sink is wired onto it before Run, and a
	// completed-LLM frame folds its tokens into the wired meter under the
	// pod's current session.
	s := New("served")
	s.mu.Lock()
	s.sessionID = "sess-wire"
	s.mu.Unlock()
	lc, err := NewLifecycleChannel(shortSocketName(t, "wire.sock"))
	if err != nil {
		t.Fatalf("NewLifecycleChannel: %v", err)
	}
	t.Cleanup(func() { _ = lc.Close() })
	m := WireDirectModeUsage(s, lc)
	if s.Usage == nil {
		t.Fatal("WireDirectModeUsage did not install the meter with a lifecycle channel present")
	}
	if lc.usage == nil {
		t.Fatal("WireDirectModeUsage did not wire the token sink onto the lifecycle channel")
	}

	// The wired sink folds into the returned meter under the current session.
	lc.usage.AddTokens(11, 4)
	u, err := m.Cumulative(context.Background(), "sess-wire")
	if err != nil {
		t.Fatalf("Cumulative: %v", err)
	}
	if u.InputTokens != 11 || u.OutputTokens != 4 {
		t.Fatalf("wired-sink fold = (%d,%d), want (11,4)", u.InputTokens, u.OutputTokens)
	}
}

// spec: §4.7 (ReportUsage)
// Without a meter the handler still returns Unimplemented (the nil-meter
// fail path), so the Basic/Standard adapter reports the capability absent
// rather than a fabricated zero.
func TestReportUsageWithoutMeterIsUnimplemented_spec_4_7(t *testing.T) {
	s := New("served")
	s.mu.Lock()
	s.sessionID = "sess-nomtr"
	s.mu.Unlock()

	_, err := s.ReportUsage(context.Background(),
		&adapterv1.ReportUsageRequest{SessionId: &adapterv1.SessionId{Value: "sess-nomtr"}})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("ReportUsage without meter = %v, want Unimplemented", status.Code(err))
	}
}

// spec: §4.7 (ReportUsage)
// An empty session id is rejected before the meter is consulted.
func TestReportUsageRejectsEmptySession_spec_4_7(t *testing.T) {
	s := New("served")
	s.Usage = NewSessionUsageMeter(fixedClock())
	_, err := s.ReportUsage(context.Background(),
		&adapterv1.ReportUsageRequest{SessionId: &adapterv1.SessionId{Value: ""}})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ReportUsage empty session = %v, want InvalidArgument", status.Code(err))
	}
}

// spec: §4.7 (llm_request_completed token fields), §11.2 (direct-mode usage)
// The session token sink folds counts into the pod's current session and
// drops a frame that arrives while the pod is idle (no assigned session).
func TestSessionTokenSinkResolvesCurrentSession_spec_4_7(t *testing.T) {
	m := NewSessionUsageMeter(fixedClock())
	current := "sess-live"
	sink := NewSessionTokenSink(m, func() string { return current })

	sink.AddTokens(8, 3)
	u, _ := m.Usage(context.Background(), "sess-live")
	if u.InputTokens != 8 || u.OutputTokens != 3 {
		t.Fatalf("live-session fold = (%d,%d), want (8,3)", u.InputTokens, u.OutputTokens)
	}

	// A frame while the pod is idle is dropped (no session to attribute).
	current = ""
	sink.AddTokens(99, 99)
	if idle, _ := m.Usage(context.Background(), ""); idle != (Usage{}) {
		t.Fatalf("idle-session fold recorded tokens: %+v", idle)
	}
}

// spec: §11.2 (direct-mode usage)
// Concurrent llm_request_completed folds and gateway ReportUsage pulls on
// the same session are mutex-guarded and total correctly. This is a smoke
// -race check; the flake-budget concurrency assertion lives in the tier-7a
// stress test (S13).
func TestSessionUsageMeterConcurrentFoldRaceSmoke_spec_11_2(t *testing.T) {
	m := NewSessionUsageMeter(fixedClock())
	const sid = "sess-race"
	const adders = 8
	const perAdder = 100

	var wg sync.WaitGroup
	wg.Add(adders + 1)
	for i := 0; i < adders; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perAdder; j++ {
				m.Add(sid, 1, 1)
			}
		}()
	}
	// A concurrent reader draining deltas must not race the folds.
	got := int64(0)
	go func() {
		defer wg.Done()
		for j := 0; j < perAdder; j++ {
			u, _ := m.Usage(context.Background(), sid)
			got += u.InputTokens
		}
	}()
	wg.Wait()

	// Drain the remainder; the total across every read equals the folds.
	final, _ := m.Usage(context.Background(), sid)
	got += final.InputTokens
	if want := int64(adders * perAdder); got != want {
		t.Fatalf("concurrent total input = %d, want %d", got, want)
	}
}
