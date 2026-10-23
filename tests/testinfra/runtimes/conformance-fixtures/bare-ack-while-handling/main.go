// SPDX-License-Identifier: MIT

// Conformance fixture: a runtime that answers a heartbeat with a bare
// heartbeat_ack while it is handling a message. It handles each message on
// its own goroutine and answers it after a delay, and acknowledges every
// heartbeat at once with no sessionId and no busy field.
//
// The deliberate violation is the bare ack written while the message is
// outstanding: §28.5.3 requires such an ack to carry busy: true and the
// session's sessionId, so the adapter can tell a working session from an
// idle one. The heartbeat_busy conformance check fails on this fixture.
//
// spec: §15.4.6 (Conformance Test Suite), §28.5.3 (CH-MSGSOCK)
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// responseDelay is long enough that the harness's heartbeat, written right
// after the message, is acknowledged before the response.
const responseDelay = 300 * time.Millisecond

type frame struct {
	Type      string            `json:"type"`
	SessionID string            `json:"sessionId"`
	Input     []json.RawMessage `json:"input"`
}

// out serializes the fixture's stdout writes.
type out struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func (o *out) write(v any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_ = o.enc.Encode(v)
}

func main() {
	o := &out{enc: json.NewEncoder(os.Stdout)}
	var handlers sync.WaitGroup
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<16), 4<<20)
	for sc.Scan() {
		var f frame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		switch f.Type {
		case "message":
			handlers.Add(1)
			go func(f frame) {
				defer handlers.Done()
				time.Sleep(responseDelay)
				o.write(map[string]any{"type": "response", "sessionId": f.SessionID, "output": f.Input})
			}(f)
		case "heartbeat":
			o.write(map[string]any{"type": "heartbeat_ack"})
		case "shutdown":
			handlers.Wait()
			return
		}
	}
	handlers.Wait()
}
