// SPDX-License-Identifier: MIT

package loadctl

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedAssetsAvailable(t *testing.T) {
	_, ok := embeddedAssets()
	if !ok {
		t.Fatal("embeddedAssets returned ok=false; web/ tree missing from binary")
	}
}

func TestServerServesEmbeddedIndex(t *testing.T) {
	s, err := NewServer(Config{StorageURL: "file://" + t.TempDir()})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer s.Close()
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Lenny load-test control plane") {
		t.Errorf("index body missing title; got: %q", string(body)[:min(200, len(body))])
	}
	if !strings.Contains(string(body), "htmx.org") {
		t.Errorf("index body missing htmx reference (suggests inlined fallback was served, not embed)")
	}
}

func TestServerServesEmbeddedStylesheet(t *testing.T) {
	s, _ := NewServer(Config{StorageURL: "file://" + t.TempDir()})
	defer s.Close()
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/assets/style.css")
	if err != nil {
		t.Fatalf("GET style.css: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status=%d want 200", resp.StatusCode)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
