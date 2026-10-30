// SPDX-License-Identifier: MIT

package imagetag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureDockerfile mirrors the root Dockerfile: it downloads modules,
// copies the whole context, compiles ./cmd/$BINARY, and copies a docs tree.
const fixtureDockerfile = `ARG GO_VERSION=1.25
FROM golang:${GO_VERSION} AS build
ARG BINARY
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/app ./cmd/"$BINARY"
FROM scratch
COPY docs/runbooks /docs/runbooks
COPY --from=build /out/app /app
`

// fixtureLayerDockerfile layers a file onto a base image named by a build
// argument, the way the heartbeat-silence fixture layers onto a runtime.
const fixtureLayerDockerfile = `ARG BASE_IMAGE
FROM ${BASE_IMAGE}
COPY extra.txt /extra.txt
`

// writeFixtureModule writes a small Go module whose cmd/a binary imports
// internal/lib, which embeds a data file and carries a file excluded by a
// build constraint; cmd/b is an unrelated binary.
func writeFixtureModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                    "module example.com/fixture\n\ngo 1.21\n",
		"go.sum":                    "",
		"Dockerfile":                fixtureDockerfile,
		"layer.Dockerfile":          fixtureLayerDockerfile,
		"extra.txt":                 "extra\n",
		".dockerignore":             ".git\nbin/\ndocs/runbooks/*.tmp\n",
		"docs/runbooks/a.md":        "runbook\n",
		"README.md":                 "readme\n",
		"bin/artifact":              "built\n",
		"cmd/a/main.go":             "package main\n\nimport \"example.com/fixture/internal/lib\"\n\nfunc main() { _ = lib.Data }\n",
		"cmd/a/main_test.go":        "package main\n",
		"cmd/b/main.go":             "package main\n\nfunc main() {}\n",
		"internal/lib/lib.go":       "package lib\n\nimport _ \"embed\"\n\n//go:embed data.txt\nvar Data string\n",
		"internal/lib/data.txt":     "v1\n",
		"internal/lib/lib_win.go":   "//go:build windows\n\npackage lib\n",
		"internal/unused/unused.go": "package unused\n",
	}
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func fixtureSpecs(t *testing.T) []Spec {
	t.Helper()
	specs, err := ParseSpecs(strings.NewReader(
		"# the binary and a fixture layered on it\n" +
			"app-a Dockerfile BINARY=a\n" +
			"app-b Dockerfile BINARY=b\n" +
			"layered layer.Dockerfile BASE_IMAGE=@app-a\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	return specs
}

func computeTags(t *testing.T, root string) map[string]string {
	t.Helper()
	results, err := Compute(root, fixtureSpecs(t), "", GoList)
	if err != nil {
		t.Fatal(err)
	}
	tags := map[string]string{}
	for _, r := range results {
		if !IsContentTag(r.Tag) {
			t.Fatalf("image %s: tag %q is not a content tag", r.Name, r.Tag)
		}
		tags[r.Name] = r.Tag
	}
	return tags
}

// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
//
// The same inputs give the same tag; a change to any input of an image changes
// that image's tag and the tag of every image built on it; and a change to a
// file the image does not read leaves its tag alone, so the install reuses it.
func TestContentTagTracksEveryBuildInputAndNothingElse(t *testing.T) {
	type change struct {
		name    string
		mutate  func(t *testing.T, root string)
		changed []string // images whose tag must change; every other tag must not
	}
	write := func(rel, body string) func(*testing.T, string) {
		return func(t *testing.T, root string) {
			t.Helper()
			p := filepath.Join(root, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	all := []string{"app-a", "app-b", "layered"}
	aAndLayered := []string{"app-a", "layered"}
	cases := []change{
		{name: "binary's own source", mutate: write("cmd/a/main.go", "package main\n\nimport \"example.com/fixture/internal/lib\"\n\nfunc main() { _ = lib.Data; println() }\n"), changed: aAndLayered},
		{name: "transitive dependency source", mutate: write("internal/lib/lib.go", "package lib\n\nimport _ \"embed\"\n\n//go:embed data.txt\nvar Data string\n\nvar X = 1\n"), changed: aAndLayered},
		{name: "embedded file", mutate: write("internal/lib/data.txt", "v2\n"), changed: aAndLayered},
		{name: "file excluded by a build constraint on this host", mutate: write("internal/lib/lib_win.go", "//go:build windows\n\npackage lib\n\nvar Y = 2\n"), changed: aAndLayered},
		{name: "untracked new file in a closure package", mutate: write("internal/lib/new.go", "package lib\n"), changed: aAndLayered},
		{name: "other binary's source", mutate: write("cmd/b/main.go", "package main\n\nfunc main() { println() }\n"), changed: []string{"app-b"}},
		{name: "go.mod", mutate: write("go.mod", "module example.com/fixture\n\ngo 1.22\n"), changed: all},
		{name: "go.sum", mutate: write("go.sum", "example.com/x v1.0.0 h1:abc=\n"), changed: all},
		{name: "Dockerfile", mutate: write("Dockerfile", fixtureDockerfile+"ENV X=1\n"), changed: all},
		{name: "copied docs file", mutate: write("docs/runbooks/a.md", "runbook v2\n"), changed: all},
		{name: "new copied docs file", mutate: write("docs/runbooks/b.md", "new\n"), changed: all},
		{name: "layer's own context file", mutate: write("extra.txt", "extra v2\n"), changed: []string{"layered"}},
		{name: "layer's Dockerfile", mutate: write("layer.Dockerfile", fixtureLayerDockerfile+"ENV Y=1\n"), changed: []string{"layered"}},
		{name: "test file in a closure package", mutate: write("cmd/a/main_test.go", "package main\n\nvar T = 1\n"), changed: nil},
		{name: "package outside every closure", mutate: write("internal/unused/unused.go", "package unused\n\nvar Z = 1\n"), changed: nil},
		{name: "file outside every input", mutate: write("README.md", "readme v2\n"), changed: nil},
		{name: "dockerignored file under a copied tree", mutate: write("docs/runbooks/scratch.tmp", "scratch\n"), changed: nil},
		{name: "dockerignored build output", mutate: write("bin/artifact", "rebuilt\n"), changed: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeFixtureModule(t)
			before := computeTags(t, root)
			if again := computeTags(t, root); !mapsEqual(before, again) {
				t.Fatalf("same inputs gave different tags: %v then %v", before, again)
			}
			tc.mutate(t, root)
			after := computeTags(t, root)
			want := map[string]bool{}
			for _, n := range tc.changed {
				want[n] = true
			}
			for _, n := range all {
				if got := before[n] != after[n]; got != want[n] {
					t.Errorf("image %s: tag changed = %v, want %v (before %s, after %s)", n, got, want[n], before[n], after[n])
				}
			}
		})
	}
}

// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
//
// A changed literal build argument changes the tag, a reference argument
// resolves to the referenced image's tag, and a fixed tag replaces hashing.
func TestComputeArgumentsReferencesAndFixedTag(t *testing.T) {
	root := writeFixtureModule(t)
	results, err := Compute(root, fixtureSpecs(t), "", GoList)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Result{}
	for _, r := range results {
		byName[r.Name] = r
	}
	if byName["app-a"].Tag == byName["app-b"].Tag {
		t.Errorf("images differing only in BINARY share tag %s", byName["app-a"].Tag)
	}
	if got, want := byName["layered"].ResolvedArgs[0].Value, "app-a:"+byName["app-a"].Tag; got != want {
		t.Errorf("layered BASE_IMAGE = %q, want %q", got, want)
	}

	fixed, err := Compute(root, fixtureSpecs(t), "e2e", func(string, []string) (map[string]*GoPackage, error) {
		t.Fatal("a fixed tag must not list Go packages")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range fixed {
		if r.Tag != "e2e" {
			t.Errorf("fixed mode: image %s tag %q, want e2e", r.Name, r.Tag)
		}
	}
	if got := fixed[len(fixed)-1].ResolvedArgs[0].Value; got != "app-a:e2e" {
		t.Errorf("fixed mode: layered BASE_IMAGE = %q, want app-a:e2e", got)
	}
}

// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
//
// A missing COPY source, a package that does not exist, and a package that
// fails to load are errors, because a guessed input set can deploy a stale
// image.
func TestComputeFailsClosedOnMissingInputs(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(root string) error
		specs   string
		wantErr string
	}{
		{
			name:    "missing COPY source",
			mutate:  func(root string) error { return os.RemoveAll(filepath.Join(root, "docs")) },
			specs:   "app-a Dockerfile BINARY=a\n",
			wantErr: "docs/runbooks",
		},
		{
			name:    "missing package",
			mutate:  func(string) error { return nil },
			specs:   "app-c Dockerfile BINARY=c\n",
			wantErr: "cmd/c",
		},
		{
			name: "package whose import clause does not parse",
			mutate: func(root string) error {
				return os.WriteFile(filepath.Join(root, "internal/lib/lib.go"), []byte("package lib\n\nimport (\n"), 0o644)
			},
			specs:   "app-a Dockerfile BINARY=a\n",
			wantErr: "internal/lib",
		},
		{
			name:    "missing Dockerfile",
			mutate:  func(string) error { return nil },
			specs:   "app-a Nope.Dockerfile\n",
			wantErr: "Nope.Dockerfile",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeFixtureModule(t)
			if err := tc.mutate(root); err != nil {
				t.Fatal(err)
			}
			specs, err := ParseSpecs(strings.NewReader(tc.specs))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compute(root, specs, "", GoList)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one naming %q", err, tc.wantErr)
			}
		})
	}
}

// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
//
// The spec list parser accepts comments, options, and build arguments, and
// rejects malformed lines; Order puts every image after the images it refers
// to and rejects unknown references and cycles.
func TestParseSpecsAndOrder(t *testing.T) {
	specs, err := ParseSpecs(strings.NewReader(
		"\n# comment\nover o.Dockerfile --context=sub BASE=@base --no-load\nbase Dockerfile BINARY=x\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 || specs[0].Context != "sub" || specs[0].Load || !specs[1].Load {
		t.Fatalf("parsed %+v", specs)
	}
	ordered, err := Order(specs)
	if err != nil {
		t.Fatal(err)
	}
	if ordered[0].Name != "base" || ordered[1].Name != "over" {
		t.Errorf("order = %s, %s; want base, over", ordered[0].Name, ordered[1].Name)
	}

	for _, bad := range []struct{ text, want string }{
		{"onlyname\n", "want <name>"},
		{"a Dockerfile --bogus\n", "unknown option"},
		{"a Dockerfile NOEQUALS\n", "KEY=VALUE"},
		{"a Dockerfile\na Dockerfile\n", "listed twice"},
	} {
		if _, err := ParseSpecs(strings.NewReader(bad.text)); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("ParseSpecs(%q) err = %v, want one naming %q", bad.text, err, bad.want)
		}
	}
	for _, bad := range []struct{ text, want string }{
		{"a Dockerfile X=@missing\n", "not in the spec list"},
		{"a Dockerfile X=@b\nb Dockerfile Y=@a\n", "cycle"},
	} {
		s, err := ParseSpecs(strings.NewReader(bad.text))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Order(s); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("Order(%q) err = %v, want one naming %q", bad.text, err, bad.want)
		}
	}
}

// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
//
// Only the generated form src-<12 hex digits> is a content tag, so the
// install script's prune never touches e2e, e2e-journey, or another fixed tag.
func TestIsContentTag(t *testing.T) {
	for tag, want := range map[string]bool{
		"src-0123456789ab":   true,
		"e2e":                false,
		"e2e-journey":        false,
		"src-0123456789AB":   false,
		"src-0123456789a":    false,
		"src-0123456789abc":  false,
		"x-src-0123456789ab": false,
	} {
		if got := IsContentTag(tag); got != want {
			t.Errorf("IsContentTag(%q) = %v, want %v", tag, got, want)
		}
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
