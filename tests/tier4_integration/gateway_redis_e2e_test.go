//go:build component

// SPDX-License-Identifier: MIT

// Tier-4 integration test: the real cmd/lenny-gateway binary running
// against a Redis container via --redis-url. It proves the §12.4
// circuit-breaker wiring — an operator opening a breaker through the
// admin API lands the breaker state in Redis, verified by reading the
// cb:{name} key directly.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

func TestGatewayRedisBreakerE2E(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	rd := containers.StartRedis(t, containers.RedisOptions{})
	gw := gateway.StartWith(t, "--dev-mode", "--redis-url=redis://"+rd.Addr+"/0")
	base := gw.BaseURL()
	ctx := context.Background()

	// Open a circuit breaker through the §15.1 admin API as a
	// platform-admin.
	const name = "rt-echo-e2e"
	body, _ := json.Marshal(map[string]any{
		"reason":     "runaway runtime (e2e)",
		"limit_tier": "runtime",
		"scope":      map[string]any{"runtime": "echo"},
	})
	req, _ := http.NewRequest(http.MethodPost,
		base+"/v1/admin/circuit-breakers/"+name+"/open", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "platform")
	req.Header.Set("X-Lenny-User-ID", "ops@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open breaker: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open breaker: status %d, body=%s", resp.StatusCode, raw)
	}

	// The breaker state must now be in Redis under the §12.4 cb:{name}
	// key — that is what makes the operator's safety block survive a
	// gateway restart.
	stored, err := rd.Client.Get(ctx, "cb:"+name).Result()
	if err != nil {
		t.Fatalf("read cb:%s from Redis: %v", name, err)
	}
	if !strings.Contains(stored, `"State":"open"`) {
		t.Errorf("Redis breaker record is not open: %s", stored)
	}
	if !strings.Contains(stored, "echo") {
		t.Errorf("Redis breaker record missing the runtime scope: %s", stored)
	}
}
