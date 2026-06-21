// SPDX-License-Identifier: MIT

//go:build unix

package stack

import (
	"context"
	"syscall"
	"testing"
	"time"
)

// spec: §24.19 (lenny restart signals the supervisor to restart one
// component; lenny down signals a graceful teardown), §17.4 (the
// supervisor is provisioned per host operating system). These tests
// exercise the POSIX supervisor-signal substrate (process_unix.go): the
// SIGHUP restart wakeup and the SIGTERM teardown wakeup the supervisor
// loop blocks on.

// TestSupervisorSignalsRestartWakeup covers newSupervisorSignals + wait:
// a SIGHUP delivered to this process reports restart=true.
func TestSupervisorSignalsRestartWakeup(t *testing.T) {
	sigs, err := newSupervisorSignals("")
	if err != nil {
		t.Fatalf("newSupervisorSignals: %v", err)
	}
	defer sigs.close()

	got := make(chan bool, 1)
	go func() { got <- sigs.wait(context.Background()) }()
	// Give wait a moment to register on the channel before signalling.
	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP: %v", err)
	}
	select {
	case restart := <-got:
		if !restart {
			t.Error("wait reported restart=false for a SIGHUP")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not return after SIGHUP")
	}
}

// TestSupervisorSignalsTeardownWakeup covers wait reporting restart=false
// for a SIGTERM (the graceful-teardown wakeup).
func TestSupervisorSignalsTeardownWakeup(t *testing.T) {
	sigs, err := newSupervisorSignals("")
	if err != nil {
		t.Fatalf("newSupervisorSignals: %v", err)
	}
	defer sigs.close()

	got := make(chan bool, 1)
	go func() { got <- sigs.wait(context.Background()) }()
	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	select {
	case restart := <-got:
		if restart {
			t.Error("wait reported restart=true for a SIGTERM")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not return after SIGTERM")
	}
}

// TestSupervisorSignalsContextCancel covers wait returning restart=false
// when the context is cancelled with no signal pending.
func TestSupervisorSignalsContextCancel(t *testing.T) {
	sigs, err := newSupervisorSignals("")
	if err != nil {
		t.Fatalf("newSupervisorSignals: %v", err)
	}
	defer sigs.close()

	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan bool, 1)
	go func() { got <- sigs.wait(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case restart := <-got:
		if restart {
			t.Error("wait reported restart=true for a cancelled context")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not return after context cancel")
	}
}

// TestSendRestartSignalDeliversSIGHUP covers sendRestartSignal: it
// delivers a SIGHUP that the supervisor-signal substrate observes as a
// restart wakeup. Sending to an impossible PID returns an error.
func TestSendRestartSignalDeliversSIGHUP(t *testing.T) {
	sigs, err := newSupervisorSignals("")
	if err != nil {
		t.Fatalf("newSupervisorSignals: %v", err)
	}
	defer sigs.close()

	got := make(chan bool, 1)
	go func() { got <- sigs.wait(context.Background()) }()
	time.Sleep(50 * time.Millisecond)
	if err := sendRestartSignal(syscall.Getpid(), Paths{}); err != nil {
		t.Fatalf("sendRestartSignal: %v", err)
	}
	select {
	case restart := <-got:
		if !restart {
			t.Error("wait reported restart=false after sendRestartSignal")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not return after sendRestartSignal")
	}

	// A signal to a PID that cannot exist surfaces an error so RunRestart
	// can clean up its request file and report the failure.
	if err := sendRestartSignal(1<<30, Paths{}); err == nil {
		t.Error("sendRestartSignal to an unused high PID should error")
	}
}

// TestGracefulStopSupervisorIsNoOpOnUnix covers gracefulStopSupervisor:
// on unix it reports false so RunDown falls through to the SIGTERM that
// stopByPID delivers.
func TestGracefulStopSupervisorIsNoOpOnUnix(t *testing.T) {
	if gracefulStopSupervisor(syscall.Getpid(), Paths{}) {
		t.Error("gracefulStopSupervisor should report false on unix")
	}
}

// TestDetachSysProcAttrSetsSession covers detachSysProcAttr — the
// supervisor detaches into its own session.
func TestDetachSysProcAttrSetsSession(t *testing.T) {
	attr := detachSysProcAttr()
	if attr == nil || !attr.Setsid {
		t.Errorf("detachSysProcAttr = %+v, want Setsid=true", attr)
	}
}

// TestProcessGroupSysProcAttrSetsGroup covers processGroupSysProcAttr:
// managed children start in their own process group.
func TestProcessGroupSysProcAttrSetsGroup(t *testing.T) {
	attr := processGroupSysProcAttr()
	if attr == nil || !attr.Setpgid {
		t.Errorf("processGroupSysProcAttr = %+v, want Setpgid=true", attr)
	}
}
