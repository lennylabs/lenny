// SPDX-License-Identifier: MIT

//go:build linux

package superviseprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/sdks/runtime/go/runtime"
)

// spec: §4.7.10 (Runtime supervisor, Launch)
//
// TestProcessTokenReadsTheStartTimeAfterTheCommField pins the token a
// cluster test compares across sessions: the start time is field 22 of
// /proc/<pid>/stat, counted after the parenthesized comm field, which may
// hold spaces and parentheses.
func TestProcessTokenReadsTheStartTimeAfterTheCommField(t *testing.T) {
	fields := make([]string, 0, 50)
	for i := 3; i <= 52; i++ {
		fields = append(fields, fmt.Sprint(i))
	}
	tail := strings.Join(fields, " ")
	cases := []struct {
		name, stat, want string
		wantErr          bool
	}{
		{name: "plain comm", stat: "41 (lenny) " + tail, want: "41:22"},
		{name: "comm with spaces and parens", stat: "41 (a b) c)) " + tail, want: "41:22"},
		{name: "no comm", stat: "41 lenny " + tail, wantErr: true},
		{name: "truncated", stat: "41 (lenny) S 1 2", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ProcessToken(41, tc.stat)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ProcessToken = %q, want an error", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("ProcessToken = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if tok, err := selfToken(); err != nil || !strings.HasPrefix(tok, fmt.Sprint(os.Getpid())+":") {
		t.Fatalf("selfToken = %q, %v; want this process's PID prefix", tok, err)
	}
}

// spec: §4.7.10 (Runtime supervisor, Launch)
//
// TestParseDialDelayRefusesNegativeAndMalformedValues pins the dial-delay
// parse: empty is no delay, and a negative or malformed value fails the
// probe at start rather than racing the adapter's warm-up wait.
func TestParseDialDelayRefusesNegativeAndMalformedValues(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "", want: 0},
		{in: " 30s ", want: 30 * time.Second},
		{in: "-1s", wantErr: true},
		{in: "soon", wantErr: true},
	}
	for _, tc := range cases {
		got, err := ParseDialDelay(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("ParseDialDelay(%q) = %v, %v; want %v, error %v", tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}

// spec: §4.7.6 (Adapter Manifest Field Reference, mcpNonce row)
//
// TestReadManifestViewDigestsTheNonce pins the manifest view: the nonce is
// reported as a digest, a platform MCP socket is detected, and a missing or
// malformed file is a view that is not present.
func TestReadManifestViewDigestsTheNonce(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "m.json")
	if err := os.WriteFile(good, []byte(`{"mcpNonce":"abc","platformMcpServer":{"socket":"@x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	v := ReadManifestView(good)
	if !v.Present || !v.PlatformMCP || v.NonceDigest != nonceDigest("abc") || strings.Contains(v.NonceDigest, "abc") {
		t.Fatalf("view = %+v; want present, platform MCP, and a digest of the nonce", v)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{bad, filepath.Join(dir, "absent.json")} {
		if v := ReadManifestView(p); v.Present || v.Error == "" {
			t.Errorf("ReadManifestView(%s) = %+v; want not present with an error", p, v)
		}
	}
	if nonceDigest("") != "" {
		t.Error("an empty nonce has a digest")
	}
	t.Setenv(manifestEnvVar, good)
	if manifestPath() != good {
		t.Errorf("manifestPath = %q, want the override %q", manifestPath(), good)
	}
}

// fakeSink records what the record server stores.
type fakeSink struct {
	mu    sync.Mutex
	links []string
	recs  []CleanupRecord
}

func (f *fakeSink) plantedLinks() []string { return f.links }

func (f *fakeSink) addCleanup(r CleanupRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recs = append(f.recs, r)
}

// spec: §5.2 (Supervised scrub sequence, cleanupCommands)
//
// TestCleanupRecordReachesTheRecordServer drives the cleanup command's path
// to the serving probe: it asks for the planted links, tries each, and the
// server stores a record with the command's UID, working directory, and
// environment. A second bind of the same name is reported rather than
// fatal.
func TestCleanupRecordReachesTheRecordServer(t *testing.T) {
	link := filepath.Join(t.TempDir(), "link")
	target := t.TempDir()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	sink := &fakeSink{links: []string{link}}
	socket := fmt.Sprintf("@lenny-probe-record-test-%d", time.Now().UnixNano())
	srv := startRecordServer(socket, sink)
	if srv.err != nil {
		t.Fatalf("start record server: %v", srv.err)
	}
	defer srv.close()
	if dup := startRecordServer(socket, sink); dup.err == nil {
		dup.close()
		t.Fatal("a second record server bound the same name")
	}
	if err := sendCleanupRecord(socket); err != nil {
		t.Fatalf("sendCleanupRecord: %v", err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.recs) != 1 {
		t.Fatalf("server stored %d records, want 1", len(sink.recs))
	}
	rec := sink.recs[0]
	if rec.UID != os.Getuid() || rec.Cwd == "" || len(rec.Env) == 0 {
		t.Errorf("record = %+v; want this process's UID, cwd, and environment", rec)
	}
	if len(rec.Link) != 2 || !rec.Link[0].OK || !rec.Link[1].OK {
		t.Errorf("link attempts = %+v; want a list and a write through the link to a writable target", rec.Link)
	}
	if err := sendCleanupRecord(socket + "-absent"); err == nil {
		t.Error("sendCleanupRecord to an unbound name succeeded")
	}
}

// spec: §5.2 (Supervised scrub sequence, cleanupCommands)
//
// TestAccessAttemptsReportTheErrno pins the access outcome a cluster test
// compares between the agent and a cleanup command: a denied write carries
// its symbolic errno.
func TestAccessAttemptsReportTheErrno(t *testing.T) {
	got := accessAttempts(filepath.Join(t.TempDir(), "absent"))
	if len(got) != 2 || got[0].OK || got[1].OK || got[0].Errno != "ENOENT" || got[1].Errno != "ENOENT" {
		t.Fatalf("attempts = %+v; want a failed list and write with ENOENT", got)
	}
	if errnoName(fmt.Errorf("plain")) != "plain" {
		t.Error("an error without an errno is not reported by its text")
	}
}

// spec: §4.7.10 (Runtime supervisor, Sweep)
//
// TestIPCResidueIsReportedUntilRemoved creates the IPC objects a session
// leaves and checks that the residue check reports each one until it is
// removed, which is what the cluster test reads after a `restart` pod's
// sweep.
func TestIPCResidueIsReportedUntilRemoved(t *testing.T) {
	res := createIPC()
	if !res.OK {
		t.Skipf("blocked: the host refuses System V or POSIX IPC creation: %s", res.Error)
	}
	arg, err := json.Marshal(res.Residue)
	if err != nil {
		t.Fatal(err)
	}
	before := checkResidue(string(arg))
	removeIPC(t, res.Residue)
	after := checkResidue(string(arg))
	if !before.ShmExists || !before.MsqExists || !before.MQExists {
		t.Errorf("before removal = %+v; want every object present", before)
	}
	if after.ShmExists || after.MsqExists || after.MQExists {
		t.Errorf("after removal = %+v; want every object gone", after)
	}
	if got := checkResidue("{"); got != (ResidueResult{}) {
		t.Errorf("malformed residue = %+v; want nothing reported", got)
	}
}

// spec: §4.7.10 (Runtime supervisor, Sweep)
//
// TestResidueReportsALinkAndNoDaemonForAReusedPID pins the link and daemon
// checks: a planted link is reported until removed, and a PID whose command
// line is not the probe daemon is not reported alive.
func TestResidueReportsALinkAndNoDaemonForAReusedPID(t *testing.T) {
	link := filepath.Join(t.TempDir(), "l")
	if err := os.Symlink("/run/lenny", link); err != nil {
		t.Fatal(err)
	}
	arg, _ := json.Marshal(Residue{Link: link, DaemonPID: os.Getpid()})
	got := checkResidue(string(arg))
	if !got.LinkExists || got.DaemonAlive {
		t.Fatalf("residue = %+v; want the link present and this test process not mistaken for the daemon", got)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if checkResidue(string(arg)).LinkExists {
		t.Error("a removed link is reported present")
	}
}

// spec: §4.7.10 (Runtime supervisor, Liveness)
//
// TestServingProbeExcludesPID1AndSubcommands pins the process the
// kill-runtime subcommand targets: the probe binary with no subcommand,
// outside PID 1.
func TestServingProbeExcludesPID1AndSubcommands(t *testing.T) {
	exe := "/usr/local/bin/lenny"
	cases := []struct {
		p    Process
		want bool
	}{
		{Process{PID: 7, Cmdline: []string{exe}, State: "S"}, true},
		{Process{PID: 1, Cmdline: []string{exe}, State: "S"}, false},
		{Process{PID: 7, Cmdline: []string{exe, SubKillRuntime}, State: "R"}, false},
		{Process{PID: 7, Cmdline: []string{exe}, State: "Z"}, false},
		{Process{PID: 7, Cmdline: []string{"/other"}, State: "S"}, false},
	}
	for _, tc := range cases {
		if got := IsServingProbe(tc.p, exe); got != tc.want {
			t.Errorf("IsServingProbe(%+v) = %v, want %v", tc.p, got, tc.want)
		}
	}
	procs, err := listProcesses()
	if err != nil || len(procs) == 0 {
		t.Fatalf("listProcesses = %d entries, %v", len(procs), err)
	}
	found := false
	for _, p := range procs {
		found = found || p.PID == os.Getpid()
	}
	if !found {
		t.Error("listProcesses omits this process")
	}
}

// spec: §4.7.10 (Runtime supervisor, End; Sweep), §5.2 (Supervised scrub
// sequence)
//
// TestProbeDaemonMatchesOnlyTheLiveGrandchild pins the process a restart-pod
// case looks for after the scrub: the daemon grandchild CmdDaemon or a
// cleanup command leaves, and neither the serving probe, the short-lived
// daemon child, nor a reaped zombie.
func TestProbeDaemonMatchesOnlyTheLiveGrandchild(t *testing.T) {
	exe := "/usr/local/bin/lenny"
	cases := []struct {
		p    Process
		want bool
	}{
		{Process{PID: 9, Cmdline: []string{exe, subDaemonGrandchild}, State: "S"}, true},
		{Process{PID: 9, Cmdline: []string{exe, subDaemonGrandchild}, State: "Z"}, false},
		{Process{PID: 9, Cmdline: []string{exe, SubDaemonChild}, State: "S"}, false},
		{Process{PID: 9, Cmdline: []string{exe}, State: "S"}, false},
		{Process{PID: 9, Cmdline: []string{"/other", subDaemonGrandchild}, State: "S"}, false},
	}
	for _, tc := range cases {
		if got := IsProbeDaemon(tc.p, exe); got != tc.want {
			t.Errorf("IsProbeDaemon(%+v) = %v, want %v", tc.p, got, tc.want)
		}
	}
}

// spec: §4.7.11 item 8 (Runtime supervisor)
//
// TestAttacksReportTheirOutcome pins the attack dispatch: an unknown or
// missing attack is reported, a write under an absent supervisor mount
// fails, and a malformed signal argument is refused without a signal.
func TestAttacksReportTheirOutcome(t *testing.T) {
	if r := runAttack(nil); r.Detail == "" {
		t.Error("no attack named is not reported")
	}
	if r := runAttack([]string{"nope"}); r.OK || r.Detail != "unknown attack" {
		t.Errorf("unknown attack = %+v", r)
	}
	if _, err := os.Stat(SupervisorMount); err == nil {
		t.Skip("blocked: the host has a supervisor mount; this case needs a host without one")
	}
	if r := runAttack([]string{AttackWriteSupervisor}); r.OK || r.Errno == "" {
		t.Errorf("write under an absent mount = %+v; want a failure with an errno", r)
	}
	for _, args := range [][]string{{AttackSignalPID1}, {AttackSignalPID1, "TERM"}} {
		if r := runAttack(args); r.OK || r.Detail == "" {
			t.Errorf("runAttack(%v) = %+v; want a refusal", args, r)
		}
	}
}

// spec: §4.7.10 (Runtime supervisor)
//
// TestMainDispatchesSubcommands pins the subcommand dispatch: an unknown
// subcommand is a protocol error, and the processes subcommand prints a
// JSON array.
func TestMainDispatchesSubcommands(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Main([]string{"nope"}, &out, &errOut); code != exitProtocolError {
		t.Errorf("unknown subcommand exit = %d, want %d", code, exitProtocolError)
	}
	out.Reset()
	if code := Main([]string{SubProcesses}, &out, &errOut); code != exitOK {
		t.Fatalf("processes exit = %d; stderr %s", code, errOut.String())
	}
	var procs []Process
	if err := json.Unmarshal(out.Bytes(), &procs); err != nil || len(procs) == 0 {
		t.Fatalf("processes output %q: %v", out.String(), err)
	}
	out.Reset()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := Main([]string{SubList, dir}, &out, &errOut); code != exitOK || strings.TrimSpace(out.String()) != `["f"]` {
		t.Errorf("list output %q, exit %d; want [\"f\"]", out.String(), code)
	}
	for _, args := range [][]string{{SubList}, {SubList, filepath.Join(dir, "absent")}} {
		out.Reset()
		if Main(args, &out, &errOut); !strings.Contains(out.String(), `"attack":"list"`) {
			t.Errorf("Main(%v) output %q; want an AttackResult", args, out.String())
		}
	}
	out.Reset()
	if code := Main([]string{SubAttack, "nope"}, &out, &errOut); code != exitOK || !strings.Contains(out.String(), "unknown attack") {
		t.Errorf("attack output %q, exit %d", out.String(), code)
	}
}

// spec: §4.7.10 (Runtime supervisor, Launch)
//
// TestHandlerAnswersReportAndEchoesOtherText pins the session commands:
// report answers with the process's token and environment, a link plant is
// reported, and a message naming no command is echoed.
func TestHandlerAnswersReportAndEchoesOtherText(t *testing.T) {
	t.Setenv(manifestEnvVar, filepath.Join(t.TempDir(), "absent.json"))
	t.Setenv(DialDelayEnvVar, "")
	st, err := newState(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	h := &handler{st: st}
	reply := sendText(t, h, CmdReport)
	var rec Record
	if err := json.Unmarshal([]byte(reply), &rec); err != nil {
		t.Fatalf("report %q: %v", reply, err)
	}
	if rec.Token != st.token || rec.PID != os.Getpid() || len(rec.Env) == 0 || rec.ManifestAtStart.Present {
		t.Errorf("record = %+v; want this process's token and environment and no manifest", rec)
	}
	if got := sendText(t, h, "hello"); got != "hello" {
		t.Errorf("echo = %q, want hello", got)
	}
	var mcp ActionResult
	if err := json.Unmarshal([]byte(sendText(t, h, CmdMCP)), &mcp); err != nil || mcp.OK {
		t.Errorf("mcp without a connection = %+v, %v; want a failure", mcp, err)
	}
	var plant ActionResult
	if err := json.Unmarshal([]byte(sendText(t, h, CmdSymlink)), &plant); err != nil {
		t.Fatal(err)
	}
	if plant.OK {
		t.Cleanup(func() { _ = os.Remove(plant.Residue.Link) })
		if len(st.plantedLinks()) != 1 {
			t.Errorf("planted links = %v; want the one link", st.plantedLinks())
		}
	} else if plant.Error == "" {
		t.Error("a failed link plant carries no error")
	}
}

// sendText sends one text message through h and returns the reply text.
func sendText(t *testing.T, h *handler, text string) string {
	t.Helper()
	msg := runtime.Message{SessionID: "s1", Envelope: &runtime.MessageEnvelope{Input: []runtime.MessagePart{runtime.Text(text)}}}
	reply, err := h.OnMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("OnMessage(%q): %v", text, err)
	}
	if len(reply.Parts) != 1 {
		t.Fatalf("OnMessage(%q) parts = %+v", text, reply.Parts)
	}
	return reply.Parts[0].Inline
}
