// SPDX-License-Identifier: MIT

package superviseprobe

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// PeerEvent is one JSON line SubSupervisePeer prints to stdout, its
// container's log.
type PeerEvent struct {
	// Event is PeerReady once the supervisor's ready arrived, or
	// PeerLinkFailed when a pong missed its timeout or the connection closed.
	Event string `json:"event"`
	// Properties is the self-test result the ready reported.
	Properties map[string]bool `json:"properties,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// PeerEvent values.
const (
	PeerReady      = "ready"
	PeerLinkFailed = "link-failed"
)

// peerConfig is SubSupervisePeer's flags.
type peerConfig struct {
	agentUID     uint
	nonceOnly    bool
	readyTimeout time.Duration
	pingInterval time.Duration
	pingTimeout  time.Duration
}

// parsePeerFlags parses SubSupervisePeer's arguments.
func parsePeerFlags(args []string, stderr io.Writer) (peerConfig, error) {
	var cfg peerConfig
	fs := flag.NewFlagSet(SubSupervisePeer, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.UintVar(&cfg.agentUID, "agent-uid", 0, "the UID the supervisor runs at, which SO_PEERCRED must report")
	fs.BoolVar(&cfg.nonceOnly, "nonce-only", false, "accept the supervisor's connection with no SO_PEERCRED check")
	fs.DurationVar(&cfg.readyTimeout, "ready-timeout", time.Minute, "bound on the connection and its ready")
	fs.DurationVar(&cfg.pingInterval, "ping-interval", 2*time.Second, "interval between pings")
	fs.DurationVar(&cfg.pingTimeout, "ping-timeout", 2*time.Second, "bound on each pong")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if cfg.agentUID == 0 && !cfg.nonceOnly {
		return cfg, fmt.Errorf("%s: --agent-uid is required outside --nonce-only", SubSupervisePeer)
	}
	if cfg.pingInterval <= 0 || cfg.pingTimeout <= 0 {
		return cfg, fmt.Errorf("%s: ping interval and timeout must be positive", SubSupervisePeer)
	}
	return cfg, nil
}

// supervisePeer stands in for the adapter's end of CH-SUPERVISE on a pod a
// test builds outside the platform: it accepts the supervisor's connection
// and its ready through the adapter's own AwaitSupervisor, prints the
// self-test result, and pings through the adapter's RunPing until the link
// fails, when it prints PeerLinkFailed and exits non-zero, as a service-mode
// adapter does. A SIGTERM ends it with exit 0. It sends no launch, so the
// supervisor runs with no runtime process. spec: §28.5.3 (CH-SUPERVISE);
// §4.7.10 (Runtime supervisor, Liveness).
func supervisePeer(args []string, stdout, stderr io.Writer) int {
	cfg, err := parsePeerFlags(args, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "supervise-probe: %v\n", err)
		return exitProtocolError
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	enc := json.NewEncoder(stdout)
	link, err := adapter.AwaitSupervisor(ctx, adapter.AwaitSupervisorConfig{
		Socket:   SuperviseSocket,
		Auth:     adapter.SocketPeerAuth{ExpectedUID: uint32(cfg.agentUID), NonceOnly: cfg.nonceOnly},
		Lifetime: adapter.LifetimeKeep,
		Timeout:  cfg.readyTimeout,
		Logger:   slog.New(slog.NewJSONHandler(stderr, nil)),
	})
	if err != nil {
		_ = enc.Encode(PeerEvent{Event: PeerLinkFailed, Error: err.Error()})
		return exitRuntimeError
	}
	defer link.Close()
	_ = enc.Encode(PeerEvent{Event: PeerReady, Properties: link.Properties()})
	link.RunPing(ctx, cfg.pingInterval, cfg.pingTimeout)
	if ctx.Err() != nil && !link.Failed() {
		return exitOK
	}
	_ = enc.Encode(PeerEvent{Event: PeerLinkFailed})
	return exitRuntimeError
}

// ParsePeerEvents decodes the PeerEvent lines of a peer container's log.
// Lines that are not events, such as the adapter's structured log records
// on stderr, are skipped.
func ParsePeerEvents(log string) []PeerEvent {
	var out []PeerEvent
	for _, line := range strings.Split(log, "\n") {
		var ev PeerEvent
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &ev) == nil && ev.Event != "" {
			out = append(out, ev)
		}
	}
	return out
}
