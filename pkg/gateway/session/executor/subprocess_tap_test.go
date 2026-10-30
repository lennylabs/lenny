// SPDX-License-Identifier: MIT

package executor_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
)

// spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.3 (CH-MSGSOCK)
// A child the adapter starts through Start hands every stdout line to the
// recorded activity tap with no Output subscriber.
func TestSubprocessExecutorTapReadsWithoutOutput_spec_4_7_1(t *testing.T) {
	e := executor.NewSubprocessExecutor(executor.SubprocessOptions{BinPath: "/bin/cat"})
	var mu sync.Mutex
	var lines []string
	e.TapOutput(func(line []byte) {
		mu.Lock()
		lines = append(lines, string(line))
		mu.Unlock()
	})
	ctx := context.Background()
	if err := e.Start(ctx, "sess-tap"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = e.Close(ctx, "sess-tap") })
	if err := e.WriteEnvelope("sess-tap", []byte(`{"type":"response","sessionId":"sess-tap"}`)); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, l := range lines {
			if strings.Contains(l, `"response"`) {
				mu.Unlock()
				return
			}
		}
		mu.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the tap never received the child's stdout line")
}
