// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/redis/go-redis/v9"
)

// fakeStreamReader is an in-memory StreamReader: an ordered slice of
// XMessages the test appends to, with XRANGE/XREVRANGE semantics over
// string ids of the form "<n>-0".
type fakeStreamReader struct {
	msgs    []redis.XMessage
	rangeN  int
	rangeFn func()
	err     error
}

func (f *fakeStreamReader) add(id, eventJSON string) {
	f.msgs = append(f.msgs, redis.XMessage{ID: id, Values: map[string]any{"event": eventJSON}})
}

func (f *fakeStreamReader) XRevRangeN(_ context.Context, _, _, _ string, _ int64) *redis.XMessageSliceCmd {
	cmd := redis.NewXMessageSliceCmd(context.Background())
	if f.err != nil {
		cmd.SetErr(f.err)
		return cmd
	}
	if len(f.msgs) == 0 {
		cmd.SetVal(nil)
		return cmd
	}
	cmd.SetVal([]redis.XMessage{f.msgs[len(f.msgs)-1]})
	return cmd
}

func (f *fakeStreamReader) XRangeN(_ context.Context, _, start, _ string, _ int64) *redis.XMessageSliceCmd {
	f.rangeN++
	if f.rangeFn != nil {
		f.rangeFn()
	}
	cmd := redis.NewXMessageSliceCmd(context.Background())
	if f.err != nil {
		cmd.SetErr(f.err)
		return cmd
	}
	// start is "(<id>" exclusive; return entries strictly after it.
	exclusive := start
	if len(start) > 0 && start[0] == '(' {
		exclusive = start[1:]
	}
	var out []redis.XMessage
	for _, m := range f.msgs {
		if m.ID > exclusive {
			out = append(out, m)
		}
	}
	cmd.SetVal(out)
	return cmd
}

// spec: §25.5 lines 2592-2611 — the first Poll snaps the cursor to the
// stream tail so a cold start does not replay the backlog to webhook
// subscriptions.
func TestRedisEventSourcePrimesToTail_spec_25_5(t *testing.T) {
	f := &fakeStreamReader{}
	f.add("1-0", `{"id":"gw:1:1","type":"dev.lenny.alert_fired"}`)
	f.add("2-0", `{"id":"gw:1:2","type":"dev.lenny.pool_transition"}`)
	src := NewRedisEventSource(f, "ops:events:stream")

	got, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("first Poll returned %d events; want 0 (cursor priming)", len(got))
	}
}

// spec: §25.3 lines 665-667 — events appended after priming are
// delivered exactly once, carrying the CloudEvents id and type.
func TestRedisEventSourceDeliversNewEvents_spec_25_3(t *testing.T) {
	f := &fakeStreamReader{}
	f.add("1-0", `{"id":"gw:1:1","type":"dev.lenny.alert_fired"}`)
	src := NewRedisEventSource(f, "ops:events:stream")

	if _, err := src.Poll(context.Background()); err != nil {
		t.Fatalf("prime Poll: %v", err)
	}
	f.add("2-0", `{"id":"gw:1:2","type":"dev.lenny.pool_transition"}`)
	f.add("3-0", `{"id":"gw:1:3","type":"dev.lenny.escalation_created"}`)

	got, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Poll returned %d events; want 2", len(got))
	}
	if got[0].ID != "gw:1:2" || got[0].Type != "dev.lenny.pool_transition" {
		t.Errorf("event[0] = %+v; want id gw:1:2 / pool_transition", got[0])
	}
	if got[1].Type != "dev.lenny.escalation_created" {
		t.Errorf("event[1].Type = %q; want escalation_created", got[1].Type)
	}
	// The body must round-trip the full CloudEvents record.
	var ce map[string]any
	if err := json.Unmarshal(got[0].Body, &ce); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if ce["id"] != "gw:1:2" {
		t.Errorf("body id = %v; want gw:1:2", ce["id"])
	}

	// A follow-up Poll with nothing new returns no events and does not
	// re-deliver.
	again, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("idempotent Poll: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second Poll returned %d events; want 0 (no re-delivery)", len(again))
	}
}

// TestRedisEventSourceParsesTenantID pins the §25.5 delivery-time tenant
// label: decode reads the lennytenantid CloudEvents extension into the
// WebhookEvent so the worker can gate delivery on it. A labeled event
// carries its tenant; an event with no lennytenantid is platform-scoped
// (empty TenantID). Against the pre-fix decode, which never read the
// extension, the labeled-event assertion fails. spec: 25.5 (delivery-time
// tenantFilter, platform-scoped rule)
func TestRedisEventSourceParsesTenantID(t *testing.T) {
	f := &fakeStreamReader{}
	src := NewRedisEventSource(f, "")
	if _, err := src.Poll(context.Background()); err != nil {
		t.Fatalf("prime Poll: %v", err)
	}
	f.add("1-0", `{"id":"gw:1:1","type":"dev.lenny.alert_fired","lennytenantid":"acme"}`)
	f.add("2-0", `{"id":"ops:1:1","type":"dev.lenny.platform_upgrade_started"}`)

	got, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Poll returned %d events; want 2", len(got))
	}
	if got[0].TenantID != "acme" {
		t.Errorf("labeled event TenantID = %q; want acme", got[0].TenantID)
	}
	if got[1].TenantID != "" {
		t.Errorf("platform-scoped event TenantID = %q; want empty", got[1].TenantID)
	}
}

// spec: §25.5 — a malformed stream entry is skipped, not fatal; the
// cursor still advances past it so it is never retried.
func TestRedisEventSourceSkipsMalformed_spec_25_5(t *testing.T) {
	f := &fakeStreamReader{}
	src := NewRedisEventSource(f, "")
	if _, err := src.Poll(context.Background()); err != nil {
		t.Fatalf("prime Poll: %v", err)
	}
	f.add("1-0", `{not json`)
	f.msgs = append(f.msgs, redis.XMessage{ID: "2-0", Values: map[string]any{"other": "x"}})
	f.add("3-0", `{"id":"gw:1:9","type":"dev.lenny.drift_detected"}`)

	got, err := src.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Poll returned %d events; want 1 (two skipped)", len(got))
	}
	if got[0].Type != "dev.lenny.drift_detected" {
		t.Errorf("event.Type = %q; want drift_detected", got[0].Type)
	}
}

// spec: §25.5 — a Redis read error propagates so the worker tick can
// surface it; the cursor is not advanced on a failed read.
func TestRedisEventSourcePropagatesError_spec_25_5(t *testing.T) {
	f := &fakeStreamReader{err: redis.ErrClosed}
	src := NewRedisEventSource(f, "ops:events:stream")
	if _, err := src.Poll(context.Background()); err == nil {
		t.Fatal("prime Poll: want error, got nil")
	}
}
