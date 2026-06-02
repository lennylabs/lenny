// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// F-7.4.15: publishWorkspaceWarnings emits one SSE workspace_plan_warning
// frame per §14 advisory the adapter returned from FinalizeWorkspace.
// spec: §7.4 line 459.
func TestPublishWorkspaceWarnings_EmitsOnePerWarning_spec_7_4_15(t *testing.T) {
	bus := sessionevents.NewBus(0)
	srv := New(memstore.New(), Options{Events: bus})

	result := &podsession.BindResult{
		TenantID:  "default",
		SessionID: "sess_w",
		WorkspacePlanWarnings: []*adapterv1.WorkspacePlanWarning{
			{Code: "workspace_plan_strip_components_skip", SourceIndex: 1, EntryPath: "a.txt", SegmentCount: 1, StripComponents: 2, Message: "skipped a.txt"},
			{Code: "workspace_plan_strip_components_skip", SourceIndex: 1, EntryPath: "b.txt", SegmentCount: 1, StripComponents: 2, Message: "skipped b.txt"},
		},
	}
	srv.publishWorkspaceWarnings(result)

	events := bus.History("sess_w", 0)
	if len(events) != 2 {
		t.Fatalf("events: got %d, want 2; events=%+v", len(events), events)
	}
	for i, ev := range events {
		if ev.Type != "workspace_plan_warning" {
			t.Errorf("event[%d].Type = %q, want workspace_plan_warning", i, ev.Type)
		}
	}
}

// F-7.4.15: a nil result or empty warnings is a no-op.
func TestPublishWorkspaceWarnings_NilSafe_spec_7_4_15(t *testing.T) {
	bus := sessionevents.NewBus(0)
	srv := New(memstore.New(), Options{Events: bus})

	srv.publishWorkspaceWarnings(nil)
	srv.publishWorkspaceWarnings(&podsession.BindResult{TenantID: "default", SessionID: "sess_w"})

	if len(bus.History("sess_w", 0)) != 0 {
		t.Errorf("expected no events for nil/empty warnings")
	}
}

// F-14.1.18: spec §14 line 100 — the strip-components-skip SSE event
// carries `entryPath`, `segmentCount`, and `stripComponents` so a
// consumer that matches on these structured fields can extract them
// without parsing the human-readable message.
func TestPublishWorkspaceWarnings_CarriesStructuredFields_spec_14_100(t *testing.T) {
	bus := sessionevents.NewBus(0)
	srv := New(memstore.New(), Options{Events: bus})

	result := &podsession.BindResult{
		TenantID:  "default",
		SessionID: "sess_w",
		WorkspacePlanWarnings: []*adapterv1.WorkspacePlanWarning{{
			Code:            "workspace_plan_strip_components_skip",
			SourceIndex:     1,
			EntryPath:       "readme.md",
			SegmentCount:    1,
			StripComponents: 2,
			Message:         "entry has 1 segment(s); fewer than stripComponents=2",
		}},
	}
	srv.publishWorkspaceWarnings(result)

	events := bus.History("sess_w", 0)
	if len(events) != 1 {
		t.Fatalf("events: got %d, want 1", len(events))
	}
	payload := decodeWarningPayload(t, events[0].Data)
	if got, want := payload["entryPath"], "readme.md"; got != want {
		t.Errorf("entryPath = %v, want %v", got, want)
	}
	// JSON numbers decode to float64; assert numerically.
	if got := payload["segmentCount"]; got != float64(1) {
		t.Errorf("segmentCount = %v, want 1", got)
	}
	if got := payload["stripComponents"]; got != float64(2) {
		t.Errorf("stripComponents = %v, want 2", got)
	}
}

// F-14.1.9: spec §14 line 338 — the materialization-time
// workspace_plan_path_collision warning is published on the per-session
// SSE bus with `path`, `winningSourceIndex`, and `losingSourceIndex`
// structured fields. Other warning codes (empty path) do not leak them.
func TestPublishWorkspaceWarnings_PathCollisionFields_spec_14_338(t *testing.T) {
	bus := sessionevents.NewBus(0)
	srv := New(memstore.New(), Options{Events: bus})

	result := &podsession.BindResult{
		TenantID:  "default",
		SessionID: "sess_c",
		WorkspacePlanWarnings: []*adapterv1.WorkspacePlanWarning{
			{
				Code:               "workspace_plan_path_collision",
				SourceIndex:        2,
				Path:               "foo/bar.txt",
				WinningSourceIndex: 2,
				LosingSourceIndex:  0,
				Message:            "path overwrite",
			},
			{
				Code:            "workspace_plan_strip_components_skip",
				SourceIndex:     1,
				EntryPath:       "x.txt",
				SegmentCount:    1,
				StripComponents: 2,
				Message:         "skipped x.txt",
			},
		},
	}
	srv.publishWorkspaceWarnings(result)

	events := bus.History("sess_c", 0)
	if len(events) != 2 {
		t.Fatalf("events: got %d, want 2", len(events))
	}
	coll := decodeWarningPayload(t, events[0].Data)
	if got := coll["path"]; got != "foo/bar.txt" {
		t.Errorf("path = %v, want foo/bar.txt", got)
	}
	if got := coll["winningSourceIndex"]; got != float64(2) {
		t.Errorf("winningSourceIndex = %v, want 2", got)
	}
	if got := coll["losingSourceIndex"]; got != float64(0) {
		t.Errorf("losingSourceIndex = %v, want 0", got)
	}
	// The strip-components warning (empty path) must not carry the
	// collision-only fields.
	skip := decodeWarningPayload(t, events[1].Data)
	if _, ok := skip["winningSourceIndex"]; ok {
		t.Errorf("strip-components payload leaked winningSourceIndex")
	}
	if _, ok := skip["path"]; ok {
		t.Errorf("strip-components payload leaked path")
	}
}

// F-14.1.17 / F-14.1.18: spec §14 line 334 — the parse-time
// unknown-source-type warning is published on the per-session SSE
// bus with `schemaVersion` and `unknownType` structured fields.
func TestPublishParsePlanWarnings_UnknownSourceType_spec_14_334(t *testing.T) {
	bus := sessionevents.NewBus(0)
	srv := New(memstore.New(), Options{Events: bus})

	sv := 1
	srv.publishParsePlanWarnings("default", "sess_p", []workspaceplan.Warning{{
		Code:          workspaceplan.WarnUnknownSourceType,
		SourceIndex:   0,
		Field:         "type",
		SchemaVersion: &sv,
		UnknownType:   "ferrousMode",
		Message:       "unknown source type",
	}})

	events := bus.History("sess_p", 0)
	if len(events) != 1 {
		t.Fatalf("events: got %d, want 1", len(events))
	}
	if events[0].Type != "workspace_plan_warning" {
		t.Errorf("event.Type = %q, want workspace_plan_warning", events[0].Type)
	}
	payload := decodeWarningPayload(t, events[0].Data)
	if got := payload["code"]; got != string(workspaceplan.WarnUnknownSourceType) {
		t.Errorf("code = %v, want %v", got, workspaceplan.WarnUnknownSourceType)
	}
	if got := payload["schemaVersion"]; got != float64(1) {
		t.Errorf("schemaVersion = %v, want 1", got)
	}
	if got := payload["unknownType"]; got != "ferrousMode" {
		t.Errorf("unknownType = %v, want ferrousMode", got)
	}
	if _, ok := payload["winningSourceIndex"]; ok {
		t.Errorf("payload leaked winningSourceIndex on unknown-source-type warning")
	}
}

// F-14.1.17 / F-14.1.18: spec §14 line 338 — the parse-time
// path-collision warning is published on the per-session SSE bus with
// `path`, `winningSourceIndex`, and `losingSourceIndex` structured
// fields.
func TestPublishParsePlanWarnings_PathCollision_spec_14_338(t *testing.T) {
	bus := sessionevents.NewBus(0)
	srv := New(memstore.New(), Options{Events: bus})

	win := 1
	lose := 0
	srv.publishParsePlanWarnings("default", "sess_c", []workspaceplan.Warning{{
		Code:               workspaceplan.WarnPathCollision,
		SourceIndex:        1,
		Field:              "path",
		Path:               "a/b.txt",
		WinningSourceIndex: &win,
		LosingSourceIndex:  &lose,
		Message:            "collision",
	}})

	events := bus.History("sess_c", 0)
	if len(events) != 1 {
		t.Fatalf("events: got %d, want 1", len(events))
	}
	payload := decodeWarningPayload(t, events[0].Data)
	if got := payload["path"]; got != "a/b.txt" {
		t.Errorf("path = %v, want a/b.txt", got)
	}
	if got := payload["winningSourceIndex"]; got != float64(1) {
		t.Errorf("winningSourceIndex = %v, want 1", got)
	}
	if got := payload["losingSourceIndex"]; got != float64(0) {
		t.Errorf("losingSourceIndex = %v, want 0", got)
	}
	if _, ok := payload["unknownType"]; ok {
		t.Errorf("payload leaked unknownType on path-collision warning")
	}
}

// F-14.1.17: empty warnings or empty session id is a no-op so a
// fabricated short-circuit cannot leak an event.
func TestPublishParsePlanWarnings_NoOpOnEmpty_spec_14(t *testing.T) {
	bus := sessionevents.NewBus(0)
	srv := New(memstore.New(), Options{Events: bus})

	srv.publishParsePlanWarnings("default", "sess_e", nil)
	srv.publishParsePlanWarnings("default", "", []workspaceplan.Warning{{Code: workspaceplan.WarnPathCollision}})

	if len(bus.History("sess_e", 0)) != 0 {
		t.Errorf("expected no events for nil warnings slice")
	}
}

// F-14.1.17: spec §14 lines 100/334/338 — each warning is also
// published on the §16.6 / §25.3 operational-event stream so Ops
// consoles, audit pipelines, and AI DevOps agents (per §25) see them
// without subscribing to the per-session SSE feed.
func TestPublishParsePlanWarnings_EmitsOpsEvent_spec_25_3(t *testing.T) {
	buf := events.NewEventBuffer(0)
	emitter := events.NewEmitter(buf, "test-replica")
	bus := sessionevents.NewBus(0)
	srv := New(memstore.New(), Options{Events: bus, OpsEmitter: emitter})

	sv := 1
	srv.publishParsePlanWarnings("default", "sess_o", []workspaceplan.Warning{{
		Code:          workspaceplan.WarnUnknownSourceType,
		SourceIndex:   0,
		Field:         "type",
		SchemaVersion: &sv,
		UnknownType:   "ferrousMode",
		Message:       "unknown source type",
	}})

	page := buf.Query(0, events.EventFilter{}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("ops events: got %d, want 1", len(page.Events))
	}
	ev := page.Events[0].Event
	if want := events.EventWorkspacePlanWarning.CloudEventsType(); ev.Type != want {
		t.Errorf("ev.Type = %q, want %q", ev.Type, want)
	}
	if ev.Subject != "session/sess_o" {
		t.Errorf("ev.Subject = %q, want session/sess_o", ev.Subject)
	}
	// Payload should preserve the structured fields.
	var data map[string]any
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatalf("decode ev.Data: %v", err)
	}
	if data["unknownType"] != "ferrousMode" {
		t.Errorf("data.unknownType = %v, want ferrousMode", data["unknownType"])
	}
	if data["schemaVersion"] != float64(1) {
		t.Errorf("data.schemaVersion = %v, want 1", data["schemaVersion"])
	}
}

// F-14.1.17: the strip-components-skip warning is also republished on
// the §16.6 / §25.3 operational-event stream from the FinalizeWorkspace
// response.
func TestPublishWorkspaceWarnings_EmitsOpsEvent_spec_25_3(t *testing.T) {
	buf := events.NewEventBuffer(0)
	emitter := events.NewEmitter(buf, "test-replica")
	bus := sessionevents.NewBus(0)
	srv := New(memstore.New(), Options{Events: bus, OpsEmitter: emitter})

	result := &podsession.BindResult{
		TenantID:  "default",
		SessionID: "sess_strip",
		WorkspacePlanWarnings: []*adapterv1.WorkspacePlanWarning{{
			Code:            "workspace_plan_strip_components_skip",
			SourceIndex:     1,
			EntryPath:       "readme.md",
			SegmentCount:    1,
			StripComponents: 2,
			Message:         "skipped",
		}},
	}
	srv.publishWorkspaceWarnings(result)

	page := buf.Query(0, events.EventFilter{}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("ops events: got %d, want 1", len(page.Events))
	}
	ev := page.Events[0].Event
	if want := events.EventWorkspacePlanWarning.CloudEventsType(); ev.Type != want {
		t.Errorf("ev.Type = %q, want %q", ev.Type, want)
	}
	if ev.Subject != "session/sess_strip" {
		t.Errorf("ev.Subject = %q, want session/sess_strip", ev.Subject)
	}
}

// decodeWarningPayload extracts the map payload from a published SSE
// event's data string. The fixture publishes JSON; this helper avoids
// re-decoding boilerplate in each assertion.
func decodeWarningPayload(t *testing.T, data string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("decode payload: %v\n%s", err, data)
	}
	return m
}

// silence unused import warning.
var _ = context.Background
