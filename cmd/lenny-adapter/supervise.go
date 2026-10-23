// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// The adapter inputs of a supervised pod: the flags the pod builder renders
// on every sidecar pod that runs the runtime supervisor, their domains, and
// their compiled defaults. The specification states no value for the ping
// interval, the ping timeout, the supervisor wait timeout, or the warm-up
// timeout, so each is an operator-tunable controller setting the pod builder
// renders as a flag; the compiled defaults apply where none is rendered.
// spec: §28.5.3 (CH-SUPERVISE, Timing); §4.7.10 (Runtime supervisor, Launch).
const (
	defaultSupervisePingInterval = 5 * time.Second
	defaultSupervisePingTimeout  = 5 * time.Second
	defaultSuperviseWaitTimeout  = 5 * time.Minute
	defaultWarmUpTimeout         = 2 * time.Minute
)

// superviseFlagNames are the flags of the supervised rows: setting any of them
// while --supervise-socket is empty is refused.
var superviseFlagNames = []string{
	"runtime-process", "launch-mode", "supervise-ping-interval",
	"supervise-ping-timeout", "supervise-wait-timeout", "warm-up-timeout",
}

// superviseFlags holds the raw supervised flags.
type superviseFlags struct {
	socket         string
	runtimeProcess string
	launchMode     string
	pingInterval   time.Duration
	pingTimeout    time.Duration
	waitTimeout    time.Duration
	warmUpTimeout  time.Duration
}

// superviseInputs is the validated supervised input contract. Zero (Socket
// empty) means the pod runs no supervisor.
type superviseInputs struct {
	Socket        string
	Lifetime      adapter.RuntimeLifetime
	Mode          adapter.LaunchMode
	PingInterval  time.Duration
	PingTimeout   time.Duration
	WaitTimeout   time.Duration
	WarmUpTimeout time.Duration
}

// Supervised reports whether the pod runs the runtime supervisor.
func (in superviseInputs) Supervised() bool { return in.Socket != "" }

// validateSuperviseInputs applies the adapter inputs contract. set names the
// flags given on the command line. A refusal is the P1 gate failure: main
// exits 1 with nothing launched and nothing bound.
func validateSuperviseInputs(f *superviseFlags, set map[string]bool) (superviseInputs, error) {
	if f.socket == "" {
		for _, n := range superviseFlagNames {
			if set[n] {
				return superviseInputs{}, fmt.Errorf("--%s requires --supervise-socket", n)
			}
		}
		return superviseInputs{}, nil
	}
	if !strings.HasPrefix(f.socket, "@") || len(f.socket) < 2 || len(f.socket) > 108 {
		return superviseInputs{}, fmt.Errorf("--supervise-socket %q is not an abstract socket name", f.socket)
	}
	in := superviseInputs{
		Socket: f.socket, PingInterval: f.pingInterval, PingTimeout: f.pingTimeout,
		WaitTimeout: f.waitTimeout, WarmUpTimeout: f.warmUpTimeout,
	}
	var err error
	if in.Lifetime, err = parseLifetime(f.runtimeProcess); err != nil {
		return superviseInputs{}, err
	}
	if in.Mode, err = parseLaunchMode(f.launchMode); err != nil {
		return superviseInputs{}, err
	}
	if in.Lifetime == adapter.LifetimeRestart && in.Mode != adapter.LaunchSession {
		return superviseInputs{}, fmt.Errorf("--runtime-process=restart requires --launch-mode=session, got %s", in.Mode)
	}
	if err := positiveDurations(in); err != nil {
		return superviseInputs{}, err
	}
	return in, nil
}

func parseLifetime(v string) (adapter.RuntimeLifetime, error) {
	switch adapter.RuntimeLifetime(v) {
	case adapter.LifetimeKeep, adapter.LifetimeRestart:
		return adapter.RuntimeLifetime(v), nil
	case "":
		return "", errors.New("--runtime-process is required with --supervise-socket")
	default:
		return "", fmt.Errorf("--runtime-process %q is not keep or restart", v)
	}
}

func parseLaunchMode(v string) (adapter.LaunchMode, error) {
	switch m := adapter.LaunchMode(v); m {
	case adapter.LaunchSession, adapter.LaunchService, adapter.LaunchPreconnectSDKWarm, adapter.LaunchPreconnectPodWarm:
		return m, nil
	case "":
		return "", errors.New("--launch-mode is required with --supervise-socket")
	default:
		return "", fmt.Errorf("--launch-mode %q is not session, service, preconnect-sdk-warm, or preconnect-pod-warm", v)
	}
}

func positiveDurations(in superviseInputs) error {
	for name, d := range map[string]time.Duration{
		"supervise-ping-interval": in.PingInterval,
		"supervise-ping-timeout":  in.PingTimeout,
		"supervise-wait-timeout":  in.WaitTimeout,
		"warm-up-timeout":         in.WarmUpTimeout,
	} {
		if d <= 0 {
			return fmt.Errorf("--%s must be a positive duration, got %s", name, d)
		}
	}
	return nil
}
