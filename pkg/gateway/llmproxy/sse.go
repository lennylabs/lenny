// SPDX-License-Identifier: MIT

package llmproxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
)

// sseDataPrefix is the field prefix of an SSE data line.
var sseDataPrefix = []byte("data:")

// RelayStream copies an upstream Server-Sent Events response to dst,
// invoking flush after each line so the agent pod receives the model's
// tokens incrementally rather than after the whole response (§4.9). It
// does not buffer the full stream. While relaying, it parses the
// Anthropic Messages stream events for the authoritative token usage:
// the input count from the message_start event and the output count
// from the terminal message_delta event.
//
// A clean upstream EOF returns the accumulated usage and a nil error. A
// read failure from src or a write failure to dst after relaying has
// begun returns a TranslationError tagged streaming_interrupted, the
// §4.9 error type for a mid-stream failure.
func RelayStream(dst io.Writer, src io.Reader, flush func()) (Usage, error) {
	var usage Usage
	br := bufio.NewReader(src)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if _, werr := dst.Write(line); werr != nil {
				return usage, &TranslationError{
					Type:    ErrStreamingInterrupted,
					Message: "write stream to client: " + werr.Error(),
				}
			}
			if flush != nil {
				flush()
			}
			applySSEUsage(line, &usage)
		}
		if err == io.EOF {
			return usage, nil
		}
		if err != nil {
			return usage, &TranslationError{
				Type:    ErrStreamingInterrupted,
				Message: "read upstream stream: " + err.Error(),
			}
		}
	}
}

// sseEvent is the subset of an Anthropic Messages stream event carrying
// token usage. message_start nests usage under message; message_delta
// carries usage at the top level.
type sseEvent struct {
	Type    string `json:"type"`
	Message struct {
		Usage *sseUsage `json:"usage"`
	} `json:"message"`
	Usage *sseUsage `json:"usage"`
}

type sseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// applySSEUsage parses an SSE data line and folds any token usage it
// carries into usage. A non-data line, a comment, or a data payload
// that is not a usage-bearing event leaves usage unchanged. The input
// count is taken from message_start; the output count from the
// cumulative message_delta, so the last message_delta wins.
func applySSEUsage(line []byte, usage *Usage) {
	if !bytes.HasPrefix(line, sseDataPrefix) {
		return
	}
	payload := bytes.TrimSpace(line[len(sseDataPrefix):])
	if len(payload) == 0 {
		return
	}
	var ev sseEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return
	}
	switch ev.Type {
	case "message_start":
		if ev.Message.Usage != nil {
			usage.InputTokens = ev.Message.Usage.InputTokens
		}
	case "message_delta":
		if ev.Usage != nil {
			usage.OutputTokens = ev.Usage.OutputTokens
		}
	}
}
