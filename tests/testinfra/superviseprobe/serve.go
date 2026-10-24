// SPDX-License-Identifier: MIT

package superviseprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/lennylabs/lenny/sdks/runtime/go/runtime"
)

// serve records the process identity, sleeps the dial delay when this is
// the pod's warm-up launch (claimDialDelay), opens its platform MCP
// connection with the manifest nonce it read at start, and serves sessions
// through the runtime SDK until the adapter closes CH-MSGSOCK. A later
// launch in the pod dials without the delay. The manifest is read before
// the dial, so the report shows which manifest the process saw at start.
// spec: §4.7.10 (Runtime supervisor, Launch; SDK-warm); §4.7.9 step 7;
// §15.4.3 (Authentication).
func serve(stderr io.Writer) int {
	st, err := newState(stderr, LaunchMarker)
	if err != nil {
		fmt.Fprintf(stderr, "supervise-probe: %v\n", err)
		return exitRuntimeError
	}
	defer st.close()
	if st.delay > 0 {
		time.Sleep(st.delay)
	}
	tools := openPlatformMCP(manifestPath(), stderr)
	defer tools.close()
	logf := func(format string, args ...any) { fmt.Fprintf(stderr, "supervise-probe: "+format+"\n", args...) }
	if err := runtime.Run(&handler{st: st, tools: tools}, runtime.WithLogger(logf)); err != nil {
		fmt.Fprintf(stderr, "supervise-probe: %v\n", err)
		if runtime.ErrIsProtocol(err) {
			return exitProtocolError
		}
		return exitRuntimeError
	}
	return exitOK
}

// handler answers each session message's command.
type handler struct {
	st *state
	// tools is the platform MCP connection the process opened at start.
	tools *platformMCPConn
}

// OnCreate accepts every session.
func (h *handler) OnCreate(context.Context, runtime.CreateRequest) error { return nil }

// OnTerminate has nothing to release.
func (h *handler) OnTerminate(context.Context, string, runtime.TerminationReason) error { return nil }

// OnMessage runs the command the message's first text part names and
// answers with its JSON result. A message that names no command is echoed.
func (h *handler) OnMessage(ctx context.Context, msg runtime.Message) (runtime.Reply, error) {
	text := firstText(msg)
	cmd, arg, _ := strings.Cut(strings.TrimSpace(text), " ")
	result, ok := h.run(ctx, msg.SessionID, cmd, arg)
	if !ok {
		return runtime.TextReply(text), nil
	}
	body, err := json.Marshal(result)
	if err != nil {
		return runtime.Reply{}, fmt.Errorf("encode %s result: %w", cmd, err)
	}
	return runtime.TextReply(string(body)), nil
}

// run dispatches one command; ok is false for a message that names none.
func (h *handler) run(ctx context.Context, sessionID, cmd, arg string) (any, bool) {
	switch cmd {
	case CmdReport:
		return h.st.record(), true
	case CmdDaemon:
		return startDaemon(), true
	case CmdIPC:
		return createIPC(), true
	case CmdSymlink:
		return h.st.plantLink(sessionID), true
	case CmdMCP:
		return callPlatformMCP(ctx, h.tools), true
	case CmdResidue:
		return checkResidue(arg), true
	default:
		return nil, false
	}
}

// firstText is the inline text of the message's first text part.
func firstText(msg runtime.Message) string {
	if msg.Envelope == nil {
		return ""
	}
	for _, p := range msg.Envelope.Input {
		if p.Type == "text" {
			return p.Inline
		}
	}
	return ""
}

// platformMCPConn is the process's platform MCP connection, opened in the
// background from the endpoint the manifest named at start: the connection,
// or the reason there is none, once done is closed.
type platformMCPConn struct {
	done chan struct{}
	conn *platformMCP
	err  error
}

// wait blocks until the dial ends or ctx ends.
func (c *platformMCPConn) wait(ctx context.Context) error {
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("platform MCP dial still in progress: %w", ctx.Err())
	}
}

// close releases the connection once the dial has ended.
func (c *platformMCPConn) close() {
	<-c.done
	c.conn.close()
}

// openPlatformMCP reads the platform MCP endpoint from the manifest at path
// and dials it in the background, so the CH-MSGSOCK dial that follows is not
// held behind it, as the runtime SDK dials CH-MSGSOCK before its MCP
// sockets. A failed dial or handshake does not end the process: a process
// launched after the pod's intra-pod MCP servers were bound reads a manifest
// nonce those servers do not validate (§15.4.3 Authentication: a later
// manifest write does not re-arm a running server), and the probe still
// serves its other commands. The failure is logged to stderr and reported by
// the platform MCP command. spec: §15.4.3 (Authentication).
func openPlatformMCP(path string, stderr io.Writer) *platformMCPConn {
	c := &platformMCPConn{done: make(chan struct{})}
	socket, nonce, err := readPlatformMCPEndpoint(path)
	if errors.Is(err, errNoPlatformMCP) || errors.Is(err, fs.ErrNotExist) {
		c.err = errNoPlatformMCP
		close(c.done)
		return c
	}
	if err != nil {
		c.err = err
		close(c.done)
		return c
	}
	go func() {
		defer close(c.done)
		c.conn, c.err = dialPlatformMCP(socket, nonce)
		if c.err != nil {
			fmt.Fprintf(stderr, "supervise-probe: %v\n", c.err)
		}
	}()
	return c
}

// callPlatformMCP makes one platform MCP call over the connection the
// process opened with the manifest nonce of its start. A relaunched process
// holds its own connection, so the call proves the process authenticated
// with the manifest it read. spec: §15.4.3 (Authentication).
func callPlatformMCP(ctx context.Context, tools *platformMCPConn) ActionResult {
	if tools == nil {
		return ActionResult{Error: "no platform MCP connection"}
	}
	if err := tools.wait(ctx); err != nil {
		return ActionResult{Error: err.Error()}
	}
	if tools.conn == nil {
		return ActionResult{Error: fmt.Sprintf("no platform MCP connection: %v", tools.err)}
	}
	if err := tools.conn.Call("lenny/get_task_tree"); err != nil {
		return ActionResult{Error: err.Error()}
	}
	return ActionResult{OK: true}
}

// environment is the process environment, sorted as the kernel passed it.
func environment() []string {
	return append([]string(nil), os.Environ()...)
}
