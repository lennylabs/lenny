// SPDX-License-Identifier: MIT

//go:build linux

package superviseprobe

import (
	"net"
	"os"
	"path/filepath"
	"strings"
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

// spec: §4.7.11 item 8 (Runtime supervisor, Property self-test)
// diagnosis: a failure means the /proc/1/mem attack reports a refusal when
// the memory file opened, so a supervisor that lost its non-dumpable
// property passes the tier-9 check whenever the confirming read fails.
//
// TestOpenableMemFileIsABreachEvenWhenTheReadFails pins that a successful
// open of the memory file is the breach: an openable file whose read fails
// still yields OK, an openable file with a readable mapping yields OK, and
// only a refused open yields a refusal with an errno.
func TestOpenableMemFileIsABreachEvenWhenTheReadFails(t *testing.T) {
	dir := t.TempDir()
	emptyMem := filepath.Join(dir, "mem")
	if err := os.WriteFile(emptyMem, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	maps := filepath.Join(dir, "maps")
	if err := os.WriteFile(maps, []byte("00400000-00452000 r-xp 00000000 08:02 173521 /sup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, mem, maps string
		wantOK          bool
		wantDetail      string
	}{
		{"read fails at a listed mapping", emptyMem, maps, true, "open succeeded; read at 0x400000"},
		{"maps unreadable", emptyMem, filepath.Join(dir, "absent-maps"), true, "open succeeded; no readable mapping"},
		{"own memory readable", "/proc/self/mem", "/proc/self/maps", true, "succeeded"},
		{"open refused", filepath.Join(dir, "absent-mem"), maps, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := readProcMem(c.mem, c.maps)
			if r.OK != c.wantOK || r.Attack != AttackReadProc1Mem || !strings.Contains(r.Detail, c.wantDetail) {
				t.Fatalf("readProcMem = %+v; want OK=%v with detail containing %q", r, c.wantOK, c.wantDetail)
			}
			if !c.wantOK && r.Errno == "" {
				t.Fatalf("refused open = %+v; want an errno", r)
			}
		})
	}
}

// spec: §4.7.11 item 8 (Runtime supervisor, Property self-test)
// diagnosis: a failure means the maps parser picks an unreadable mapping or
// misreads the start address the breach confirmation reads from.
//
// TestFirstReadableMappingSkipsUnreadableMappings pins the address the
// memory attack confirms a breach at.
func TestFirstReadableMappingSkipsUnreadableMappings(t *testing.T) {
	cases := []struct {
		name, maps string
		want       int64
		wantErr    bool
	}{
		{"first readable", "7f0000000000-7f0000001000 ---p 0 0:0 0\n55d000000000-55d000001000 r--p 0 0:0 0 /sup\n", 0x55d000000000, false},
		{"malformed lines skipped", "garbage\nzz-10 r--p\n1000-2000 rw-p 0 0:0 0\n", 0x1000, false},
		{"none readable", "1000-2000 ---p 0 0:0 0\n", 0, true},
		{"empty", "", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "maps")
			if err := os.WriteFile(p, []byte(c.maps), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := firstReadableMapping(p)
			if (err != nil) != c.wantErr || got != c.want {
				t.Fatalf("firstReadableMapping = %#x, %v; want %#x, error %v", got, err, c.want, c.wantErr)
			}
		})
	}
}
