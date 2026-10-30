// SPDX-License-Identifier: MIT

// Package runtimenonce holds the test side of the runtime connection
// handshake on CH-MSGSOCK and CH-RUNTIMEOPS. The adapter's listeners compare
// each accepted connection's first line, {"_lennyNonce":"<hex>"}, with the
// mcpNonce of the manifest published at that moment, so a test that dials
// either listener publishes a manifest and writes its nonce line first, and
// a fake adapter that accepts either socket reads and checks that line
// before its first frame.
//
// The package imports no adapter package, so the adapter's own in-package
// tests can use it without an import cycle.
//
// spec: §4.7.11 (Runtime connection handshake), §4.7.6 (mcpNonce row).
package runtimenonce

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ManifestFilename is the adapter manifest's file name inside its directory.
const ManifestFilename = "adapter-manifest.json"

// lineTimeout bounds a fake's wait for the runtime's nonce line. It is
// wider than the adapter's 500 ms so a loaded test host does not fail a
// fake whose subject is not the timing.
const lineTimeout = 5 * time.Second

// Manifest is a published test manifest.
type Manifest struct {
	// Dir is the directory the manifest is published in, which a test
	// passes as a Server's ManifestDir or to the adapter's
	// PublishedManifestNonce.
	Dir string
	// Path is the manifest file, which a test passes in
	// LENNY_ADAPTER_MANIFEST.
	Path string
	// Nonce is the published mcpNonce.
	Nonce string
}

// Provider returns a nonce provider that reads the manifest's mcpNonce from
// the file each time it is called, as the adapter's provider does.
func (m Manifest) Provider() func() string {
	return func() string {
		n, err := ReadNonce(m.Path)
		if err != nil {
			return ""
		}
		return n
	}
}

// NewNonce returns a random 256-bit nonce, lowercase hex.
func NewNonce(t testing.TB) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("generate nonce: %v", err)
	}
	return hex.EncodeToString(b)
}

// Publish writes a version-1 manifest carrying a fresh mcpNonce, merged with
// extra, into a new temporary directory.
func Publish(t testing.TB, extra map[string]any) Manifest {
	t.Helper()
	return PublishIn(t, t.TempDir(), extra)
}

// PublishIn writes a version-1 manifest carrying a fresh mcpNonce, merged
// with extra, into dir.
func PublishIn(t testing.TB, dir string, extra map[string]any) Manifest {
	t.Helper()
	m := Manifest{Dir: dir, Path: filepath.Join(dir, ManifestFilename), Nonce: NewNonce(t)}
	Rewrite(t, m.Path, m.Nonce, extra)
	return m
}

// Rewrite replaces the manifest at path with one carrying nonce, merged with
// extra.
func Rewrite(t testing.TB, path, nonce string, extra map[string]any) {
	t.Helper()
	doc := map[string]any{"version": 1, "mcpNonce": nonce}
	for k, v := range extra {
		doc[k] = v
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("publish manifest: %v", err)
	}
}

// ReadNonce returns the mcpNonce of the manifest at path.
func ReadNonce(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var m struct {
		MCPNonce string `json:"mcpNonce"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return "", err
	}
	return m.MCPNonce, nil
}

// Line returns the newline-terminated nonce line for nonce.
func Line(nonce string) []byte {
	b, _ := json.Marshal(map[string]string{"_lennyNonce": nonce})
	return append(b, '\n')
}

// Write writes the nonce line for nonce to w.
func Write(w io.Writer, nonce string) error {
	if _, err := w.Write(Line(nonce)); err != nil {
		return fmt.Errorf("write nonce line: %w", err)
	}
	return nil
}

// Check reads the first line from r, within lineTimeout on conn, and returns
// an error unless it is the nonce line carrying want. A fake adapter calls it
// on each accepted connection before its first frame, keeping r for the
// frames that follow.
func Check(conn net.Conn, r *bufio.Reader, want string) error {
	_ = conn.SetReadDeadline(time.Now().Add(lineTimeout))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	line, err := r.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read nonce line: %w", err)
	}
	if !bytes.Equal(bytes.TrimSpace(line), bytes.TrimSpace(Line(want))) {
		return fmt.Errorf("first line %q is not the nonce line for the published mcpNonce", bytes.TrimSpace(line))
	}
	return nil
}
