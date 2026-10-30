// SPDX-License-Identifier: MIT

// Tests for the reference type: mcp runtime. They drive run() in-
// process — feeding newline-delimited JSON-RPC on stdin and reading the
// JSON-RPC stdout — without spawning a subprocess. A type: mcp runtime
// is a plain MCP server, so the tests speak standard MCP directly.
package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// runtimePipes starts run() over in-memory pipes and returns a writer
// for stdin and a reader for stdout. The runtime exits when stdin is
// closed.
func runtimePipes(t *testing.T) (stdin *io.PipeWriter, stdout *bufio.Reader, done chan error) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var stderr strings.Builder
	done = make(chan error, 1)
	go func() {
		err := run(inR, outW, &stderr)
		_ = outW.CloseWithError(io.EOF)
		done <- err
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		<-done
	})
	return inW, bufio.NewReader(outR), done
}

// send writes one JSON-RPC request line to the runtime's stdin.
func send(t *testing.T, w io.Writer, id any, method string, params any) {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		req["id"] = id
	}
	if params != nil {
		req["params"] = params
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal %s: %v", method, err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		t.Fatalf("write %s: %v", method, err)
	}
}

// readResp reads one JSON-RPC response line.
func readResp(t *testing.T, r *bufio.Reader) map[string]json.RawMessage {
	t.Helper()
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		t.Fatalf("read response: %v", err)
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode response %q: %v", line, err)
	}
	return resp
}

func TestInitializeHandshake(t *testing.T) {
	stdin, stdout, _ := runtimePipes(t)
	send(t, stdin, 1, "initialize", map[string]any{"protocolVersion": protocolVersion})
	resp := readResp(t, stdout)
	if _, isErr := resp["error"]; isErr {
		t.Fatalf("initialize returned an error: %s", resp["error"])
	}
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
		Capabilities struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if result.ProtocolVersion != protocolVersion {
		t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, protocolVersion)
	}
	if result.ServerInfo.Name != serverName {
		t.Errorf("serverInfo.name = %q, want %q", result.ServerInfo.Name, serverName)
	}
	if result.Capabilities.Tools == nil {
		t.Error("initialize result does not advertise the tools capability")
	}
}

func TestToolsList(t *testing.T) {
	stdin, stdout, _ := runtimePipes(t)
	send(t, stdin, 1, "initialize", map[string]any{"protocolVersion": protocolVersion})
	readResp(t, stdout)

	send(t, stdin, 2, "tools/list", nil)
	resp := readResp(t, stdout)
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "echo" {
		t.Errorf("tools/list = %+v, want [echo]", result.Tools)
	}
}

func TestToolsCallEcho(t *testing.T) {
	stdin, stdout, _ := runtimePipes(t)
	send(t, stdin, 1, "initialize", map[string]any{"protocolVersion": protocolVersion})
	readResp(t, stdout)

	send(t, stdin, 2, "tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"text": "hello"},
	})
	resp := readResp(t, stdout)
	if _, isErr := resp["error"]; isErr {
		t.Fatalf("tools/call returned an error: %s", resp["error"])
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("decode tools/call result: %v", err)
	}
	if result.IsError {
		t.Error("tools/call result has isError = true")
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("tools/call content = %+v, want a single text block", result.Content)
	}
	if !strings.Contains(result.Content[0].Text, "hello") {
		t.Errorf("echoed text = %q, want it to contain \"hello\"", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "seq=1") {
		t.Errorf("echoed text = %q, want it to carry the call sequence number", result.Content[0].Text)
	}
}

func TestToolsCallSequenceIncrements(t *testing.T) {
	stdin, stdout, _ := runtimePipes(t)
	send(t, stdin, 1, "initialize", map[string]any{"protocolVersion": protocolVersion})
	readResp(t, stdout)

	for i, want := range []string{"seq=1", "seq=2", "seq=3"} {
		send(t, stdin, i+2, "tools/call", map[string]any{
			"name":      "echo",
			"arguments": map[string]any{"text": "x"},
		})
		resp := readResp(t, stdout)
		if !strings.Contains(string(resp["result"]), want) {
			t.Errorf("call %d result = %s, want it to contain %q", i+1, resp["result"], want)
		}
	}
}

func TestUnknownToolReturnsError(t *testing.T) {
	stdin, stdout, _ := runtimePipes(t)
	send(t, stdin, 1, "initialize", map[string]any{"protocolVersion": protocolVersion})
	readResp(t, stdout)

	send(t, stdin, 2, "tools/call", map[string]any{"name": "nope"})
	resp := readResp(t, stdout)
	if _, isErr := resp["error"]; !isErr {
		t.Error("tools/call for an unknown tool did not return a JSON-RPC error")
	}
}

func TestUnknownMethodReturnsError(t *testing.T) {
	stdin, stdout, _ := runtimePipes(t)
	send(t, stdin, 1, "frobnicate", nil)
	resp := readResp(t, stdout)
	if _, isErr := resp["error"]; !isErr {
		t.Error("an unknown method did not return a JSON-RPC error")
	}
}

func TestNotificationDrawsNoReply(t *testing.T) {
	stdin, stdout, _ := runtimePipes(t)
	send(t, stdin, 1, "initialize", map[string]any{"protocolVersion": protocolVersion})
	readResp(t, stdout)

	// notifications/initialized has no id and must draw no reply. If the
	// server wrongly replied, that reply would be read in place of the
	// tools/list response below.
	send(t, stdin, nil, "notifications/initialized", nil)
	send(t, stdin, 2, "tools/list", nil)
	resp := readResp(t, stdout)
	var id int
	if err := json.Unmarshal(resp["id"], &id); err != nil || id != 2 {
		t.Errorf("response id = %d (err %v), want 2 — the notification may have drawn a reply", id, err)
	}
}

func TestStdinEOFExitsCleanly(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- run(inR, outW, &stderr)
		_ = outW.Close()
	}()
	// Drain stdout so the runtime's writes never block.
	go func() { _, _ = io.Copy(io.Discard, outR) }()

	_ = inW.Close() // EOF on stdin
	if err := <-done; err != nil {
		t.Errorf("run on stdin EOF returned %v, want nil (clean exit)", err)
	}
}

func TestMalformedJSONIsProtocolError(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- run(inR, outW, &stderr)
		_ = outW.Close()
	}()
	go func() { _, _ = io.Copy(io.Discard, outR) }()

	if _, err := inW.Write([]byte("not json\n")); err != nil {
		t.Fatalf("write malformed line: %v", err)
	}
	err := <-done
	var pe protocolError
	if err == nil {
		t.Fatal("run on malformed JSON returned nil, want a protocol error")
	}
	if !asProtocolError(err, &pe) {
		t.Errorf("run returned %v, want a protocolError", err)
	}
	_ = inW.Close()
}

// asProtocolError reports whether err is a protocolError.
func asProtocolError(err error, target *protocolError) bool {
	pe, ok := err.(protocolError)
	if ok {
		*target = pe
	}
	return ok
}
