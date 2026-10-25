// SPDX-License-Identifier: MIT

package superviseprobe

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// recordRequest is one line a cleanup command writes to the record server:
// a request for the planted links, or a record to store.
type recordRequest struct {
	Op     string         `json:"op"`
	Record *CleanupRecord `json:"record,omitempty"`
}

// Record-server operations.
const (
	opLinks  = "links"
	opRecord = "record"
)

// recordSink is the state the record server reads and writes.
type recordSink interface {
	plantedLinks() []string
	addCleanup(CleanupRecord)
}

// recordServer serves recordRequests on an abstract socket.
type recordServer struct {
	ln  net.Listener
	err error
}

// startRecordServer listens on socket and serves sink. A bind failure is
// kept in err and reported in the Record, so the probe still serves
// sessions on a pod where a second process holds the name.
func startRecordServer(socket string, sink recordSink) *recordServer {
	ln, err := net.Listen("unix", abstractAddr(socket))
	if err != nil {
		return &recordServer{err: fmt.Errorf("listen %s: %w", socket, err)}
	}
	s := &recordServer{ln: ln}
	go s.acceptLoop(sink)
	return s
}

// acceptLoop serves each connection until the listener closes.
func (s *recordServer) acceptLoop(sink recordSink) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go serveRecordConn(conn, sink)
	}
}

// serveRecordConn answers each request line on conn.
func serveRecordConn(conn net.Conn, sink recordSink) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	sc := bufio.NewScanner(conn)
	enc := json.NewEncoder(conn)
	for sc.Scan() {
		var req recordRequest
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			return
		}
		switch req.Op {
		case opLinks:
			if enc.Encode(sink.plantedLinks()) != nil {
				return
			}
		case opRecord:
			if req.Record != nil {
				sink.addCleanup(*req.Record)
			}
			if enc.Encode(true) != nil {
				return
			}
		default:
			return
		}
	}
}

// close stops the listener.
func (s *recordServer) close() {
	if s.ln != nil {
		_ = s.ln.Close()
	}
}

// abstractAddr turns an "@name" abstract socket name into the address the
// net package dials.
func abstractAddr(name string) string {
	if strings.HasPrefix(name, "@") {
		return "\x00" + name[1:]
	}
	return name
}

// reportCleanup is the cleanupCommands entry. It reads its identity,
// working directory, and environment, tries each planted link, and sends
// the record to the serving probe. spec: §5.2 (Supervised scrub sequence,
// cleanupCommands); §4.7.10 (Runtime supervisor, Cleanup).
func reportCleanup(stderr io.Writer) int {
	if err := sendCleanupRecord(RecordSocket); err != nil {
		fmt.Fprintf(stderr, "supervise-probe: cleanup record: %v\n", err)
		return exitRuntimeError
	}
	return exitOK
}

// sendCleanupRecord asks the record server at socket for the planted links,
// builds the record, and sends it.
func sendCleanupRecord(socket string) error {
	conn, err := net.DialTimeout("unix", abstractAddr(socket), 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", socket, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	if err := enc.Encode(recordRequest{Op: opLinks}); err != nil {
		return fmt.Errorf("request links: %w", err)
	}
	var links []string
	if err := dec.Decode(&links); err != nil {
		return fmt.Errorf("read links: %w", err)
	}
	rec := buildCleanupRecord(links)
	if err := enc.Encode(recordRequest{Op: opRecord, Record: &rec}); err != nil {
		return fmt.Errorf("send record: %w", err)
	}
	var ack bool
	if err := dec.Decode(&ack); err != nil {
		return fmt.Errorf("read ack: %w", err)
	}
	return nil
}

// buildCleanupRecord is the calling process's CleanupRecord.
func buildCleanupRecord(links []string) CleanupRecord {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "error: " + err.Error()
	}
	rec := CleanupRecord{UID: os.Getuid(), GID: os.Getgid(), Cwd: cwd, Env: environment(), Link: []Access{}}
	for _, l := range links {
		rec.Link = append(rec.Link, accessAttempts(l)...)
	}
	return rec
}

// accessAttempts lists, reads, and writes under dir, and returns the outcome
// of each. The write's file is removed when it succeeds.
func accessAttempts(dir string) []Access {
	out := make([]Access, 0, 2)
	_, err := os.ReadDir(dir)
	out = append(out, newAccess(dir, "list", err))
	probe := filepath.Join(dir, fmt.Sprintf("probe-write-%d", time.Now().UnixNano()))
	werr := os.WriteFile(probe, []byte("probe"), 0o600)
	if werr == nil {
		_ = os.Remove(probe)
	}
	return append(out, newAccess(dir, "write", werr))
}

// newAccess builds one Access from an operation's error.
func newAccess(path, op string, err error) Access {
	a := Access{Path: path, Op: op, OK: err == nil}
	if err != nil {
		a.Errno = errnoName(err)
	}
	return a
}

// errnoName is the symbolic errno of err, such as EACCES, or its text when
// it carries no errno.
func errnoName(err error) string {
	var en syscall.Errno
	if errors.As(err, &en) {
		if name := unix.ErrnoName(en); name != "" {
			return name
		}
	}
	return err.Error()
}
