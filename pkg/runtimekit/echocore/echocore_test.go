// SPDX-License-Identifier: MIT

package echocore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/runtimekit/echocore"
)

// runEcho drives echocore.Run over the given input and returns the
// newline-delimited output frames it produced.
func runEcho(t *testing.T, input string) []string {
	t.Helper()
	var out bytes.Buffer
	err := echocore.Run(context.Background(), strings.NewReader(input), &out, io.Discard)
	if err != nil {
		t.Fatalf("echocore.Run: %v", err)
	}
	var frames []string
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if line != "" {
			frames = append(frames, line)
		}
	}
	return frames
}

func TestEchoesAMessageAsAResponse(t *testing.T) {
	in := `{"type":"message","id":"m1","input":[{"type":"text","inline":"hi"}]}` + "\n"
	frames := runEcho(t, in)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1: %v", len(frames), frames)
	}
	var resp struct {
		Type   string `json:"type"`
		Output []struct {
			SchemaVersion int    `json:"schemaVersion"`
			Type          string `json:"type"`
			Inline        string `json:"inline"`
		} `json:"output"`
	}
	if err := json.Unmarshal([]byte(frames[0]), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Type != "response" {
		t.Errorf("frame type = %q, want response", resp.Type)
	}
	if len(resp.Output) != 1 || !strings.Contains(resp.Output[0].Inline, "hi") {
		t.Errorf("output = %+v, want the echoed input", resp.Output)
	}
	if resp.Output[0].SchemaVersion != 1 {
		t.Errorf("schemaVersion = %d, want 1 (§28.5.3 producer obligation)", resp.Output[0].SchemaVersion)
	}
	if !strings.Contains(resp.Output[0].Inline, "[echo seq=1]") {
		t.Errorf("text part %q must carry the sequence prefix", resp.Output[0].Inline)
	}
}

func TestAnswersHeartbeatWithAck(t *testing.T) {
	frames := runEcho(t, `{"type":"heartbeat"}`+"\n")
	if len(frames) != 1 || !strings.Contains(frames[0], "heartbeat_ack") {
		t.Errorf("heartbeat must be answered with heartbeat_ack, got %v", frames)
	}
}

func TestShutdownExitsCleanly(t *testing.T) {
	// A shutdown frame followed by more input: the loop must stop at
	// shutdown and not echo the trailing message.
	in := `{"type":"shutdown","deadline_ms":1}` + "\n" + `{"type":"message","input":[]}` + "\n"
	frames := runEcho(t, in)
	if len(frames) != 0 {
		t.Errorf("shutdown must end the loop, got trailing frames %v", frames)
	}
}

func TestUnknownTypeIsIgnored(t *testing.T) {
	in := `{"type":"future_frame"}` + "\n" + `{"type":"heartbeat"}` + "\n"
	frames := runEcho(t, in)
	if len(frames) != 1 || !strings.Contains(frames[0], "heartbeat_ack") {
		t.Errorf("an unknown type must be skipped, got %v", frames)
	}
}

func TestMalformedFrameIsAProtocolError(t *testing.T) {
	var out bytes.Buffer
	err := echocore.Run(context.Background(), strings.NewReader("not json\n"), &out, io.Discard)
	if err == nil {
		t.Fatal("malformed input must be a protocol error")
	}
	var pe echocore.ProtocolError
	if !errors.As(err, &pe) {
		t.Errorf("error %v must be a ProtocolError so the entrypoint can set exit code 2", err)
	}
}

func TestEmptyInputExitsCleanly(t *testing.T) {
	var out bytes.Buffer
	if err := echocore.Run(context.Background(), strings.NewReader(""), &out, io.Discard); err != nil {
		t.Errorf("EOF on empty input must be a clean exit, got %v", err)
	}
}

// spec: 28.5.3 (Intra-pod), 28.5.1 (Gateway-to-pod)
// The silence directive is echoed like any message, and every later
// heartbeat goes unanswered, so the adapter's ack deadline elapses as for a
// runtime that hangs between turns. A heartbeat before the directive is still
// acked.
func TestHeartbeatSilenceDirectiveStopsTheAcks(t *testing.T) {
	directive := `{"type":"message","sessionId":"s1","input":[{"type":"text","inline":"` +
		echocore.HeartbeatSilenceDirective + `"}]}`
	in := `{"type":"heartbeat"}` + "\n" + directive + "\n" + `{"type":"heartbeat"}` + "\n" +
		`{"type":"message","sessionId":"s1","input":[{"type":"text","inline":"after"}]}` + "\n" +
		`{"type":"heartbeat"}` + "\n"
	frames := runEcho(t, in)
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want the first ack and two echoes: %v", len(frames), frames)
	}
	if !strings.Contains(frames[0], "heartbeat_ack") {
		t.Errorf("frame 0 = %s, want the ack for the heartbeat before the directive", frames[0])
	}
	if !strings.Contains(frames[1], echocore.HeartbeatSilenceDirective) || !strings.Contains(frames[2], "after") {
		t.Errorf("frames %v, want the directive and the later message echoed", frames[1:])
	}
}

// spec: 28.5.3 (Intra-pod)
// A message whose text is not exactly the directive, or a frame that is not a
// message, is not the directive.
func TestIsHeartbeatSilenceDirective(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{`{"type":"message","input":[{"type":"text","inline":"` + echocore.HeartbeatSilenceDirective + `"}]}`, true},
		{`{"type":"message","input":[{"type":"text","inline":"hi"}]}`, false},
		{`{"type":"message","input":[]}`, false},
		{`{"type":"heartbeat"}`, false},
		{`not json`, false},
	} {
		if got := echocore.IsHeartbeatSilenceDirective([]byte(tc.line)); got != tc.want {
			t.Errorf("IsHeartbeatSilenceDirective(%s) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// spec: 28.5.3 (Intra-pod, Inbound: session_end)
// A session_end ends nothing and writes no frame; its arrival is logged with
// the session it names.
func TestSessionEndIsLoggedWithItsSession(t *testing.T) {
	var out, stderr bytes.Buffer
	in := `{"type":"session_end","sessionId":"sess-42"}` + "\n" + `{"type":"heartbeat"}` + "\n"
	if err := echocore.Run(context.Background(), strings.NewReader(in), &out, &stderr); err != nil {
		t.Fatalf("echocore.Run: %v", err)
	}
	if !strings.Contains(stderr.String(), "session_end for session sess-42") {
		t.Errorf("stderr = %q, want the session_end logged with its session", stderr.String())
	}
	if !strings.Contains(out.String(), "heartbeat_ack") || strings.Count(out.String(), "\n") != 1 {
		t.Errorf("output = %q, want only the heartbeat ack", out.String())
	}
}
