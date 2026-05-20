// SPDX-License-Identifier: MIT

package redisconn_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/redisconn"
)

func TestNewClientRejectsEmptyConfig(t *testing.T) {
	_, err := redisconn.NewClient(redisconn.Config{})
	if !errors.Is(err, redisconn.ErrNoSource) {
		t.Errorf("empty config: got %v, want ErrNoSource", err)
	}
}

func TestNewClientRejectsSentinelWithoutMaster(t *testing.T) {
	_, err := redisconn.NewClient(redisconn.Config{
		SentinelAddrs: []string{"127.0.0.1:26379"},
	})
	if !errors.Is(err, redisconn.ErrMissingMasterName) {
		t.Errorf("missing master: got %v, want ErrMissingMasterName", err)
	}
}

func TestNewClientDirectMode(t *testing.T) {
	c, err := redisconn.NewClient(redisconn.Config{URL: "redis://127.0.0.1:6379/0"})
	if err != nil {
		t.Fatalf("direct mode: %v", err)
	}
	defer c.Close()
	if c.Options().Addr != "127.0.0.1:6379" {
		t.Errorf("direct mode addr: got %q, want 127.0.0.1:6379", c.Options().Addr)
	}
}

func TestNewClientDirectModeAppliesPasswordOverride(t *testing.T) {
	c, err := redisconn.NewClient(redisconn.Config{
		URL:      "redis://127.0.0.1:6379/0",
		Password: "override",
	})
	if err != nil {
		t.Fatalf("direct mode: %v", err)
	}
	defer c.Close()
	if c.Options().Password != "override" {
		t.Errorf("password override: got %q, want override", c.Options().Password)
	}
}

func TestNewClientDirectModeAppliesDBOverride(t *testing.T) {
	c, err := redisconn.NewClient(redisconn.Config{
		URL: "redis://127.0.0.1:6379/0",
		DB:  3,
	})
	if err != nil {
		t.Fatalf("direct mode: %v", err)
	}
	defer c.Close()
	if c.Options().DB != 3 {
		t.Errorf("db override: got %d, want 3", c.Options().DB)
	}
}

func TestNewClientSentinelMode(t *testing.T) {
	c, err := redisconn.NewClient(redisconn.Config{
		SentinelAddrs: []string{"127.0.0.1:26379", "127.0.0.1:26380", "127.0.0.1:26381"},
		MasterName:    "lenny-master",
		Password:      "p",
	})
	if err != nil {
		t.Fatalf("sentinel mode: %v", err)
	}
	defer c.Close()
	// Sentinel-mode FailoverClient does not surface SentinelAddrs on
	// the returned *redis.Client.Options(); the construction not
	// erroring confirms the failover branch is wired.
	if c == nil {
		t.Error("sentinel mode returned nil client")
	}
}

func TestNewClientURLTakesPrecedenceOverSentinel(t *testing.T) {
	c, err := redisconn.NewClient(redisconn.Config{
		URL:           "redis://127.0.0.1:6379/0",
		SentinelAddrs: []string{"127.0.0.1:26379"},
		MasterName:    "ignored",
	})
	if err != nil {
		t.Fatalf("mixed mode: %v", err)
	}
	defer c.Close()
	if c.Options().Addr != "127.0.0.1:6379" {
		t.Errorf("URL should win: got addr %q, want 127.0.0.1:6379", c.Options().Addr)
	}
}
