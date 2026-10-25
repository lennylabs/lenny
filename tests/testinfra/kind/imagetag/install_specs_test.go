// SPDX-License-Identifier: MIT

package imagetag

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// installSpecs extracts the IMAGE_SPECS here-document from install.sh.
func installSpecs(t *testing.T) (specs []Spec, script string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, "tests/testinfra/kind/install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	specs, err = InstallScriptSpecs(string(b))
	if err != nil {
		t.Fatalf("install.sh: %v", err)
	}
	return specs, string(b)
}

// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
//
// The install script's image inventory parses, every Dockerfile, COPY source,
// and Go package it names exists, every image it references is in the list,
// and its content tags are stable across two computations and distinct per
// image. Every image the script deploys by name through image_ref or
// image_tag is a loaded image of the inventory, so no rendered reference
// names a tag that was never loaded.
func TestInstallScriptImageSpecsComputeStableDistinctTags(t *testing.T) {
	specs, script := installSpecs(t)
	first, err := Compute(repoRoot, specs, "", GoList)
	if err != nil {
		t.Fatalf("compute install.sh image tags: %v", err)
	}
	second, err := Compute(repoRoot, specs, "", GoList)
	if err != nil {
		t.Fatal(err)
	}
	loaded := map[string]bool{}
	byTag := map[string]string{}
	for i, r := range first {
		if r.Tag != second[i].Tag {
			t.Errorf("image %s: tag %s then %s from the same tree", r.Name, r.Tag, second[i].Tag)
		}
		if other, dup := byTag[r.Tag]; dup {
			t.Errorf("images %s and %s share tag %s", other, r.Name, r.Tag)
		}
		byTag[r.Tag] = r.Name
		if r.Load {
			loaded[r.Name] = true
		}
	}

	refs := regexp.MustCompile(`image_(?:ref|tag) ([a-z0-9-]+)\)`).FindAllStringSubmatch(script, -1)
	if len(refs) == 0 {
		t.Fatal("install.sh calls neither image_ref nor image_tag")
	}
	for _, m := range refs {
		if !loaded[m[1]] {
			t.Errorf("install.sh deploys %s, which is not a loaded image in IMAGE_SPECS", m[1])
		}
	}
}

// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
//
// Every Dockerfile in the inventory is analyzed precisely, so no image falls
// back to hashing the whole context, which would rebuild it on any change
// anywhere in the tree.
func TestInstallScriptImageSpecsAvoidTheWholeContextFallback(t *testing.T) {
	specs, _ := installSpecs(t)
	for _, s := range specs {
		text, err := os.ReadFile(filepath.Join(repoRoot, s.Dockerfile))
		if err != nil {
			t.Fatal(err)
		}
		literal := map[string]string{}
		for _, a := range s.Args {
			if a.Dep() == "" {
				literal[a.Key] = a.Value
			}
		}
		in, err := analyzeDockerfile(string(text), literal)
		if err != nil {
			t.Fatalf("image %s: %v", s.Name, err)
		}
		if in.WholeContext {
			t.Errorf("image %s: %s falls back to the whole context; teach the analyzer its inputs", s.Name, s.Dockerfile)
		}
	}
}

// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
//
// Extraction fails on a script without the IMAGE_SPECS here-document, on an
// unterminated one, and on a malformed line inside it.
func TestInstallScriptSpecsRejectsMalformedScripts(t *testing.T) {
	for _, tc := range []struct{ script, want string }{
		{"#!/bin/sh\n", "no"},
		{installSpecsOpen + "a Dockerfile\n", "closing SPECS"},
		{installSpecsOpen + "lonely\nSPECS\n", "want <name>"},
	} {
		if _, err := InstallScriptSpecs(tc.script); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("InstallScriptSpecs(%q) err = %v, want one naming %q", tc.script, err, tc.want)
		}
	}
	specs, err := InstallScriptSpecs("x\n" + installSpecsOpen + "# c\na Dockerfile B=1\nSPECS\ny\n")
	if err != nil || len(specs) != 1 || specs[0].Name != "a" {
		t.Fatalf("InstallScriptSpecs = %+v, %v", specs, err)
	}
}
