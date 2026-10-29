// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §8.3 budget return on the compose
// stack. A parent races children and cancels the losers through
// lenny/cancel_child; each loser's post-terminal hook returns its
// reservation on the recorded root after the usage quiescence wait, so the
// parent's parallel_children slots and the tree's tree_size units come back
// and the parent can delegate as many children again.
package tier4_integration_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	sessionstorepg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: 8.3 (Return on completion), 8.3 (Usage quiescence before budget
// return), 7.1 (Terminal pipeline context)
// diagnosis: cancelling delegated children through lenny/cancel_child on the
// real cmd/lenny-gateway binary over Postgres and Redis did not return their
// reservations. Either the parent's {root}:dlg:parallel_children counter or
// the tree's tree_size counter did not fall back to zero within the usage
// quiescence timeout plus slack (the post-terminal hook runs no return, or
// the return omits the tree_size delta or addresses a non-root key set), or
// the parent could not delegate the same number of children again.
func TestCancelledChildrenReturnTheirSlots_spec_8_3(t *testing.T) {
	t.Parallel()
	pg := sharedDatabase(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode",
		"--postgres-dsn="+pg.DSN, "--redis-url=redis://"+rd.Addr+"/0",
		"--delegation-usage-quiescence-timeout-seconds=1")
	c := mcpClient{t: t, base: gw.BaseURL()}
	ctx := context.Background()

	root := c.runningSession()
	parallel := 3
	if r, err := sessionstorepg.New(pg.Pool).Get(ctx, mcpTenant, root); err == nil &&
		r.DelegationLease != nil && r.DelegationLease.MaxParallelChildren > 0 {
		parallel = r.DelegationLease.MaxParallelChildren
	}
	slotKey := "{" + root + "}:dlg:parallel_children:" + root
	sizeKey := "{" + root + "}:dlg:tree_size"
	counter := func(key string) int64 {
		v, err := rd.Client.Get(ctx, key).Result()
		if err != nil {
			return -1
		}
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}

	for wave := 0; wave < 2; wave++ {
		var losers []string
		for i := 0; i < parallel; i++ {
			losers = append(losers, c.delegateChild(root, fmt.Sprintf("echo-race-%d-%d", wave, i)))
		}
		if n := counter(slotKey); n != int64(parallel) {
			t.Fatalf("wave %d: parallel_children = %d after %d delegations, want %d", wave, n, parallel, parallel)
		}
		for _, id := range losers {
			rpc := c.callTool("lenny/cancel_child", `{"parentSessionId":"`+root+`","childSessionId":"`+id+`"}`)
			if text, isErr := toolResultText(t, rpc); isErr {
				t.Fatalf("wave %d: cancel_child %s: %s", wave, id, text)
			}
		}
		deadline := time.Now().Add(30 * time.Second)
		for counter(slotKey) != 0 || counter(sizeKey) != 0 {
			if time.Now().After(deadline) {
				t.Fatalf("wave %d: parallel_children = %d, tree_size = %d after cancelling every child, want 0 and 0",
					wave, counter(slotKey), counter(sizeKey))
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}
