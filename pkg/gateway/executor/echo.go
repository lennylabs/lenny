// SPDX-License-Identifier: MIT

package executor

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// EchoExecutor is an in-process Executor backing the minimal gateway.
// For each user message it returns a single `text` OutputPart whose
// content echoes the input prefixed with `echo [seq=N]:`, where N is
// the per-session sequence number. The shape matches the §15.4.4
// reference echo runtime so contract tests written against that
// runtime exercise the same wire shape against the in-process
// executor.
//
// EchoExecutor is concurrency-safe.
type EchoExecutor struct {
	mu  sync.Mutex
	seq map[string]int
}

// NewEchoExecutor returns a fresh EchoExecutor.
func NewEchoExecutor() *EchoExecutor {
	return &EchoExecutor{seq: map[string]int{}}
}

// Send implements Executor. Concatenates every user message in the
// batch into a single response prefixed with the per-session
// sequence number.
func (e *EchoExecutor) Send(_ context.Context, sessionID string, messages []Message) ([]OutputPart, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq[sessionID]++
	seq := e.seq[sessionID]

	var inputs []string
	for _, m := range messages {
		if m.Content == "" {
			continue
		}
		inputs = append(inputs, m.Content)
	}
	body := strings.Join(inputs, " | ")
	return []OutputPart{{
		Type: "text",
		Text: fmt.Sprintf("echo [seq=%d]: %s", seq, body),
	}}, nil
}

// Close implements Executor. Drops the per-session sequence counter.
func (e *EchoExecutor) Close(_ context.Context, sessionID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.seq, sessionID)
	return nil
}
