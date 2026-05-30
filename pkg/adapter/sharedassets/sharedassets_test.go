// SPDX-License-Identifier: MIT

package sharedassets

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// spec: §6.4 line 409 — F-6.4.3. Encode/Decode round-trips the inline
// shared-asset set so the controller and the adapter agree on the wire
// form carried on the --shared-assets flag.
func TestEncodeDecodeRoundTrip_spec_6_4(t *testing.T) {
	in := []FileSpec{
		{Path: "config/app.yaml", Content: "key: value\n", Mode: "0444"},
		{Path: "lib/shared.txt", Content: "shared"},
	}
	enc, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if enc == "" {
		t.Fatal("Encode of a non-empty set returned the empty string")
	}
	out, err := Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("round-trip length = %d, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("round-trip[%d] = %+v, want %+v", i, out[i], in[i])
		}
	}
}

// spec: §6.4 line 409 — an empty set encodes to the empty string so the
// controller can omit the --shared-assets flag entirely, and the empty
// string decodes back to nil so an adapter started without the flag
// populates nothing.
func TestEncodeEmptyIsEmptyString_spec_6_4(t *testing.T) {
	enc, err := Encode(nil)
	if err != nil {
		t.Fatalf("Encode(nil): %v", err)
	}
	if enc != "" {
		t.Errorf("Encode(nil) = %q, want empty string", enc)
	}
	out, err := Decode("")
	if err != nil {
		t.Fatalf("Decode(\"\"): %v", err)
	}
	if out != nil {
		t.Errorf("Decode(\"\") = %v, want nil", out)
	}
}

// spec: §15.5 forward-read — a corrupt --shared-assets value is a config
// error the adapter must surface at startup rather than silently ignore.
func TestDecodeRejectsCorruptInput_spec_6_4(t *testing.T) {
	if _, err := Decode("not-valid-base64!!!"); err == nil {
		t.Fatal("Decode accepted non-base64 input")
	}
	// Valid base64 that is not a JSON array.
	if _, err := Decode("bm90LWpzb24="); err == nil { // "not-json"
		t.Fatal("Decode accepted base64 that is not a JSON array")
	}
}

// spec: §6.4 line 409 — Materialize writes each inline asset read-only
// into the shared root, defaulting the mode to 0444 so the runtime can
// read but the file itself is immutable.
func TestMaterializeWritesReadOnlyFiles_spec_6_4(t *testing.T) {
	dir := t.TempDir()
	specs := []FileSpec{
		{Path: "config.yaml", Content: "k: v\n"},                  // default mode
		{Path: "nested/asset.txt", Content: "data", Mode: "0440"}, // explicit mode + subdir
		{Path: "empty.txt"}, // empty content
	}
	if err := Materialize(dir, specs); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("read config.yaml: %v", err)
	}
	if string(got) != "k: v\n" {
		t.Errorf("config.yaml = %q, want %q", got, "k: v\n")
	}
	assertMode(t, filepath.Join(dir, "config.yaml"), 0o444)
	assertMode(t, filepath.Join(dir, "nested/asset.txt"), 0o440)

	empty, err := os.ReadFile(filepath.Join(dir, "empty.txt"))
	if err != nil {
		t.Fatalf("read empty.txt: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty.txt = %q, want empty file", empty)
	}
}

// spec: §6.4 line 409 — the default mode is pinned exactly regardless of
// the process umask, so the asset is 0444 even under a restrictive umask.
func TestMaterializeDefaultModeUmaskIndependent_spec_6_4(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)

	dir := t.TempDir()
	if err := Materialize(dir, []FileSpec{{Path: "a.txt", Content: "x"}}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	assertMode(t, filepath.Join(dir, "a.txt"), 0o444)
}

// spec: §6.4 — the adapter is a distinct trust boundary, so Materialize
// rejects a path that escapes the shared root through `..`, an absolute
// path, or an empty path.
func TestMaterializeRejectsPathEscape_spec_6_4(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		path string
	}{
		{"parent escape", "../escape.txt"},
		{"nested parent escape", "ok/../../escape.txt"},
		{"absolute", "/etc/passwd"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Materialize(dir, []FileSpec{{Path: tc.path, Content: "x"}})
			if err == nil {
				t.Fatalf("Materialize accepted escaping path %q", tc.path)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "escape.txt")); statErr == nil {
				t.Fatal("escaping write landed inside the shared root")
			}
		})
	}
}

// spec: §13.1 — setuid/setgid asset modes are rejected so a shared asset
// cannot smuggle a privilege-escalation bit onto the pod filesystem.
func TestMaterializeRejectsSetuidSetgid_spec_6_4(t *testing.T) {
	dir := t.TempDir()
	for _, mode := range []string{"4755", "2755", "6755"} {
		if err := Materialize(dir, []FileSpec{{Path: "x", Content: "y", Mode: mode}}); err == nil {
			t.Errorf("Materialize accepted setuid/setgid mode %q", mode)
		}
	}
}

// spec: §6.4 line 409 — an empty shared root is a configuration error
// (the populate step requires a destination directory).
func TestMaterializeRejectsEmptyRoot_spec_6_4(t *testing.T) {
	if err := Materialize("", []FileSpec{{Path: "x", Content: "y"}}); err == nil {
		t.Fatal("Materialize accepted an empty shared root")
	}
}

// spec: §6.4 line 409 — Materialize over an empty asset set is a no-op
// success, leaving the (already-created) directory empty.
func TestMaterializeEmptySetIsNoop_spec_6_4(t *testing.T) {
	dir := t.TempDir()
	if err := Materialize(dir, nil); err != nil {
		t.Fatalf("Materialize(nil): %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("shared root has %d entries, want 0", len(entries))
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm() != want {
		t.Errorf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
	}
}
