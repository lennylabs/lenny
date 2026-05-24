// SPDX-License-Identifier: MIT

package runtimekit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// spec: §4.4 line 244 — runtime-side `checkpoint_ready` after the
// runtime quiesces.
func TestHandleCheckpointRequestEmitsReady(t *testing.T) {
	var out bytes.Buffer
	client := NewLifecycleClient(LifecycleClientOptions{
		Writer:            &out,
		AutoResumeTimeout: 50 * time.Millisecond,
	})
	defer client.Close()

	if err := client.HandleCheckpointRequest(context.Background(), "ckpt-1", 1000, func(_ context.Context, _ int32) error {
		return nil
	}); err != nil {
		t.Fatalf("HandleCheckpointRequest: %v", err)
	}
	if !strings.Contains(out.String(), `"type":"checkpoint_ready"`) {
		t.Errorf("output = %q, want checkpoint_ready frame", out.String())
	}
	if !strings.Contains(out.String(), `"checkpointId":"ckpt-1"`) {
		t.Errorf("output = %q, want checkpointId in the frame", out.String())
	}
	if client.PendingCount() != 1 {
		t.Errorf("pending = %d, want 1", client.PendingCount())
	}
}

// spec: §4.4 line 244 — autonomous-resume timer fires after 60s
// (compressed in test) and the runtime emits the timeout warning.
func TestAutonomousResumeFiresWhenCompleteNeverArrives(t *testing.T) {
	var stderr bytes.Buffer
	var out bytes.Buffer
	logCh := make(chan string, 4)
	client := NewLifecycleClient(LifecycleClientOptions{
		Writer:            &out,
		Stderr:            &stderr,
		AutoResumeTimeout: 20 * time.Millisecond,
		LogF: func(format string, args ...any) {
			logCh <- strings.TrimSpace(formatLog(format, args...))
		},
	})
	defer client.Close()

	if err := client.HandleCheckpointRequest(context.Background(), "ckpt-stuck", 100, func(_ context.Context, _ int32) error {
		return nil
	}); err != nil {
		t.Fatalf("HandleCheckpointRequest: %v", err)
	}

	select {
	case msg := <-logCh:
		if !strings.HasPrefix(msg, CheckpointTimeoutLogPrefix) {
			t.Errorf("log = %q, want prefix %q", msg, CheckpointTimeoutLogPrefix)
		}
		if !strings.Contains(msg, "checkpointId=ckpt-stuck") {
			t.Errorf("log = %q, want checkpointId in the message", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("autonomous-resume timer never fired")
	}
	if client.PendingCount() != 0 {
		t.Errorf("pending = %d, want 0 after auto-resume", client.PendingCount())
	}
}

// spec: §4.4 line 244 — receiving `checkpoint_complete` cancels the
// autonomous-resume timer and the runtime does not log the warning.
func TestHandleCheckpointCompleteCancelsTimer(t *testing.T) {
	var fired atomic.Bool
	var out bytes.Buffer
	client := NewLifecycleClient(LifecycleClientOptions{
		Writer:            &out,
		AutoResumeTimeout: 50 * time.Millisecond,
		LogF: func(string, ...any) {
			fired.Store(true)
		},
	})
	defer client.Close()

	if err := client.HandleCheckpointRequest(context.Background(), "ckpt-ok", 100, func(_ context.Context, _ int32) error {
		return nil
	}); err != nil {
		t.Fatalf("HandleCheckpointRequest: %v", err)
	}
	client.HandleCheckpointComplete("ckpt-ok")
	if client.PendingCount() != 0 {
		t.Errorf("pending = %d, want 0 after complete", client.PendingCount())
	}
	time.Sleep(80 * time.Millisecond)
	if fired.Load() {
		t.Errorf("autonomous-resume timer must not fire after checkpoint_complete")
	}
}

// A handler error short-circuits the handshake: the runtime emits
// `checkpoint_complete{status:"failed"}` instead of `checkpoint_ready`
// and does not arm the autonomous-resume timer.
func TestHandleCheckpointRequestHandlerErrorEmitsFailed(t *testing.T) {
	var out bytes.Buffer
	client := NewLifecycleClient(LifecycleClientOptions{
		Writer:            &out,
		AutoResumeTimeout: 50 * time.Millisecond,
	})
	defer client.Close()

	wantErr := errors.New("workspace too large")
	err := client.HandleCheckpointRequest(context.Background(), "ckpt-fail", 100, func(_ context.Context, _ int32) error {
		return wantErr
	})
	if err != nil {
		t.Fatalf("HandleCheckpointRequest must succeed at writing the failure frame, got %v", err)
	}
	if !strings.Contains(out.String(), `"type":"checkpoint_complete"`) {
		t.Errorf("output = %q, want checkpoint_complete on handler error", out.String())
	}
	if !strings.Contains(out.String(), `"status":"failed"`) {
		t.Errorf("output = %q, want status:failed", out.String())
	}
	if !strings.Contains(out.String(), `"reason":"workspace too large"`) {
		t.Errorf("output = %q, want reason field with handler error", out.String())
	}
	if client.PendingCount() != 0 {
		t.Errorf("pending = %d, want 0 on handler error", client.PendingCount())
	}
}

// A late checkpoint_complete (after the autonomous-resume timer fires)
// is silently ignored.
func TestHandleCheckpointCompleteLateIsNoop(t *testing.T) {
	var out bytes.Buffer
	client := NewLifecycleClient(LifecycleClientOptions{
		Writer: &out,
	})
	defer client.Close()
	// No pending — late complete is a no-op rather than a panic.
	client.HandleCheckpointComplete("ghost-id")
	if client.PendingCount() != 0 {
		t.Errorf("late complete must not register a pending slot")
	}
}

// Close cancels in-flight timers without firing the warning.
func TestCloseStopsPendingTimers(t *testing.T) {
	var stderr bytes.Buffer
	var out bytes.Buffer
	client := NewLifecycleClient(LifecycleClientOptions{
		Writer:            &out,
		Stderr:            &stderr,
		AutoResumeTimeout: 30 * time.Millisecond,
	})
	if err := client.HandleCheckpointRequest(context.Background(), "ckpt-shutdown", 100, func(_ context.Context, _ int32) error {
		return nil
	}); err != nil {
		t.Fatalf("HandleCheckpointRequest: %v", err)
	}
	client.Close()
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(stderr.String(), CheckpointTimeoutLogPrefix) {
		t.Errorf("stderr must not log a timeout after Close; got %q", stderr.String())
	}
}

// Empty checkpointId is rejected so a runtime author bug surfaces
// immediately rather than producing an unaddressable pending slot.
func TestHandleCheckpointRequestRejectsEmptyID(t *testing.T) {
	client := NewLifecycleClient(LifecycleClientOptions{Writer: &bytes.Buffer{}})
	defer client.Close()
	if err := client.HandleCheckpointRequest(context.Background(), "", 100, nil); err == nil {
		t.Errorf("empty checkpointId must be rejected")
	}
}

// A duplicate checkpoint_request for the same id re-arms the timer
// rather than panicking. Tests the defensive replace path.
func TestDuplicateCheckpointRequestReplacesTimer(t *testing.T) {
	var out bytes.Buffer
	client := NewLifecycleClient(LifecycleClientOptions{
		Writer:            &out,
		AutoResumeTimeout: 100 * time.Millisecond,
	})
	defer client.Close()

	if err := client.HandleCheckpointRequest(context.Background(), "ckpt-dup", 100, func(_ context.Context, _ int32) error {
		return nil
	}); err != nil {
		t.Fatalf("first HandleCheckpointRequest: %v", err)
	}
	if err := client.HandleCheckpointRequest(context.Background(), "ckpt-dup", 100, func(_ context.Context, _ int32) error {
		return nil
	}); err != nil {
		t.Fatalf("second HandleCheckpointRequest: %v", err)
	}
	if client.PendingCount() != 1 {
		t.Errorf("pending = %d, want 1 after duplicate request", client.PendingCount())
	}
}

// The ready frame is valid JSON we can decode back.
func TestCheckpointReadyFrameIsValidJSON(t *testing.T) {
	var out bytes.Buffer
	client := NewLifecycleClient(LifecycleClientOptions{Writer: &out})
	defer client.Close()
	if err := client.HandleCheckpointRequest(context.Background(), "ckpt-json", 100, func(_ context.Context, _ int32) error {
		return nil
	}); err != nil {
		t.Fatalf("HandleCheckpointRequest: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &parsed); err != nil {
		t.Fatalf("frame is not valid JSON: %v", err)
	}
	if parsed["type"] != "checkpoint_ready" {
		t.Errorf("type = %v, want checkpoint_ready", parsed["type"])
	}
}

// formatLog uses fmt.Sprintf to render the helper's emitted log line.
func formatLog(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

// TestConcurrentRequestAndComplete exercises concurrent
// HandleCheckpointRequest / HandleCheckpointComplete to catch a
// regression in the pending map's locking.
func TestConcurrentRequestAndComplete(t *testing.T) {
	client := NewLifecycleClient(LifecycleClientOptions{
		Writer:            &bytes.Buffer{},
		AutoResumeTimeout: 200 * time.Millisecond,
	})
	defer client.Close()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		id := fmt.Sprintf("ckpt-%d", i)
		go func(id string) {
			defer wg.Done()
			_ = client.HandleCheckpointRequest(context.Background(), id, 100, func(_ context.Context, _ int32) error {
				return nil
			})
		}(id)
		go func(id string) {
			defer wg.Done()
			time.Sleep(5 * time.Millisecond)
			client.HandleCheckpointComplete(id)
		}(id)
	}
	wg.Wait()
}
