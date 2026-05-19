// SPDX-License-Identifier: MIT

package redis

import (
	"context"
	"strings"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

func TestServerStartStop(t *testing.T) {
	s := New()
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	if s.Addr() == "" {
		t.Fatal("Addr is empty after Start")
	}
	// §17.4 requires the embedded Redis to bind loopback only.
	if !strings.HasPrefix(s.Addr(), "127.0.0.1:") {
		t.Errorf("Addr %q is not loopback-bound", s.Addr())
	}
	if !strings.HasPrefix(s.URL(), "redis://127.0.0.1:") {
		t.Errorf("URL %q is not a loopback redis URL", s.URL())
	}
	if !strings.HasSuffix(s.URL(), "/0") {
		t.Errorf("URL %q does not select database 0", s.URL())
	}
}

func TestServerStartIdempotent(t *testing.T) {
	s := New()
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()
	addr := s.Addr()
	// A second Start is a no-op and must not rebind.
	if err := s.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if s.Addr() != addr {
		t.Errorf("Addr changed after second Start: %q != %q", s.Addr(), addr)
	}
}

func TestServerPingAndCommands(t *testing.T) {
	s := New()
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	// The gateway and controllers speak the Redis wire protocol; a
	// SET/GET round-trip confirms the embedded server answers it.
	client := goredis.NewClient(&goredis.Options{Addr: s.Addr()})
	defer func() { _ = client.Close() }()
	if err := client.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatalf("SET: %v", err)
	}
	got, err := client.Get(ctx, "k").Result()
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if got != "v" {
		t.Errorf("GET returned %q, want %q", got, "v")
	}
}

func TestPingBeforeStart(t *testing.T) {
	s := New()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Ping(ctx); err == nil {
		t.Error("expected Ping before Start to fail")
	}
}

func TestStopBeforeStartIsNoOp(t *testing.T) {
	s := New()
	// Stop on a never-started server must not panic.
	s.Stop()
}
