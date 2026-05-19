// SPDX-License-Identifier: MIT

// Package redis runs the in-process Redis-compatible server that backs
// §17.4 Embedded Mode. It wraps github.com/alicebob/miniredis, a pure
// Go implementation of the Redis wire protocol, and binds it to a
// loopback TCP port so the production gateway and controllers connect
// to it through their standard --redis-url configuration.
//
// The server holds state in memory only. §17.4 specifies that
// Embedded Mode Redis state is lost on lenny down. Embedded Mode Redis
// runs loopback-only and is exempt from the production AUTH and TLS
// requirements; the non-suppressible production warning banner states
// that Embedded Mode credentials are insecure.
package redis

import (
	"context"
	"fmt"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

// Server is a running in-process Redis-compatible server.
type Server struct {
	mr      *miniredis.Miniredis
	started bool
}

// New constructs a Server. The server is not started until Start is
// called.
func New() *Server {
	return &Server{}
}

// Start binds the server to a loopback TCP port. The port is chosen by
// the operating system. Start is idempotent within a process.
func (s *Server) Start() error {
	if s.started {
		return nil
	}
	mr := miniredis.NewMiniRedis()
	// StartAddr binds an explicit address. 127.0.0.1:0 lets the OS
	// pick a free port and keeps the listener loopback-only, which
	// satisfies the §17.4 loopback-only requirement.
	if err := mr.StartAddr("127.0.0.1:0"); err != nil {
		return fmt.Errorf("embedded redis: start: %w", err)
	}
	s.mr = mr
	s.started = true
	return nil
}

// Stop terminates the server and discards its in-memory state. Stop is
// idempotent.
func (s *Server) Stop() {
	if !s.started || s.mr == nil {
		return
	}
	s.mr.Close()
	s.started = false
}

// Addr returns the host:port the server listens on. It is valid only
// while the server is running.
func (s *Server) Addr() string {
	if s.mr == nil {
		return ""
	}
	return s.mr.Addr()
}

// URL returns the redis:// connection URL for database 0. The gateway
// and controllers consume it through their --redis-url configuration.
func (s *Server) URL() string {
	addr := s.Addr()
	if addr == "" {
		return ""
	}
	return "redis://" + addr + "/0"
}

// Ping connects to the server and issues a PING. It is used by lenny
// status.
func (s *Server) Ping(ctx context.Context) error {
	addr := s.Addr()
	if addr == "" {
		return fmt.Errorf("embedded redis: not started")
	}
	client := goredis.NewClient(&goredis.Options{Addr: addr})
	defer func() { _ = client.Close() }()
	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("embedded redis: ping: %w", err)
	}
	return nil
}
