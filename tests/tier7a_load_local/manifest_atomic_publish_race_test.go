// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the adapter manifest publish.
//
// The adapter manifest is one pod-global file, and every session bound to
// a slot on the pod writes it at its own start, so two starts admitted at
// once write it concurrently. The runtime reads the file when its binary
// starts and the document it reads has to be complete and authoritative,
// which a truncate-and-write in place does not give: a reader lands on a
// prefix of one document, or on one document's head followed by another's
// tail, and decodes a manifest that is neither session's. The write
// therefore publishes by rename.
//
// The case carries a stress budget:
//
//	lenny-test stress --test TestConcurrentManifestWritesPublishWholeDocuments_spec_4_7_5 --runs 50 --pkg ./tests/tier7a_load_local/... --tag load_local
//
// spec: §4.7.5 (the adapter manifest is one pod-global file, complete and
// authoritative when the runtime binary starts), §6.4 (every session is
// bound to a slot on every pod), §13.1 (manifest file mode).
package tier7a_load_local_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// manifestWriteRounds is how many times each of the two sessions rewrites
// the pod's manifest. A torn write occupies a window of microseconds, so
// the case needs many attempts to land a reader inside one.
const manifestWriteRounds = 300

// racingManifest builds one session's whole manifest. The two sessions'
// documents differ in length, so a truncating in-place write leaves the
// longer document's tail behind the shorter one's head.
func racingManifest(sessionID, nonce string, tools int) adapter.Manifest {
	m := adapter.Manifest{
		Version:   1,
		SessionID: sessionID,
		TaskID:    sessionID,
		MCPNonce:  nonce,
	}
	for i := range tools {
		m.AdapterLocalTools = append(m.AdapterLocalTools, adapter.ManifestTool{
			Name:        fmt.Sprintf("%s/tool_%02d", sessionID, i),
			Description: strings.Repeat("d", 64),
		})
	}
	return m
}

// spec: 4.7.5 (one pod-global manifest, complete and authoritative when
// the runtime binary starts), 6.4 (every session is bound to a slot on
// every pod), 13.1 (manifest file mode)
// diagnosis: two co-tenant starts writing the pod's one manifest at once
// left a document a runtime cannot use. A decode failure means a reader
// saw a prefix of a document being written in place. A sessionId paired
// with the co-tenant's mcpNonce means the reader saw one document's head
// spliced onto the other's tail, and the runtime that reads it presents a
// nonce the intra-pod MCP servers were never armed with. Either outcome
// means the manifest write no longer publishes atomically.
func TestConcurrentManifestWritesPublishWholeDocuments_spec_4_7_5(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, adapter.ManifestFilename)

	nonceOf := map[string]string{
		"alice": strings.Repeat("a", 2*adapter.MCPNonceBytes),
		"bob":   strings.Repeat("b", 2*adapter.MCPNonceBytes),
	}
	toolsOf := map[string]int{"alice": 2, "bob": 48}

	// The pod holds one manifest before either start runs, so the reader
	// never has to tolerate a missing file.
	if err := adapter.WriteManifest(dir, racingManifest("alice", nonceOf["alice"], toolsOf["alice"])); err != nil {
		t.Fatalf("seed the pod manifest: %v", err)
	}

	stop := make(chan struct{})
	readerDone := make(chan struct{})
	var stopOnce sync.Once
	stopReader := func() {
		stopOnce.Do(func() { close(stop) })
		<-readerDone
	}
	t.Cleanup(stopReader)

	var (
		mu         sync.Mutex
		decodeErrs int
		mispairs   []string
	)
	go func() {
		defer close(readerDone)
		// The reader is bounded by the writers' stop and by a deadline, so
		// it cannot outlive the case on any exit path.
		deadline := time.After(60 * time.Second)
		for {
			select {
			case <-stop:
				return
			case <-deadline:
				return
			default:
			}
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var m adapter.Manifest
			if err := json.Unmarshal(b, &m); err != nil {
				mu.Lock()
				decodeErrs++
				mu.Unlock()
				continue
			}
			if want, ok := nonceOf[m.SessionID]; !ok || m.MCPNonce != want {
				mu.Lock()
				mispairs = append(mispairs,
					fmt.Sprintf("sessionId %q read beside mcpNonce %q", m.SessionID, m.MCPNonce))
				mu.Unlock()
			}
			runtime.Gosched()
		}
	}()

	var wg sync.WaitGroup
	for _, sessionID := range []string{"alice", "bob"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := racingManifest(sessionID, nonceOf[sessionID], toolsOf[sessionID])
			for range manifestWriteRounds {
				if err := adapter.WriteManifest(dir, m); err != nil {
					t.Errorf("write %s's manifest: %v", sessionID, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	stopReader()

	mu.Lock()
	defer mu.Unlock()
	if decodeErrs > 0 {
		t.Errorf("a reader decoded a partial manifest %d times; the pod-global write does not publish whole documents", decodeErrs)
	}
	if len(mispairs) > 0 {
		t.Errorf("a reader saw a torn manifest %d times, first: %s", len(mispairs), mispairs[0])
	}

	// The published file still carries the §13.1 boundary the runtime
	// reads it through, and nothing but the manifest is left in the
	// directory: a failed publish must not accumulate temporary files.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat the published manifest: %v", err)
	}
	if got := info.Mode().Perm(); got != adapter.ManifestFileMode {
		t.Errorf("published manifest mode = %#o, want %#o", got, adapter.ManifestFileMode)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the manifest directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != adapter.ManifestFilename {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("manifest directory holds %v, want only %s", names, adapter.ManifestFilename)
	}
}
