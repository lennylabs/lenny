// SPDX-License-Identifier: MIT

package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileBackendRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := Open("file://" + filepath.Join(dir, "reports"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	body := bytes.NewBufferString(`{"hello":"world"}`)
	url, err := st.Put(context.Background(), "runs/r1/report.html", body, "application/json")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !strings.HasPrefix(url, "file://") {
		t.Errorf("url=%q want file:// prefix", url)
	}

	rc, err := st.Get(context.Background(), "runs/r1/report.html")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != `{"hello":"world"}` {
		t.Errorf("Get body=%q want %q", got, `{"hello":"world"}`)
	}
}

func TestFileBackendGetMissingReturnsNotFound(t *testing.T) {
	st, err := Open("file://" + t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.Get(context.Background(), "absent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err=%v want ErrNotFound", err)
	}
}

func TestNoopBackendKeepsDevPathWorking(t *testing.T) {
	st, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	url, err := st.Put(context.Background(), "x", strings.NewReader("y"), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "objectstore://disabled/") {
		t.Errorf("noop URL=%q", url)
	}
}

func TestOpenUnknownSchemeFails(t *testing.T) {
	_, err := Open("ftp://nope")
	if err == nil {
		t.Error("Open ftp:// returned nil error; expected scheme error")
	}
}
