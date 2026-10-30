// SPDX-License-Identifier: MIT

//go:build linux

package superviseprobe

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeSupervisor dials the peer's socket, sends ready with props, and
// answers pongs pongs before it closes the connection.
func fakeSupervisor(t *testing.T, props map[string]string, pongs int) {
	t.Helper()
	var conn net.Conn
	var err error
	for i := 0; i < 100; i++ {
		if conn, err = net.Dial("unix", abstractAddr(SuperviseSocket)); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Errorf("dial the peer: %v", err)
		return
	}
	defer conn.Close()
	ready, _ := json.Marshal(map[string]any{"type": "ready", "gen": 0, "properties": props})
	if _, err := conn.Write(append(ready, '\n')); err != nil {
		t.Errorf("send ready: %v", err)
		return
	}
	r := bufio.NewReader(conn)
	for i := 0; i < pongs; i++ {
		line, err := r.ReadBytes('\n')
		if err != nil {
			t.Errorf("read ping: %v", err)
			return
		}
		var req struct {
			Type string `json:"type"`
			Gen  uint64 `json:"gen"`
			ID   uint64 `json:"id"`
		}
		if err := json.Unmarshal(line, &req); err != nil || req.Type != "ping" {
			t.Errorf("request %q is not a ping: %v", line, err)
			return
		}
		pong, _ := json.Marshal(map[string]any{"type": "pong", "gen": req.Gen, "id": req.ID})
		if _, err := conn.Write(append(pong, '\n')); err != nil {
			t.Errorf("send pong: %v", err)
			return
		}
	}
}

// spec: §28.5.3 (CH-SUPERVISE), §4.7.10 (Runtime supervisor, Liveness)
//
// TestSupervisePeerReportsReadyAndExitsOnLinkFailure pins the stand-in
// adapter the sandboxed tier-6 pods run: it prints the ready's properties,
// keeps pinging while pongs arrive, and on a closed link prints
// PeerLinkFailed and exits non-zero, the outcome a test reads as a
// supervisor that stopped answering.
func TestSupervisePeerReportsReadyAndExitsOnLinkFailure(t *testing.T) {
	props := map[string]string{
		"nondumpable": "pass", "killReach": "pass", "initSigkillImmune": "fail", "initSigstopImmune": "pass",
	}
	done := make(chan struct{})
	go func() { defer close(done); fakeSupervisor(t, props, 2) }()
	var out, errOut bytes.Buffer
	code := Main([]string{
		SubSupervisePeer, "--agent-uid", strconv.Itoa(os.Getuid()),
		"--ready-timeout", "5s", "--ping-interval", "50ms", "--ping-timeout", "500ms",
	}, &out, &errOut)
	<-done
	if code != exitRuntimeError {
		t.Fatalf("peer exit = %d after the link closed, want %d; stderr %s", code, exitRuntimeError, errOut.String())
	}
	evs := ParsePeerEvents(out.String() + "\n" + errOut.String())
	if len(evs) != 2 || evs[0].Event != PeerReady || evs[1].Event != PeerLinkFailed {
		t.Fatalf("peer events = %+v, want ready then link-failed", evs)
	}
	if !evs[0].Properties["initSigstopImmune"] || evs[0].Properties["initSigkillImmune"] {
		t.Errorf("ready properties = %v, want the supervisor's report", evs[0].Properties)
	}
}

// spec: §28.5.3 (CH-SUPERVISE, Preconditions)
//
// TestSupervisePeerFailsWithNoSupervisorAndOnBadFlags pins the peer's
// failure paths: no connection within the ready timeout is a link failure,
// and a missing agent UID or a non-positive ping bound is a usage error.
func TestSupervisePeerFailsWithNoSupervisorAndOnBadFlags(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Main([]string{SubSupervisePeer, "--agent-uid", "1", "--ready-timeout", "100ms"}, &out, &errOut)
	if code != exitRuntimeError || !strings.Contains(out.String(), `"event":"link-failed"`) {
		t.Errorf("no supervisor: exit %d, output %q; want a link failure", code, out.String())
	}
	for _, args := range [][]string{{}, {"--agent-uid", "1", "--ping-interval", "0s"}, {"--bogus"}} {
		out.Reset()
		if code := Main(append([]string{SubSupervisePeer}, args...), &out, &errOut); code != exitProtocolError {
			t.Errorf("peer %v exit = %d, want %d", args, code, exitProtocolError)
		}
	}
}

// spec: §5.2 (Supervised scrub sequence)
//
// TestSetupResidueCheckReportsTheSegmentUnderItsKey pins the adapter-side
// residue reading: a segment under the key is reported until removed, and
// a key that is missing or not a positive integer is a usage error.
func TestSetupResidueCheckReportsTheSegmentUnderItsKey(t *testing.T) {
	key := int(time.Now().UnixNano()%(1<<30)) + 1
	check := func() SetupResidue {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := Main([]string{SubSetupResidueCheck, strconv.Itoa(key)}, &out, &errOut); code != exitOK {
			t.Fatalf("check exit = %d; stderr %s", code, errOut.String())
		}
		var r SetupResidue
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("decode %q: %v", out.String(), err)
		}
		return r
	}
	if r := check(); r.ShmExists || r.Key != key || r.DaemonPIDs == nil {
		t.Fatalf("before shmget: %+v, want no segment and an empty daemon list", r)
	}
	id, err := sysvShmCreate(key)
	if err != nil {
		t.Fatal(err)
	}
	if r := check(); !r.ShmExists || len(r.DaemonPIDs) != 0 {
		t.Errorf("after shmget: %+v, want the segment and no daemon", r)
	}
	if err := sysvShmRemove(id); err != nil {
		t.Fatal(err)
	}
	if r := check(); r.ShmExists {
		t.Errorf("after removal: %+v, want no segment", r)
	}
	for _, args := range [][]string{{SubSetupResidue}, {SubSetupResidueCheck, "x"}, {SubSetupResidueCheck, "0"}} {
		var out, errOut bytes.Buffer
		if code := Main(args, &out, &errOut); code != exitProtocolError {
			t.Errorf("Main(%v) exit = %d, want %d", args, code, exitProtocolError)
		}
	}
}
