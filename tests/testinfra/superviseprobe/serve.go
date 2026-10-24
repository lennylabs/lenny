// SPDX-License-Identifier: MIT

package superviseprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lennylabs/lenny/sdks/runtime/go/runtime"
)

// serve records the process identity, sleeps the dial delay when this is
// the pod's warm-up launch (claimDialDelay), and serves sessions through
// the runtime SDK at the Standard level until the adapter closes
// CH-MSGSOCK. A later launch in the pod dials without the delay. The
// manifest is read before the dial, so the report shows which manifest the
// process saw at start. spec: §4.7.10 (Runtime supervisor, Launch;
// SDK-warm); §4.7.9 step 7.
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
	logf := func(format string, args ...any) { fmt.Fprintf(stderr, "supervise-probe: "+format+"\n", args...) }
	if err := runtime.Run(&handler{st: st}, runtime.WithStandardLevel(), runtime.WithLogger(logf)); err != nil {
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
		return callPlatformMCP(ctx), true
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

// callPlatformMCP makes one platform MCP call over the connection the SDK
// opened with the manifest nonce of this process's start. A relaunched
// process holds its own connection, so the call proves the process
// authenticated with the manifest it read. spec: §15.4.3 (Authentication).
func callPlatformMCP(ctx context.Context) ActionResult {
	tools := runtime.ToolsFrom(ctx)
	if tools == nil {
		return ActionResult{Error: "no platform MCP connection"}
	}
	if _, err := tools.Call("lenny/get_task_tree", map[string]any{}); err != nil {
		return ActionResult{Error: err.Error()}
	}
	return ActionResult{OK: true}
}

// environment is the process environment, sorted as the kernel passed it.
func environment() []string {
	return append([]string(nil), os.Environ()...)
}
