// SPDX-License-Identifier: MIT

package jwtaudit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/mtls"
)

// recordingAppender captures every Append call so a test asserts on
// the committed (tenant, event_type, payload) tuple without needing
// a real audit hash chain.
type recordingAppender struct {
	appends []recordedAppend
	failErr error
}

type recordedAppend struct {
	tenantID  string
	eventType string
	payload   json.RawMessage
	at        time.Time
}

func (r *recordingAppender) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	if r.failErr != nil {
		return audit.Row{}, r.failErr
	}
	r.appends = append(r.appends, recordedAppend{
		tenantID:  tenantID,
		eventType: eventType,
		payload:   append(json.RawMessage(nil), payload...),
		at:        at,
	})
	return audit.Row{TenantID: tenantID, EventType: eventType, Payload: payload, Timestamp: at}, nil
}

// spec: 10.3
// diagnosis: a CA-rotation stage transition did not produce a
// `platform.ca_rotated` audit row, or the row landed on the wrong
// tenant chain. The CAObserver must emit one row per stage
// transition on the `platform` chain by default with the canonical
// event type.
func TestCAObserverEmitsRotatedRow(t *testing.T) {
	t.Parallel()
	ap := &recordingAppender{}
	obs := NewCAObserver(ap)
	at := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	obs.OnCARotation(mtls.CARotationEvent{
		From:         mtls.CAStageIdle,
		To:           mtls.CAStageNewCADeployed,
		CurrentCAID:  "ca-1",
		TrustedCAIDs: []string{"ca-1", "ca-2"},
		At:           at,
	})

	if len(ap.appends) != 1 {
		t.Fatalf("appends = %d, want 1", len(ap.appends))
	}
	got := ap.appends[0]
	if got.tenantID != PlatformTenantID {
		t.Errorf("tenantID = %q, want %q", got.tenantID, PlatformTenantID)
	}
	if got.eventType != EventTypeCARotated {
		t.Errorf("eventType = %q, want %q", got.eventType, EventTypeCARotated)
	}
	if !got.at.Equal(at) {
		t.Errorf("at = %v, want %v", got.at, at)
	}
	var payload CAPayload
	if err := json.Unmarshal(got.payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.From != string(mtls.CAStageIdle) {
		t.Errorf("payload.From = %q, want %q", payload.From, mtls.CAStageIdle)
	}
	if payload.To != string(mtls.CAStageNewCADeployed) {
		t.Errorf("payload.To = %q, want %q", payload.To, mtls.CAStageNewCADeployed)
	}
	if payload.CurrentCAID != "ca-1" {
		t.Errorf("payload.CurrentCAID = %q, want ca-1", payload.CurrentCAID)
	}
	if len(payload.TrustedCAIDs) != 2 {
		t.Errorf("payload.TrustedCAIDs = %v, want 2 entries", payload.TrustedCAIDs)
	}
}

// spec: 10.3
// diagnosis: the CAObserver wired with a CARotation did not receive
// an end-to-end event when the rotation advanced. The
// mtls.CARotationObserver contract must drop in as a CARotation
// observer and produce one audit row per Begin / Promote / Retire
// call.
func TestCAObserverWiresIntoCARotation(t *testing.T) {
	t.Parallel()
	ap := &recordingAppender{}
	obs := NewCAObserver(ap)
	r, err := mtls.NewCARotation("ca-1", mtls.CARotationOptions{
		OverlapWindow: time.Hour,
		Observer:      obs,
	})
	if err != nil {
		t.Fatalf("NewCARotation: %v", err)
	}
	if err := r.BeginNewCARotation("ca-2"); err != nil {
		t.Fatalf("BeginNewCARotation: %v", err)
	}
	if err := r.PromoteNewCA(); err != nil {
		t.Fatalf("PromoteNewCA: %v", err)
	}
	if len(ap.appends) != 2 {
		t.Fatalf("appends after begin+promote = %d, want 2", len(ap.appends))
	}
	for _, a := range ap.appends {
		if a.eventType != EventTypeCARotated {
			t.Errorf("eventType = %q, want %q", a.eventType, EventTypeCARotated)
		}
	}
}

// spec: 10.3
// diagnosis: NewCAObserver accepted a nil appender so the gateway
// would panic on the first transition. Construction must reject a
// nil appender so the wiring failure surfaces at boot.
func TestNewCAObserverRejectsNilAppender(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Errorf("NewCAObserver(nil) did not panic")
		}
	}()
	_ = NewCAObserver(nil)
}

// spec: 10.3
// diagnosis: a failed `platform.ca_rotated` append was dropped
// silently, or the observer ignored its configured tenant and logger.
// A failed append must be counted and logged, and the configured
// tenant must receive the row when the append succeeds.
func TestCAObserverCountsAppendFailuresAndHonorsOptions(t *testing.T) {
	t.Parallel()
	logs := &recordingLogger{}
	ap := &recordingAppender{failErr: errors.New("postgres down")}
	obs := NewCAObserver(ap, WithTenantID("acme"), WithLogger(logs.Printf), WithAppendTimeout(time.Second))
	ev := mtls.CARotationEvent{From: mtls.CAStageIdle, To: mtls.CAStageNewCADeployed, CurrentCAID: "ca-1", At: time.Now().UTC()}

	obs.OnCARotation(ev)
	if obs.FailedAppends() != 1 {
		t.Errorf("FailedAppends after failure = %d, want 1", obs.FailedAppends())
	}
	if len(logs.entries) != 1 || !strings.Contains(logs.entries[0], "postgres down") {
		t.Errorf("failure was not logged: %v", logs.entries)
	}

	ap.failErr = nil
	obs.OnCARotation(ev)
	if obs.FailedAppends() != 1 {
		t.Errorf("FailedAppends after success = %d, want 1", obs.FailedAppends())
	}
	if len(ap.appends) != 1 || ap.appends[0].tenantID != "acme" {
		t.Errorf("appends = %+v, want one row on tenant acme", ap.appends)
	}
}

// recordingLogger captures formatted log lines.
type recordingLogger struct {
	entries []string
}

func (r *recordingLogger) Printf(format string, args ...any) {
	r.entries = append(r.entries, sprintf(format, args...))
}

func sprintf(format string, args ...any) string {
	// avoid pulling fmt by recombining via a tiny helper that
	// matches printf semantics for the failure-path test above
	return formatLikePrintf(format, args)
}

func formatLikePrintf(format string, args []any) string {
	// Trivial passthrough — the test only checks substring presence,
	// so the exact formatted output is not load-bearing. The fmt
	// package would work too; this avoids a stdlib dep for the sake
	// of clarity.
	var sb strings.Builder
	sb.WriteString(format)
	for _, a := range args {
		sb.WriteString(" ")
		if s, ok := a.(string); ok {
			sb.WriteString(s)
			continue
		}
		if e, ok := a.(error); ok {
			sb.WriteString(e.Error())
			continue
		}
	}
	return sb.String()
}
