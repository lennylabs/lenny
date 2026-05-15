// SPDX-License-Identifier: MIT

package executor_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/executor"
)

// spec: §15.4.4 echo runtime contract.

func TestEchoExecutorSequencesPerSession(t *testing.T) {
	e := executor.NewEchoExecutor()

	// First send.
	out, err := e.Send(context.Background(), "sess_a", []executor.Message{
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	if len(out) != 1 || out[0].Type != "text" {
		t.Errorf("output: %+v", out)
	}
	if !strings.Contains(out[0].Text, "[seq=1]") || !strings.Contains(out[0].Text, "hello") {
		t.Errorf("expected seq=1 + hello, got %q", out[0].Text)
	}

	// Second send on same session advances sequence.
	out2, _ := e.Send(context.Background(), "sess_a", []executor.Message{
		{Role: "user", Content: "world"},
	})
	if !strings.Contains(out2[0].Text, "[seq=2]") {
		t.Errorf("expected seq=2, got %q", out2[0].Text)
	}

	// Different session has its own sequence.
	outB, _ := e.Send(context.Background(), "sess_b", []executor.Message{
		{Role: "user", Content: "x"},
	})
	if !strings.Contains(outB[0].Text, "[seq=1]") {
		t.Errorf("sess_b should start at seq=1, got %q", outB[0].Text)
	}
}

func TestEchoExecutorCloseDropsSequence(t *testing.T) {
	e := executor.NewEchoExecutor()
	_, _ = e.Send(context.Background(), "sess_a", []executor.Message{{Content: "x"}})
	_, _ = e.Send(context.Background(), "sess_a", []executor.Message{{Content: "x"}})
	if err := e.Close(context.Background(), "sess_a"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	out, _ := e.Send(context.Background(), "sess_a", []executor.Message{{Content: "again"}})
	if !strings.Contains(out[0].Text, "[seq=1]") {
		t.Errorf("Close should reset sequence: %q", out[0].Text)
	}
}

func TestEchoExecutorConcatenatesMessageBatch(t *testing.T) {
	e := executor.NewEchoExecutor()
	out, _ := e.Send(context.Background(), "sess_a", []executor.Message{
		{Content: "hello"},
		{Content: "world"},
	})
	if !strings.Contains(out[0].Text, "hello | world") {
		t.Errorf("batch: %q", out[0].Text)
	}
}

func TestEchoExecutorIgnoresEmptyContent(t *testing.T) {
	e := executor.NewEchoExecutor()
	out, _ := e.Send(context.Background(), "sess_a", []executor.Message{
		{Content: ""},
		{Content: "real"},
	})
	if !strings.Contains(out[0].Text, "real") || strings.Contains(out[0].Text, " | real") {
		// "real" should appear; no leading " | " from empty content.
		if !strings.Contains(out[0].Text, "real") {
			t.Errorf("empty-content batch should still emit real: %q", out[0].Text)
		}
	}
}

func TestEchoExecutorCloseUnopenedIsNoOp(t *testing.T) {
	e := executor.NewEchoExecutor()
	if err := e.Close(context.Background(), "never-opened"); err != nil {
		t.Errorf("Close should be idempotent: %v", err)
	}
}
