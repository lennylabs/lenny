// SPDX-License-Identifier: MIT

package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/ops/backup/runner"
)

// captureEmitter records every operational event the run emits so the
// test can assert the §16.6 backup_completed / backup_failed catalogue
// types and payloads. spec: §25.3 lines 692-694; §16.6.
type captureEmitter struct {
	events []events.OperationalEvent
}

func (e *captureEmitter) Emit(_ context.Context, ev events.OperationalEvent) error {
	e.events = append(e.events, ev)
	return nil
}

// TestRunEmitsBackupCompletedOpsEvent_spec_25_3_692 asserts a successful
// run emits the §16.6 backup_completed operational event with the §25.3
// line 692 payload highlights (type, status, size, duration).
func TestRunEmitsBackupCompletedOpsEvent_spec_25_3_692(t *testing.T) {
	em := &captureEmitter{}
	_, err := runner.Run(context.Background(), runner.Config{
		BackupID:   "bkp-ok",
		Mode:       runner.ModeFull,
		Dumper:     fakeDumper{},
		Archiver:   fakeArchiver{},
		Uploader:   newFakeUploader(),
		Reporter:   &fakeReporter{},
		OpsEmitter: em,
		Now:        func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	ev := findEvent(t, em.events, events.EventBackupCompleted.CloudEventsType())
	if ev.Severity != "info" {
		t.Errorf("severity = %q, want info", ev.Severity)
	}
	if ev.Subject != "backup/bkp-ok" {
		t.Errorf("subject = %q, want backup/bkp-ok", ev.Subject)
	}
	payload := decodePayload(t, ev)
	if payload["status"] != "completed" {
		t.Errorf("status = %v, want completed", payload["status"])
	}
	if payload["type"] != string(runner.ModeFull) {
		t.Errorf("type = %v, want %s", payload["type"], runner.ModeFull)
	}
	if _, ok := payload["sizeBytes"]; !ok {
		t.Error("payload missing sizeBytes")
	}
	if _, ok := payload["durationMs"]; !ok {
		t.Error("payload missing durationMs")
	}
}

// TestRunEmitsBackupFailedOpsEvent_spec_25_3_694 asserts a failed run
// emits the §16.6 backup_failed operational event carrying the §25.3
// line 694 payload (type, error).
func TestRunEmitsBackupFailedOpsEvent_spec_25_3_694(t *testing.T) {
	em := &captureEmitter{}
	_, err := runner.Run(context.Background(), runner.Config{
		BackupID:   "bkp-bad",
		Mode:       runner.ModeFull,
		Dumper:     fakeDumper{pgErr: errors.New("pg_dump exploded")},
		Archiver:   fakeArchiver{},
		Uploader:   newFakeUploader(),
		Reporter:   &fakeReporter{},
		OpsEmitter: em,
	})
	if err == nil {
		t.Fatal("Run: expected an error from the failing dump")
	}
	ev := findEvent(t, em.events, events.EventBackupFailed.CloudEventsType())
	if ev.Severity != "warning" {
		t.Errorf("severity = %q, want warning", ev.Severity)
	}
	if ev.Subject != "backup/bkp-bad" {
		t.Errorf("subject = %q, want backup/bkp-bad", ev.Subject)
	}
	payload := decodePayload(t, ev)
	if payload["status"] != "failed" {
		t.Errorf("status = %v, want failed", payload["status"])
	}
	if errStr, _ := payload["error"].(string); errStr == "" {
		t.Errorf("payload error = %q, want the failure reason", errStr)
	}
}

// TestRunNilOpsEmitterIsNoOp_spec_25_3_670 confirms an unconfigured
// emitter (Redis absent on the Job) does not panic and the run still
// succeeds — the durable audit row and status update are unaffected.
func TestRunNilOpsEmitterIsNoOp_spec_25_3_670(t *testing.T) {
	reporter := &fakeReporter{}
	_, err := runner.Run(context.Background(), runner.Config{
		BackupID: "bkp-nil",
		Mode:     runner.ModeFull,
		Dumper:   fakeDumper{},
		Archiver: fakeArchiver{},
		Uploader: newFakeUploader(),
		Reporter: reporter,
		// OpsEmitter deliberately left nil.
	})
	if err != nil {
		t.Fatalf("Run with nil OpsEmitter: %v", err)
	}
	if reporter.completed == nil {
		t.Error("run did not record completion with a nil emitter")
	}
}

func findEvent(t *testing.T, evs []events.OperationalEvent, ceType string) events.OperationalEvent {
	t.Helper()
	for _, ev := range evs {
		if ev.Type == ceType {
			return ev
		}
	}
	t.Fatalf("no event of type %q among %d emitted events", ceType, len(evs))
	return events.OperationalEvent{}
}

func decodePayload(t *testing.T, ev events.OperationalEvent) map[string]any {
	t.Helper()
	if ev.DataContentType != "application/json" {
		t.Errorf("datacontenttype = %q, want application/json", ev.DataContentType)
	}
	var m map[string]any
	if err := json.Unmarshal(ev.Data, &m); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return m
}
