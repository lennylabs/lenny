// SPDX-License-Identifier: MIT

// Package imagetag computes content-addressed tags for the images the Kind
// install script builds. A tag is a hash of every input the image's build
// reads: the Dockerfile, the build arguments, the context files its COPY and
// ADD instructions name, the Go package closure of the binaries it compiles,
// and the tags of the images it builds on. An unchanged image keeps its tag,
// so the install script reuses it, and a changed input changes the tag, so the
// script rebuilds and reloads it.
//
// It is test infrastructure: no product image is tagged this way.
package imagetag

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// TagPrefix starts every content tag, so the install script's prune step can
// tell a content tag from a fixed tag such as e2e.
const TagPrefix = "src-"

// hashLen is the number of hex digits of the SHA-256 digest a tag keeps.
const hashLen = 12

// schemeVersion is hashed first. Changing how inputs are hashed changes it,
// so a tag computed under an older scheme is never reused.
const schemeVersion = "lenny-image-tag v1"

// tagPattern matches a content tag.
var tagPattern = regexp.MustCompile(`^` + TagPrefix + `[0-9a-f]{12}$`)

// IsContentTag reports whether tag is a content tag.
func IsContentTag(tag string) bool { return tagPattern.MatchString(tag) }

// Spec is one image the install script builds.
type Spec struct {
	// Name is the image repository name.
	Name string
	// Dockerfile is the Dockerfile path relative to the repository root.
	Dockerfile string
	// Context is the build context directory relative to the repository
	// root. Empty means the repository root.
	Context string
	// Args are the build arguments in the order the spec lists them. A
	// value of the form @<name> refers to another image in the same spec
	// list and is replaced by that image's reference.
	Args []Arg
	// Load is false for an image that is only a build base and is never
	// loaded onto the cluster.
	Load bool
}

// Arg is one build argument.
type Arg struct{ Key, Value string }

// Dep returns the image name an argument refers to, or "" for a literal.
func (a Arg) Dep() string {
	if strings.HasPrefix(a.Value, "@") {
		return a.Value[1:]
	}
	return ""
}

// Result is a Spec with its computed tag.
type Result struct {
	Spec
	Tag string
	// ResolvedArgs are the build arguments with every @<name> reference
	// replaced by <name>:<tag>.
	ResolvedArgs []Arg
}

// ParseSpecs reads the spec list. Each non-blank line that is not a comment
// is `<name> <dockerfile> [--context=<dir>] [--no-load] [KEY=VALUE ...]`.
func ParseSpecs(r io.Reader) ([]Spec, error) {
	var specs []Spec
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			return nil, fmt.Errorf("line %d: want <name> <dockerfile> [options] [KEY=VALUE ...], got %q", lineNo, line)
		}
		s := Spec{Name: f[0], Dockerfile: f[1], Load: true}
		for _, tok := range f[2:] {
			switch {
			case tok == "--no-load":
				s.Load = false
			case strings.HasPrefix(tok, "--context="):
				s.Context = strings.TrimPrefix(tok, "--context=")
			case strings.HasPrefix(tok, "--"):
				return nil, fmt.Errorf("line %d: unknown option %q", lineNo, tok)
			default:
				k, v, ok := strings.Cut(tok, "=")
				if !ok || k == "" {
					return nil, fmt.Errorf("line %d: build argument %q is not KEY=VALUE", lineNo, tok)
				}
				s.Args = append(s.Args, Arg{Key: k, Value: v})
			}
		}
		if seen[s.Name] {
			return nil, fmt.Errorf("line %d: image %s is listed twice", lineNo, s.Name)
		}
		seen[s.Name] = true
		specs = append(specs, s)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return specs, nil
}

// installSpecsOpen is the line of install.sh that opens the IMAGE_SPECS
// here-document.
const installSpecsOpen = "read -r -d '' IMAGE_SPECS <<'SPECS' || true\n"

// InstallScriptSpecs extracts and parses the IMAGE_SPECS here-document of
// the Kind install script.
func InstallScriptSpecs(script string) ([]Spec, error) {
	start := strings.Index(script, installSpecsOpen)
	if start < 0 {
		return nil, fmt.Errorf("the install script has no %q line", strings.TrimSpace(installSpecsOpen))
	}
	body := script[start+len(installSpecsOpen):]
	end := strings.Index(body, "\nSPECS\n")
	if end < 0 {
		return nil, errors.New("the install script's IMAGE_SPECS here-document has no closing SPECS line")
	}
	specs, err := ParseSpecs(strings.NewReader(body[:end]))
	if err != nil {
		return nil, fmt.Errorf("IMAGE_SPECS: %w", err)
	}
	return specs, nil
}

// Order returns the specs in build order: every image after the images its
// arguments refer to, and otherwise in listing order.
func Order(specs []Spec) ([]Spec, error) {
	byName := map[string]Spec{}
	for _, s := range specs {
		byName[s.Name] = s
	}
	var out []Spec
	state := map[string]int{} // 1 visiting, 2 done
	var visit func(name string, from string) error
	visit = func(name, from string) error {
		s, ok := byName[name]
		if !ok {
			return fmt.Errorf("image %s refers to @%s, which is not in the spec list", from, name)
		}
		switch state[name] {
		case 1:
			return fmt.Errorf("image %s is part of a reference cycle", name)
		case 2:
			return nil
		}
		state[name] = 1
		for _, a := range s.Args {
			if d := a.Dep(); d != "" {
				if err := visit(d, name); err != nil {
					return err
				}
			}
		}
		state[name] = 2
		out = append(out, s)
		return nil
	}
	for _, s := range specs {
		if err := visit(s.Name, s.Name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GoLister lists the Go packages in the closure of the given ./-relative
// package paths under root, keyed by import path.
type GoLister func(root string, pkgs []string) (map[string]*GoPackage, error)

// GoPackage is the subset of `go list -json` output the hash reads.
type GoPackage struct {
	ImportPath        string
	Standard          bool
	Module            *struct{ Main bool }
	Deps              []string
	GoFiles           []string
	CgoFiles          []string
	IgnoredGoFiles    []string
	IgnoredOtherFiles []string
	CFiles            []string
	CXXFiles          []string
	HFiles            []string
	SFiles            []string
	SysoFiles         []string
	OtherFiles        []string
	EmbedFiles        []string
	Error             *struct{ Err string }
}

// files returns the package's source files that can affect a build: every
// non-test file in the directory, including those a build constraint
// excludes on the listing host, because the image's build platform may
// differ.
func (p *GoPackage) files() []string {
	var out []string
	for _, list := range [][]string{
		p.GoFiles, p.CgoFiles, p.IgnoredGoFiles, p.IgnoredOtherFiles, p.CFiles,
		p.CXXFiles, p.HFiles, p.SFiles, p.SysoFiles, p.OtherFiles, p.EmbedFiles,
	} {
		for _, f := range list {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			out = append(out, f)
		}
	}
	return out
}

// GoList is the GoLister that runs `go list -deps -json` with the
// environment the Dockerfiles build under (CGO_ENABLED=0, GOOS=linux).
func GoList(root string, pkgs []string) (map[string]*GoPackage, error) {
	if len(pkgs) == 0 {
		return map[string]*GoPackage{}, nil
	}
	args := append([]string{"list", "-e", "-deps", "-json"}, pkgs...)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %v: %s", err, stderr.String())
	}
	res := map[string]*GoPackage{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p GoPackage
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		res[p.ImportPath] = &p
	}
	return res, nil
}

// Compute returns the build-ordered results for specs, rooted at the
// repository root. When fixedTag is non-empty every image gets that tag and
// nothing is hashed; this is the install script's explicit-tag mode.
func Compute(root string, specs []Spec, fixedTag string, list GoLister) ([]Result, error) {
	ordered, err := Order(specs)
	if err != nil {
		return nil, err
	}
	tags := map[string]string{}
	var results []Result
	if fixedTag != "" {
		for _, s := range ordered {
			tags[s.Name] = fixedTag
			results = append(results, Result{Spec: s, Tag: fixedTag, ResolvedArgs: resolve(s.Args, tags)})
		}
		return results, nil
	}

	modulePath, err := readModulePath(root)
	if err != nil {
		return nil, err
	}
	analyses := make([]dockerfileInputs, len(ordered))
	var allPkgs []string
	pkgSeen := map[string]bool{}
	for i, s := range ordered {
		text, err := os.ReadFile(filepath.Join(root, s.Dockerfile))
		if err != nil {
			return nil, fmt.Errorf("image %s: %w", s.Name, err)
		}
		literal := map[string]string{}
		for _, a := range s.Args {
			if a.Dep() == "" {
				literal[a.Key] = a.Value
			}
		}
		in, err := analyzeDockerfile(string(text), literal)
		if err != nil {
			return nil, fmt.Errorf("image %s: %s: %w", s.Name, s.Dockerfile, err)
		}
		analyses[i] = in
		for _, p := range in.GoPackages {
			if !pkgSeen[p] {
				pkgSeen[p] = true
				allPkgs = append(allPkgs, p)
			}
		}
	}
	pkgs, err := list(root, allPkgs)
	if err != nil {
		return nil, err
	}

	for i, s := range ordered {
		files, err := inputFiles(root, s, analyses[i], modulePath, pkgs)
		if err != nil {
			return nil, fmt.Errorf("image %s: %w", s.Name, err)
		}
		h := sha256.New()
		fmt.Fprintf(h, "%s\n", schemeVersion)
		df, err := fileDigest(filepath.Join(root, s.Dockerfile))
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(h, "dockerfile %s %s\n", s.Dockerfile, df)
		fmt.Fprintf(h, "context %s\n", contextDir(s))
		args := append([]Arg(nil), s.Args...)
		sort.SliceStable(args, func(a, b int) bool { return args[a].Key < args[b].Key })
		for _, a := range args {
			if d := a.Dep(); d != "" {
				fmt.Fprintf(h, "arg %s=@%s:%s\n", a.Key, d, tags[d])
			} else {
				fmt.Fprintf(h, "arg %s=%s\n", a.Key, a.Value)
			}
		}
		for _, f := range files {
			d, err := fileDigest(filepath.Join(root, f))
			if err != nil {
				return nil, fmt.Errorf("image %s: %w", s.Name, err)
			}
			fmt.Fprintf(h, "file %s %s\n", f, d)
		}
		tag := TagPrefix + hex.EncodeToString(h.Sum(nil))[:hashLen]
		tags[s.Name] = tag
		results = append(results, Result{Spec: s, Tag: tag, ResolvedArgs: resolve(s.Args, tags)})
	}
	return results, nil
}

func resolve(args []Arg, tags map[string]string) []Arg {
	out := make([]Arg, 0, len(args))
	for _, a := range args {
		if d := a.Dep(); d != "" {
			out = append(out, Arg{Key: a.Key, Value: d + ":" + tags[d]})
			continue
		}
		out = append(out, a)
	}
	return out
}

func contextDir(s Spec) string {
	if s.Context == "" {
		return "."
	}
	return path.Clean(s.Context)
}

// inputFiles returns the sorted, repository-relative files an image's build
// reads from its context.
func inputFiles(root string, s Spec, in dockerfileInputs, modulePath string, pkgs map[string]*GoPackage) ([]string, error) {
	ctx := contextDir(s)
	ignore, err := readDockerignore(filepath.Join(root, ctx))
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	add := func(rel string) { set[path.Join(ctx, rel)] = true }

	if in.WholeContext {
		if err := walkContext(filepath.Join(root, ctx), ".", ignore, add); err != nil {
			return nil, err
		}
	}
	for _, src := range in.Sources {
		matches, err := filepath.Glob(filepath.Join(root, ctx, filepath.FromSlash(src)))
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("COPY source %s does not exist in context %s", src, ctx)
		}
		for _, m := range matches {
			rel, err := filepath.Rel(filepath.Join(root, ctx), m)
			if err != nil {
				return nil, err
			}
			if err := walkContext(filepath.Join(root, ctx), filepath.ToSlash(rel), ignore, add); err != nil {
				return nil, err
			}
		}
	}
	if len(in.GoPackages) > 0 {
		if ctx != "." {
			return nil, fmt.Errorf("a Go build needs the repository root as its context, got %s", ctx)
		}
		set["go.mod"] = true
		if _, err := os.Stat(filepath.Join(root, "go.sum")); err == nil {
			set["go.sum"] = true
		}
		for _, rel := range in.GoPackages {
			importPath := modulePath
			if c := path.Clean(rel); c != "." {
				importPath += "/" + c
			}
			rootPkg, ok := pkgs[importPath]
			if !ok {
				return nil, fmt.Errorf("go list returned no package %s", importPath)
			}
			closure := append([]string{importPath}, rootPkg.Deps...)
			for _, ip := range closure {
				p, ok := pkgs[ip]
				if !ok || p.Standard || p.Module == nil || !p.Module.Main {
					// Standard-library packages come from the build image's
					// toolchain, and other modules are pinned by go.sum.
					continue
				}
				if p.Error != nil {
					return nil, fmt.Errorf("package %s: %s", ip, p.Error.Err)
				}
				dir := strings.TrimPrefix(strings.TrimPrefix(ip, modulePath), "/")
				for _, f := range p.files() {
					set[path.Join(dir, f)] = true
				}
			}
		}
	}
	return sortedKeys(set), nil
}

// walkContext adds rel, or every regular file under it when it is a
// directory, skipping paths the context's .dockerignore excludes.
func walkContext(ctxAbs, rel string, ignore []string, add func(string)) error {
	start := filepath.Join(ctxAbs, filepath.FromSlash(rel))
	return filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		r, err := filepath.Rel(ctxAbs, p)
		if err != nil {
			return err
		}
		r = filepath.ToSlash(r)
		if r != "." && ignored(r, ignore) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			add(r)
		}
		return nil
	})
}

// readDockerignore returns the context's .dockerignore patterns. Negated
// patterns are dropped, which only makes the hash read more files.
func readDockerignore(ctxAbs string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(ctxAbs, ".dockerignore"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		out = append(out, path.Clean(strings.TrimPrefix(line, "/")))
	}
	return out, nil
}

// ignored reports whether a context-relative path matches a .dockerignore
// pattern, either directly or through one of its parent directories.
func ignored(rel string, patterns []string) bool {
	for _, pat := range patterns {
		for p := rel; p != "." && p != "/" && p != ""; p = path.Dir(p) {
			if ok, _ := path.Match(pat, p); ok {
				return true
			}
		}
	}
	return false
}

func fileDigest(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readModulePath(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "module" {
			return strings.Trim(f[1], `"`), nil
		}
	}
	return "", fmt.Errorf("%s/go.mod declares no module path", root)
}
