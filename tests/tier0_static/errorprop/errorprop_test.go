// SPDX-License-Identifier: MIT

package errorprop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScanDetectsDroppedClose constructs a tiny synthetic source
// tree and asserts the scanner flags the dropped Close() error.
func TestScanDetectsDroppedClose(t *testing.T) {
	dir := t.TempDir()
	src := `package x

import "io"

func leak(c io.Closer) {
	if err := c.Close(); err != nil {
		// silently dropped — this should fire
	}
}
`
	mustWrite(t, filepath.Join(dir, "x.go"), src)
	findings, err := Scan(dir, []string{"."})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings=%d want 1: %+v", len(findings), findings)
	}
	if findings[0].Verb != "Close" {
		t.Errorf("verb=%q want Close", findings[0].Verb)
	}
}

// TestScanAllowsPropagatedClose confirms the scanner does not flag
// the case where the Close error is returned to the caller.
func TestScanAllowsPropagatedClose(t *testing.T) {
	dir := t.TempDir()
	src := `package x

import "io"

func ok(c io.Closer) error {
	if err := c.Close(); err != nil {
		return err
	}
	return nil
}
`
	mustWrite(t, filepath.Join(dir, "x.go"), src)
	findings, err := Scan(dir, []string{"."})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings; got: %+v", findings)
	}
}

// TestScanAllowsLoggedClose confirms the scanner does not flag the
// case where the Close error is logged.
func TestScanAllowsLoggedClose(t *testing.T) {
	dir := t.TempDir()
	src := `package x

import (
	"io"
	"log"
)

func ok(c io.Closer) {
	if err := c.Close(); err != nil {
		log.Printf("close failed: %v", err)
	}
}
`
	mustWrite(t, filepath.Join(dir, "x.go"), src)
	findings, err := Scan(dir, []string{"."})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings; got: %+v", findings)
	}
}

// TestScanCoversMultipleVerbs confirms the verb list (Close, Cleanup,
// Release, Drain, Stop, Flush) is enforced.
func TestScanCoversMultipleVerbs(t *testing.T) {
	dir := t.TempDir()
	src := `package x

type res struct{}

func (r *res) Close() error   { return nil }
func (r *res) Cleanup() error { return nil }
func (r *res) Release() error { return nil }
func (r *res) Drain() error   { return nil }
func (r *res) Stop() error    { return nil }
func (r *res) Flush() error   { return nil }

func leakAll(r *res) {
	if err := r.Close(); err != nil {}
	if err := r.Cleanup(); err != nil {}
	if err := r.Release(); err != nil {}
	if err := r.Drain(); err != nil {}
	if err := r.Stop(); err != nil {}
	if err := r.Flush(); err != nil {}
}
`
	mustWrite(t, filepath.Join(dir, "x.go"), src)
	findings, err := Scan(dir, []string{"."})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 6 {
		t.Errorf("findings=%d want 6", len(findings))
		for _, f := range findings {
			t.Logf("  - %s", f)
		}
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if !strings.HasSuffix(path, ".go") {
		t.Fatalf("path must end with .go: %s", path)
	}
}
