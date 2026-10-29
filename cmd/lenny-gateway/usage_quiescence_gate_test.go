// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// TestUsageQuiescenceTimeoutBelowTheSealWindow_spec_8_3 asserts the startup
// check that holds the usage quiescence timeout in effect below the seal
// window in effect: a timeout equal to the seal window is refused, one a
// second shorter is accepted, a zero timeout resolves to the 5 s default and
// is refused against a 5 s seal window, and a zero seal window resolves to
// the 300 s default.
//
// spec: §7.1 (Terminal pipeline context), §8.3 (Usage quiescence before
// budget return), §11.3
func TestUsageQuiescenceTimeoutBelowTheSealWindow_spec_8_3(t *testing.T) {
	for _, tc := range []struct {
		name             string
		quiescence, seal int
		ok               bool
	}{
		{"equal to the seal window", 10, 10, false},
		{"a second shorter", 9, 10, true},
		{"longer than the seal window", 11, 10, false},
		{"zero timeout against a 5 s seal window", 0, 5, false},
		{"zero timeout against a 6 s seal window", 0, 6, true},
		{"default seal window", 299, 0, true},
		{"default seal window reached", 300, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateUsageQuiescenceTimeout(tc.quiescence, tc.seal)
			if (err == nil) != tc.ok {
				t.Errorf("validateUsageQuiescenceTimeout(%d, %d) = %v, want ok=%v", tc.quiescence, tc.seal, err, tc.ok)
			}
		})
	}
}

// TestUsageQuiescenceTimeoutWired_spec_8_3 asserts that the startup gate
// calls the check and that the session server receives the flag.
//
// spec: §8.3 (Usage quiescence before budget return), §11.3
func TestUsageQuiescenceTimeoutWired_spec_8_3(t *testing.T) {
	space := regexp.MustCompile(`\s+`)
	for _, tc := range []struct{ file, want string }{
		{"startupgates.go", "validateUsageQuiescenceTimeout(*f.delegationUsageQuiescenceTimeoutSeconds, *f.workspaceSealMaxDurationSeconds)"},
		{"sessionsrv.go", "UsageQuiescenceTimeout: time.Duration(*f.delegationUsageQuiescenceTimeoutSeconds) * time.Second,"},
	} {
		b, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(space.ReplaceAllString(string(b), " "), tc.want) {
			t.Errorf("%s: %q missing", tc.file, tc.want)
		}
	}
}

// countingReturner records budget returns.
type countingReturner struct{ calls chan treebudget.Reservation }

func (c countingReturner) Return(_ context.Context, r treebudget.Reservation) error {
	c.calls <- r
	return nil
}

func firedTimer(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- time.Time{}
	return ch
}

// TestAdminAndBudgetTerminatorsReturnTheChildOnce_spec_8_3 asserts that the
// admin adapter's ForceTerminate and the §11.2 budget terminator, each
// reaching the post-terminal hook through OnSessionTerminal, issue exactly
// one budget return with the four deltas on a delegated child's recorded
// root once WaitTerminalWork returns.
//
// spec: §7.1 (Terminal pipeline context), §8.3 (Return on completion), §11.2,
// §24.11
func TestAdminAndBudgetTerminatorsReturnTheChildOnce_spec_8_3(t *testing.T) {
	for _, tc := range []struct {
		name  string
		drive func(store sessionstore.Store, srv *sessionserver.Server)
	}{
		{"admin force-terminate", func(store sessionstore.Store, srv *sessionserver.Server) {
			a := sessionAdminAdapter{store: store, onTerminal: srv.OnSessionTerminal, beginTerminal: srv.BeginTerminal}
			if _, _, _, err := a.ForceTerminate(context.Background(), "acme", "child"); err != nil {
				t.Errorf("ForceTerminate: %v", err)
			}
		}},
		{"budget terminator", func(store sessionstore.Store, srv *sessionserver.Server) {
			term := &budgetSessionTerminator{store: store, onTerminal: srv.OnSessionTerminal, beginTerminal: srv.BeginTerminal}
			term.TerminateSession("child", sessionbudget.ReasonBudgetExhausted)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := memstore.New()
			for _, row := range []sessionstore.Session{
				{ID: "root", TenantID: "acme", State: session.StateRunning, RootSessionID: "root"},
				{ID: "child", TenantID: "acme", State: session.StateRunning, ParentSessionID: "root", RootSessionID: "root", DelegationDepth: 1},
			} {
				if err := store.Create(ctx, row); err != nil {
					t.Fatalf("seed %s: %v", row.ID, err)
				}
			}
			ret := countingReturner{calls: make(chan treebudget.Reservation, 8)}
			srv := sessionserver.New(store, sessionserver.Options{TreeBudgetReturner: ret, UsageQuiescenceTimer: firedTimer})
			defer srv.Close()
			tc.drive(store, srv)
			// The budget terminator opens no session-server claim before its
			// goroutine reaches OnSessionTerminal, so wait for the hook's
			// claim to appear before waiting for it to end.
			deadline := time.Now().Add(5 * time.Second)
			for len(ret.calls) == 0 && time.Now().Before(deadline) {
				wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := srv.WaitTerminalWork(wctx)
				cancel()
				if err != nil {
					t.Fatalf("WaitTerminalWork: %v", err)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := srv.WaitTerminalWork(ctx); err != nil {
				t.Fatalf("WaitTerminalWork: %v", err)
			}
			want := treebudget.Reservation{
				RootSessionID: "root", ParentSessionID: "root",
				TreeSizeDelta: 1, TreeMemoryDelta: treebudget.PerNodeMemoryBytes, ParallelChildrenDelta: 1,
			}
			if n := len(ret.calls); n != 1 {
				t.Fatalf("budget returns = %d, want 1", n)
			}
			if got := <-ret.calls; got != want {
				t.Errorf("budget return = %+v, want %+v", got, want)
			}
		})
	}
}
