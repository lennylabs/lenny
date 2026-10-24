// SPDX-License-Identifier: MIT

package adapter

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/supervisorselftest"
	"github.com/lennylabs/lenny/tests/testinfra/runtimenonce"
)

// This file holds the in-process fake runtime supervisor the tier-1 tests of
// the CH-SUPERVISE client and the supervised runtime run against. The fake
// dials the adapter's socket, sends ready, and answers each request with a
// programmable reaction; by default a launch also dials the runtime's
// CH-MSGSOCK socket as the launched process would.

// uniqueSocket returns an abstract socket name no other test uses.
func uniqueSocket(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "@lenny-test-" + prefix + "-" + hex.EncodeToString(b[:])
}

// fakeReaction is what the fake does with one request.
type fakeReaction struct {
	// errCode, when set, is the reply's error.
	errCode string
	// silent sends no reply.
	silent bool
	// closeConn closes the connection instead of replying.
	closeConn bool
	// raw, when set, is written instead of the reply.
	raw string
	// delay holds the reply back.
	delay time.Duration
	// noProcess skips the launched process's dial on a launch.
	noProcess bool
	// exitAfterLaunch sends exited after launched with this status.
	exitAfterLaunch *int
}

// fakeSupervisor is a scripted runtime supervisor peer.
type fakeSupervisor struct {
	t       *testing.T
	conn    net.Conn
	r       *bufio.Reader
	wmu     sync.Mutex
	runtime string // the CH-MSGSOCK socket the launched process dials
	nonce   func() string

	mu        sync.Mutex
	requests  []map[string]any
	reactions map[string][]fakeReaction
	procs     []net.Conn
	gen       uint64
	// liveGen is the generation launched and not yet ended; a launch while
	// it is set is a launch the real supervisor would refuse.
	liveGen         uint64
	launchViolation bool
	done            chan struct{}
}

// dialFakeSupervisor dials socket until it connects, sends the ready line,
// and starts serving.
func dialFakeSupervisor(t *testing.T, socket, ready string) *fakeSupervisor {
	t.Helper()
	conn := dialUntil(t, socket)
	f := &fakeSupervisor{t: t, conn: conn, r: bufio.NewReader(conn), reactions: map[string][]fakeReaction{}, done: make(chan struct{})}
	if ready != "" {
		f.writeLine(ready)
	}
	t.Cleanup(f.close)
	return f
}

// dialUntil dials an abstract socket, repeating a refused dial for a while.
func dialUntil(t *testing.T, socket string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.Dial("unix", socket)
		if err == nil {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial %s: %v", socket, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// readyLine renders a ready message with every property passing except the
// named ones.
func readyLine(failing ...string) string {
	props := map[string]string{}
	for _, p := range supervisorselftest.Properties {
		props[p] = "pass"
	}
	for _, p := range failing {
		props[p] = "fail"
	}
	b, _ := json.Marshal(map[string]any{"type": "ready", "gen": 0, "properties": props})
	return string(b)
}

// react queues a reaction for the next request of type typ.
func (f *fakeSupervisor) react(typ string, r fakeReaction) {
	f.mu.Lock()
	f.reactions[typ] = append(f.reactions[typ], r)
	f.mu.Unlock()
}

func (f *fakeSupervisor) writeLine(line string) {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	_, _ = f.conn.Write([]byte(line + "\n"))
}

// serve answers requests until the connection closes.
func (f *fakeSupervisor) serve() {
	go func() {
		defer close(f.done)
		for {
			line, err := f.r.ReadBytes('\n')
			if err != nil {
				return
			}
			var req map[string]any
			if json.Unmarshal(line, &req) != nil {
				return
			}
			f.mu.Lock()
			f.requests = append(f.requests, req)
			f.mu.Unlock()
			f.answer(req)
		}
	}()
}

// nextReaction pops the queued reaction for typ.
func (f *fakeSupervisor) nextReaction(typ string) fakeReaction {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.reactions[typ]
	if len(q) == 0 {
		return fakeReaction{}
	}
	f.reactions[typ] = q[1:]
	return q[0]
}

// answer applies the reaction for req.
func (f *fakeSupervisor) answer(req map[string]any) {
	typ, _ := req["type"].(string)
	gen := uint64(req["gen"].(float64))
	id := uint64(req["id"].(float64))
	r := f.nextReaction(typ)
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	switch {
	case r.closeConn:
		_ = f.conn.Close()
		return
	case r.silent:
		return
	case r.raw != "":
		f.writeLine(r.raw)
		return
	}
	f.sideEffect(typ, gen, r)
	reply := map[string]any{"type": superviseReplyType[typ], "gen": gen, "id": id}
	if r.errCode != "" {
		reply["error"] = r.errCode
	}
	b, _ := json.Marshal(reply)
	f.writeLine(string(b))
	if typ == superviseLaunch && r.exitAfterLaunch != nil {
		f.writeLine(fmt.Sprintf(`{"type":"exited","gen":%d,"status":%d}`, gen, *r.exitAfterLaunch))
	}
}

// sideEffect performs what the real supervisor does for a successful request:
// a launch starts a process that dials CH-MSGSOCK, and an end or a sweep ends
// every process.
func (f *fakeSupervisor) sideEffect(typ string, gen uint64, r fakeReaction) {
	if r.errCode != "" {
		return
	}
	switch typ {
	case superviseLaunch:
		f.mu.Lock()
		if f.liveGen != 0 {
			f.launchViolation = true
		}
		f.gen, f.liveGen = gen, gen
		f.mu.Unlock()
		if !r.noProcess && f.runtime != "" {
			go f.startProcess()
		}
	case superviseEnd, superviseSweep:
		f.mu.Lock()
		f.liveGen = 0
		f.mu.Unlock()
		f.killProcesses()
	}
}

// startProcess dials the CH-MSGSOCK socket with the published nonce, as a
// launched runtime process does.
func (f *fakeSupervisor) startProcess() {
	c, err := net.Dial("unix", f.runtime)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.procs = append(f.procs, c)
	f.mu.Unlock()
	if f.nonce != nil {
		_ = runtimenonce.Write(c, f.nonce())
	}
}

// killProcesses closes every launched process's connection.
func (f *fakeSupervisor) killProcesses() {
	f.mu.Lock()
	procs := f.procs
	f.procs = nil
	f.mu.Unlock()
	for _, c := range procs {
		_ = c.Close()
	}
}

// sent returns the types of the requests received so far.
func (f *fakeSupervisor) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.requests))
	for _, r := range f.requests {
		out = append(out, r["type"].(string))
	}
	return out
}

// count returns how many requests of typ were received.
func (f *fakeSupervisor) count(typ string) int {
	n := 0
	for _, s := range f.sent() {
		if s == typ {
			n++
		}
	}
	return n
}

// request returns the i-th received request of typ.
func (f *fakeSupervisor) request(typ string, i int) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		if r["type"] == typ {
			if i == 0 {
				return r
			}
			i--
		}
	}
	return nil
}

func (f *fakeSupervisor) close() {
	_ = f.conn.Close()
	f.killProcesses()
}

// supervisedFixture is a SupervisedRuntime attached to a fake supervisor.
type supervisedFixture struct {
	rt      *SupervisedRuntime
	sup     *fakeSupervisor
	link    *SupervisorLink
	runtime string
	nonce   string
}

// newSupervisedFixture builds a supervised runtime whose CH-MSGSOCK listener
// reads a published test manifest's nonce, awaits the fake supervisor, and
// attaches the link. ops, when set, is the CH-RUNTIMEOPS channel.
func newSupervisedFixture(t *testing.T, lifetime RuntimeLifetime, mode LaunchMode, ops *RuntimeOps) *supervisedFixture {
	t.Helper()
	m := runtimenonce.Publish(t, nil)
	runtimeSock := uniqueSocket("msg")
	auth := SocketPeerAuth{NonceOnly: false, ExpectedUID: currentUID()}
	rt, err := NewSupervisedRuntime(SupervisedRuntimeConfig{
		Lifetime: lifetime, Mode: mode, RuntimeOps: ops,
		NewTransport: func() (*SocketRuntimeProcess, error) {
			return NewSocketRuntimeProcess(runtimeSock, auth, PublishedManifestNonce(m.Dir))
		},
	})
	if err != nil {
		t.Fatalf("NewSupervisedRuntime: %v", err)
	}
	t.Cleanup(func() { _ = rt.CloseListener() })
	supSock := uniqueSocket("sup")
	var sup *fakeSupervisor
	done := make(chan struct{})
	go func() {
		defer close(done)
		sup = dialFakeSupervisor(t, supSock, readyLine())
	}()
	link, err := AwaitSupervisor(context.Background(), AwaitSupervisorConfig{
		Socket: supSock, Auth: auth, Lifetime: lifetime, Timeout: 5 * time.Second,
	})
	<-done
	if err != nil {
		t.Fatalf("AwaitSupervisor: %v", err)
	}
	sup.runtime = runtimeSock
	sup.nonce = func() string { return m.Nonce }
	sup.serve()
	rt.AttachLink(link)
	return &supervisedFixture{rt: rt, sup: sup, link: link, runtime: runtimeSock, nonce: m.Nonce}
}

// currentUID is the test process's UID, which the peer check admits.
func currentUID() uint32 { return uint32(os.Getuid()) }
