// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the pod-global adapter manifest under
// concurrent writers.
//
// The manifest is one file per pod and every session on the pod writes it,
// so two starts admitted at once write it at the same time. The runtime
// reads it at startup, and on a concurrent pod one session's read can land
// while a co-tenant's write is in flight. A truncate-and-write in place
// interleaves the two documents and hands the reader bytes that decode as
// neither session's manifest, which takes the §15.4.3 nonce and the socket
// paths with it. The write is published by rename so a reader sees the
// whole of one session's manifest or the whole of the other's.
//
// Which of the two the reader sees is unordered and is not asserted. That
// collision is the recorded limit of one pod-global manifest file.
//
// spec: §15.4 (adapter manifest), §5.2 (every session is bound to a slot
// on every pod), §15.4.3 (the nonce the manifest carries).
package tier7a_load_local_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// spec: 15.4 (adapter manifest), 5.2 (concurrent sessions on one pod),
// 15.4.3 (intra-pod MCP nonce)
// diagnosis: a reader observed a manifest that is neither writer's
// document. Two sessions starting at once wrote the pod's one manifest
// file in place, so the runtime that read it during the overlap got a
// truncated or spliced document: it fails to parse the file, or parses one
// carrying one session's identifier beside another's nonce, and its
// §15.4.3 handshake against the pod's MCP sockets fails.
func TestConcurrentManifestWritesArePublishedWhole_spec_15_4(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, adapter.ManifestFilename)

	var stop atomic.Bool
	var reads, decoded int
	var writers, reader sync.WaitGroup

	reader.Add(1)
	go func() {
		defer reader.Done()
		for !stop.Load() {
			b, err := os.ReadFile(path)
			if err != nil || len(b) == 0 {
				continue
			}
			reads++
			var m adapter.Manifest
			if err := json.Unmarshal(b, &m); err != nil {
				t.Errorf("a reader observed a manifest that decodes as no session's document: %v", err)
				return
			}
			// The two writers differ in every field a runtime acts on, so a
			// spliced document that happens to parse is caught here.
			if m.SessionID == "alice" && m.MCPNonce != "nonce-alice" {
				t.Errorf("manifest pairs sessionId alice with nonce %q", m.MCPNonce)
				return
			}
			if m.SessionID == "bob" && m.MCPNonce != "nonce-bob" {
				t.Errorf("manifest pairs sessionId bob with nonce %q", m.MCPNonce)
				return
			}
			decoded++
		}
	}()

	for _, sessionID := range []string{"alice", "bob"} {
		writers.Add(1)
		go func() {
			defer writers.Done()
			m := adapter.Manifest{
				Version:   1,
				SessionID: sessionID,
				TaskID:    sessionID,
				MCPNonce:  "nonce-" + sessionID,
				// A padded tool description makes each document large
				// enough that an interleaved write is observable rather
				// than landing in one atomic page-sized syscall.
				AdapterLocalTools: []adapter.ManifestTool{{
					Name:        "lenny/" + sessionID,
					Description: string(make([]byte, 1<<15)),
					InputSchema: json.RawMessage(`{}`),
				}},
			}
			for range 60 {
				if err := adapter.WriteManifest(dir, m); err != nil {
					t.Errorf("write manifest for %s: %v", sessionID, err)
					return
				}
			}
		}()
	}

	writers.Wait()
	stop.Store(true)
	reader.Wait()

	if reads == 0 {
		t.Fatal("the reader never observed the manifest; the case exercised no overlap")
	}
	if decoded == 0 {
		t.Fatal("no read decoded a whole manifest")
	}
}
