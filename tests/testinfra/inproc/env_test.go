// SPDX-License-Identifier: MIT

package inproc

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func httpGet(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func TestEnvStartStop(t *testing.T) {
	env := New(Config{
		AdapterLatency:   time.Millisecond,
		AdapterErrorRate: 0,
		WatchLag:         5 * time.Millisecond,
	})
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer env.Stop(context.Background())

	if env.RedisAddr() == "" {
		t.Error("RedisAddr empty after Start")
	}
	if env.FakeKube() == nil {
		t.Error("FakeKube nil after Start")
	}
	if env.FakeKube().WatchLag() != 5*time.Millisecond {
		t.Errorf("FakeKube WatchLag=%v want 5ms", env.FakeKube().WatchLag())
	}
	if env.Adapter() == nil {
		t.Error("Adapter nil after Start")
	}
	if env.GatewayURL() == "" {
		t.Error("GatewayURL empty after Start")
	}
}

func TestEnvGatewayServesHealthz(t *testing.T) {
	env := New(Config{})
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer env.Stop(context.Background())

	resp, err := httpGet(env.GatewayURL() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	if resp != "ok" {
		t.Errorf("body=%q want ok", resp)
	}
}

func TestEnvDoubleStartRejected(t *testing.T) {
	env := New(Config{})
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer env.Stop(context.Background())
	if err := env.Start(context.Background()); err == nil {
		t.Fatal("second Start: expected error")
	}
}

func TestEnvStopIdempotent(t *testing.T) {
	env := New(Config{})
	_ = env.Start(context.Background())
	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := env.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}
