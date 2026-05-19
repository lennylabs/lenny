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
		t.Errorf("schemaVersion = %d, want 1 (§15.4.1 producer obligation)", resp.Output[0].SchemaVersion)
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
