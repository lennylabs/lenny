// SPDX-License-Identifier: MIT

//go:build linux

package superviseprobe

import (
	"net"
	"os"
	"testing"
	"time"
)

// spec: §4.7.11 item 8 (Runtime supervisor, Property self-test)
//
// TestProcAttacksFailForAnUnprivilegedCaller runs the ptrace and /proc/1
// attacks as the unprivileged test user, which must be refused as the agent
// UID is refused against a non-dumpable supervisor.
func TestProcAttacksFailForAnUnprivilegedCaller(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("blocked: the test runs as root, which the attacks do not model")
	}
	for _, a := range []string{AttackPtracePID1, AttackReadProc1Mem, AttackReadProc1FD} {
		if r := runAttack([]string{a}); r.OK || r.Errno == "" || r.Attack != a {
			t.Errorf("runAttack(%s) = %+v; want a refusal with an errno", a, r)
		}
	}
	if r := killRuntime(); r.OK || r.Detail == "" {
		t.Errorf("killRuntime with no serving probe = %+v; want nothing killed", r)
	}
}

// spec: §28.5.3 (CH-SUPERVISE, Endpoint)
//
// TestSquatCountsTheConnectionsItAccepts pins the squat attack's observable:
// it binds the CH-SUPERVISE name and counts the connections it accepts in
// its window, zero when nothing dials and one when a peer dials.
func TestSquatCountsTheConnectionsItAccepts(t *testing.T) {
	done := make(chan AttackResult, 1)
	go func() { done <- runAttack([]string{AttackBindSupervise, "2"}) }()
	var conn net.Conn
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("unix", abstractAddr(SuperviseSocket))
		if err == nil {
			conn = c
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if conn == nil {
		t.Skip("blocked: the host could not bind the CH-SUPERVISE name within a second")
	}
	_ = conn.Close()
	r := <-done
	if !r.OK || r.Connections != 1 {
		t.Fatalf("squat = %+v; want one accepted connection", r)
	}
	if r := runAttack([]string{AttackBindSupervise, "1"}); !r.OK || r.Connections != 0 {
		t.Errorf("idle squat = %+v; want no connection", r)
	}
	if r := runAttack([]string{AttackSignalPID1, "10"}); r.Detail != "SIGUSR1" {
		t.Errorf("SIGUSR1 to PID 1 = %+v; want the signal named", r)
	}
}
