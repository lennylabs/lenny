// SPDX-License-Identifier: MIT

package heartbeatsilence_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/runtimekit"
	"github.com/lennylabs/lenny/pkg/runtimekit/echocore"
	"github.com/lennylabs/lenny/tests/testinfra/heartbeatsilence"
)

// childModeEnv makes the test binary act as the supervised child: it runs
// the echocore loop on stdin and stdout, the way cmd/runtimes/echo does
// without an adapter socket.
const childModeEnv = "HEARTBEAT_SILENCE_TEST_CHILD"

// childExitCodeEnv makes the child exit with the given code after its loop.
const childExitCodeEnv = "HEARTBEAT_SILENCE_TEST_EXIT"

func TestMain(m *testing.M) {
	if os.Getenv(childModeEnv) == "1" {
		os.Exit(runChild())
	}
	os.Exit(m.Run())
}

// runChild is the child process body. A socket variable in its environment
// is a fixture defect, reported with exit code 3.
func runChild() int {
	if _, ok := os.LookupEnv(runtimekit.SocketEnvVar); ok {
		fmt.Fprintln(os.Stderr, "child inherited the adapter socket variable")
		return 3
	}
	if err := echocore.Run(context.Background(), os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if code := os.Getenv(childExitCodeEnv); code != "" {
		var n int
		_, _ = fmt.Sscanf(code, "%d", &n)
		return n
	}
	return 0
}

func message(text string) string {
	return `{"type":"message","sessionId":"s1","input":[{"type":"text","inline":"` + text + `"}]}` + "\n"
}

const heartbeat = `{"type":"heartbeat","ts":1}` + "\n"

// spec: 28.5.3 (Intra-pod), 28.5.1 (Gateway-to-pod)
// Every frame passes through unchanged until the directive message has been
// forwarded; after it, heartbeats are dropped and every other frame still
// passes, so the child keeps answering messages but acks no heartbeat.
func TestFilterDropsHeartbeatsOnlyAfterTheDirective_spec_28_5_3(t *testing.T) {
	in := heartbeat + message("before") + heartbeat + message(heartbeatsilence.Directive) +
		heartbeat + message("after") + heartbeat
	var out, log bytes.Buffer
	if err := heartbeatsilence.Filter(strings.NewReader(in), &out, &log); err != nil {
		t.Fatalf("Filter: %v", err)
	}
	want := heartbeat + message("before") + heartbeat + message(heartbeatsilence.Directive) + message("after")
	if out.String() != want {
		t.Fatalf("forwarded:\n%s\nwant:\n%s", out.String(), want)
	}
	if !strings.Contains(log.String(), "directive received") {
		t.Errorf("log = %q, want the directive noted", log.String())
	}
}

// spec: 28.5.3 (Intra-pod)
// A message whose text is not exactly the directive, or whose directive text
// is not the first part, does not silence the heartbeats, and a line that
// does not parse is forwarded unchanged for the child to judge.
func TestFilterForwardsEverythingWithoutTheDirective_spec_28_5_3(t *testing.T) {
	notFirst := `{"type":"message","input":[{"type":"text","inline":"x"},{"type":"text","inline":"` +
		heartbeatsilence.Directive + `"}]}` + "\n"
	in := message("hello "+heartbeatsilence.Directive) + notFirst + "not json\n" + heartbeat
	var out bytes.Buffer
	if err := heartbeatsilence.Filter(strings.NewReader(in), &out, io.Discard); err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if out.String() != in {
		t.Fatalf("forwarded:\n%s\nwant every line unchanged:\n%s", out.String(), in)
	}
}

// spec: 28.5.3 (Intra-pod, Inbound: session_end)
// Each session_end is logged with the session it names and still forwarded.
func TestFilterLogsSessionEnd_spec_28_5_3(t *testing.T) {
	end := `{"type":"session_end","sessionId":"sess-42"}` + "\n"
	var out, log bytes.Buffer
	if err := heartbeatsilence.Filter(strings.NewReader(end), &out, &log); err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if out.String() != end {
		t.Errorf("forwarded %q, want the session_end unchanged", out.String())
	}
	if !strings.Contains(log.String(), heartbeatsilence.SessionEndLogPrefix+"sess-42") {
		t.Errorf("log = %q, want the session_end logged with its session", log.String())
	}
}

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken") }

// spec: 28.5.3 (Intra-pod)
// A forward that cannot be written ends the filter with a wrapped error.
func TestFilterReturnsAWriteFailure_spec_28_5_3(t *testing.T) {
	err := heartbeatsilence.Filter(strings.NewReader(heartbeat), failingWriter{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "forward \"heartbeat\" frame") {
		t.Fatalf("Filter error = %v, want the failed forward named", err)
	}
}

// spec: 4.7 (Runtime Adapter)
// The child's environment drops the adapter socket variable and keeps the
// rest, so the child selects the stdin/stdout transport.
func TestChildEnvDropsTheSocketVariable_spec_4_7(t *testing.T) {
	got := heartbeatsilence.ChildEnv([]string{"A=1", runtimekit.SocketEnvVar + "=@sock", "B=2"})
	if strings.Join(got, ",") != "A=1,B=2" {
		t.Fatalf("ChildEnv = %v, want A=1 and B=2 only", got)
	}
}

// childEnv returns the environment that runs this test binary as the child,
// with the socket variable set so the test also proves it is removed.
func childEnv(extra ...string) []string {
	env := append(os.Environ(), childModeEnv+"=1", runtimekit.SocketEnvVar+"=@unused")
	return append(env, extra...)
}

// lockedBuffer is a bytes.Buffer safe for the fixture's filter and the
// child's stderr copy, which write it concurrently.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func superviseEcho(t *testing.T, in string, extra ...string) (string, string, error) {
	t.Helper()
	var out bytes.Buffer
	var stderr lockedBuffer
	err := heartbeatsilence.Supervise(context.Background(),
		heartbeatsilence.Child{Path: os.Args[0], Env: childEnv(extra...)},
		strings.NewReader(in), &out, &stderr)
	return out.String(), stderr.String(), err
}

// spec: 28.5.3 (Intra-pod, CH-MSGSOCK Timing.), 15.4 (Runtime Adapter
// Specification)
// Behind the fixture, an unmodified echo loop acks the heartbeat before the
// directive, echoes the directive and the later message, and acks nothing
// after it; EOF on the adapter side ends the child cleanly.
func TestSuperviseSilencesTheChildsAcksAfterTheDirective_spec_28_5_3(t *testing.T) {
	in := heartbeat + message(heartbeatsilence.Directive) + heartbeat + message("after") + heartbeat
	out, stderr, err := superviseEcho(t, in)
	if err != nil {
		t.Fatalf("Supervise: %v (stderr %s)", err, stderr)
	}
	if n := strings.Count(out, "heartbeat_ack"); n != 1 {
		t.Errorf("child wrote %d heartbeat_ack frames, want only the one before the directive:\n%s", n, out)
	}
	if !strings.Contains(out, heartbeatsilence.Directive) || !strings.Contains(out, "after") {
		t.Errorf("child output %s, want the directive and the later message echoed", out)
	}
}

// spec: 15.4 (Runtime Adapter Specification)
// The child's non-zero exit reaches the caller as its exit code, so the
// fixture's entrypoint can exit with the code the adapter reads.
func TestSupervisePassesTheChildsExitCode_spec_15_4(t *testing.T) {
	_, stderr, err := superviseEcho(t, heartbeat, childExitCodeEnv+"=2")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
		t.Fatalf("Supervise error = %v, want the child's exit code 2 (stderr %s)", err, stderr)
	}
}

// spec: 15.4 (Runtime Adapter Specification)
// A child binary that cannot be started is a wrapped start error.
func TestSuperviseReportsAnUnstartableChild_spec_15_4(t *testing.T) {
	err := heartbeatsilence.Supervise(context.Background(),
		heartbeatsilence.Child{Path: "/nonexistent/runtime"}, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "start child runtime") {
		t.Fatalf("Supervise error = %v, want a start error", err)
	}
}
