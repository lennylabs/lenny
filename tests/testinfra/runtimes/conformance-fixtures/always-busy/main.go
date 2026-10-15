// SPDX-License-Identifier: MIT

// Conformance fixture: a runtime that reports busy on every heartbeat_ack.
// It answers each heartbeat with busy: true and the session the heartbeat
// names, or the most recently started session when the heartbeat names
// none, whether or not a message is being handled. It handles each message
// on its own goroutine and answers it after a short delay, so the harness's
// heartbeat is acknowledged while the message is outstanding.
//
// Reporting busy while idle keeps a session from being sealed but violates
// no wire rule, so the heartbeat_busy conformance check passes on this
// fixture. A heartbeat that arrives before any session_start gets a bare
// ack, because the schema refuses busy: true without a sessionId.
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

// responseDelay is how long the fixture takes to answer a message.
const responseDelay = 200 * time.Millisecond

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
	var lastSession string
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<16), 4<<20)
	for sc.Scan() {
		var f frame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		switch f.Type {
		case "session_start":
			lastSession = f.SessionID
		case "message":
			handlers.Add(1)
			go func(f frame) {
				defer handlers.Done()
				time.Sleep(responseDelay)
				o.write(map[string]any{"type": "response", "sessionId": f.SessionID, "output": f.Input})
			}(f)
		case "heartbeat":
			id := f.SessionID
			if id == "" {
				id = lastSession
			}
			if id == "" {
				o.write(map[string]any{"type": "heartbeat_ack"})
				continue
			}
			o.write(map[string]any{"type": "heartbeat_ack", "sessionId": id, "busy": true})
		case "shutdown":
			handlers.Wait()
			return
		}
	}
	handlers.Wait()
}
