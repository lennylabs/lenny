// SPDX-License-Identifier: MIT

package tier0_static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// productionCarrierRoots are the trees the gate scans, relative to the
// repository root.
var productionCarrierRoots = []string{"pkg", "cmd"}

// environmentFallbackOwner is the one directory permitted to name the
// LENNY_ENV variable: providerflags.Bind registers it as the fallback for
// the --environment flag and validates the value in its finalizer.
const environmentFallbackOwner = "pkg/kms/providerflags/"

// TestProductionModeHasOneCarrier asserts that no non-test Go file under
// pkg/ or cmd/ reads production mode from a carrier other than the
// finalized --environment value. It fails on any string literal that is
// exactly "LENNY_ENV" outside pkg/kms/providerflags/, which is a direct
// environment read that bypasses the flag's validation, and on any string
// literal that is exactly "LENNY_PRODUCTION" anywhere, which is a second
// production signal. A failure is fixed by reading deployenv.IsProduction
// on the finalized --environment value, never by adding an exemption to
// this gate.
//
// spec: 17.9.1 (Composition Dimensions) "Production mode"
func TestProductionModeHasOneCarrier(t *testing.T) {
	root := schematest.RepoRoot(t)
	for _, dir := range productionCarrierRoots {
		for _, hit := range productionCarrierHitsInTree(t, root, dir) {
			t.Errorf("%s; read deployenv.IsProduction on the finalized --environment value (§17.9.1 Production mode)", hit)
		}
	}
}

// TestProductionCarrierScannerFindsDirectRead pins the scanner so the gate
// cannot pass vacuously: a direct LENNY_ENV read outside providerflags and a
// LENNY_PRODUCTION literal anywhere are each one finding, the providerflags
// fallback registration is none, a name that only contains LENNY_ENV as a
// prefix is none, and a mention inside a comment is none.
//
// spec: 17.9.1 (Composition Dimensions) "Production mode"
func TestProductionCarrierScannerFindsDirectRead(t *testing.T) {
	cases := []struct {
		name string
		rel  string
		src  string
		want int
	}{
		{
			name: "direct LENNY_ENV read in the gateway",
			rel:  "cmd/lenny-gateway/gates.go",
			src:  "package main\nimport \"os\"\nfunc prod() bool { return os.Getenv(\"LENNY_ENV\") == \"production\" }\n",
			want: 1,
		},
		{
			name: "LENNY_ENV fallback in providerflags",
			rel:  "pkg/kms/providerflags/providerflags.go",
			src:  "package providerflags\nvar envVar = \"LENNY_ENV\"\n",
			want: 0,
		},
		{
			name: "LENNY_PRODUCTION inside providerflags",
			rel:  "pkg/kms/providerflags/providerflags.go",
			src:  "package providerflags\nvar envVar = \"LENNY_PRODUCTION\"\n",
			want: 1,
		},
		{
			name: "LENNY_PRODUCTION flag fallback in lenny-ops",
			rel:  "cmd/lenny-ops/flags.go",
			src:  "package main\nconst productionEnv = `LENNY_PRODUCTION`\n",
			want: 1,
		},
		{
			name: "longer name with the LENNY_ENV prefix",
			rel:  "pkg/sandbox/env.go",
			src:  "package sandbox\nvar block = \"LENNY_ENV_VAR_BLOCKLIST\"\n",
			want: 0,
		},
		{
			name: "mention in a comment",
			rel:  "pkg/sandbox/env.go",
			src:  "package sandbox\n// LENNY_ENV is read by providerflags.\nvar x = 1\n",
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hits, err := productionCarrierHits(c.rel, []byte(c.src))
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if len(hits) != c.want {
				t.Errorf("findings = %v, want %d", hits, c.want)
			}
		})
	}
}

// productionCarrierHitsInTree walks root/dir and returns the findings of
// every non-test Go file. It skips testdata directories, whose fixtures
// record a tree as it was written.
func productionCarrierHitsInTree(t *testing.T, root, dir string) []string {
	t.Helper()
	var hits []string
	err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		found, err := productionCarrierHits(filepath.ToSlash(rel), src)
		if err != nil {
			return err
		}
		hits = append(hits, found...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return hits
}

// productionCarrierHits parses one Go source file at the repository-relative
// slash path rel and returns "rel:line: message" for each string literal that
// names a production carrier other than the finalized --environment value.
func productionCarrierHits(rel string, src []byte) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var hits []string
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		if msg := productionCarrierViolation(rel, value); msg != "" {
			hits = append(hits, rel+":"+itoa(fset.Position(lit.Pos()).Line)+": "+msg)
		}
		return true
	})
	return hits, nil
}

// productionCarrierViolation reports why the literal value in the file at rel
// names a second production carrier, or "" when it does not.
func productionCarrierViolation(rel, value string) string {
	switch value {
	case "LENNY_PRODUCTION":
		return `names "LENNY_PRODUCTION", a second production signal`
	case "LENNY_ENV":
		if strings.HasPrefix(rel, environmentFallbackOwner) {
			return ""
		}
		return `names "LENNY_ENV" outside providerflags, a direct read that bypasses --environment validation`
	}
	return ""
}
