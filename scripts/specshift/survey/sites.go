// SPDX-License-Identifier: MIT

package survey

import (
	"bytes"
	"context"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// Site kinds, as the annotation index records them.
const (
	SiteCode    = annotation.SiteCode
	SiteTest    = annotation.SiteTest
	SitePackage = annotation.SitePackage
)

// Site is one annotation that cites an identifier, as the annotation
// index records it.
type Site = annotation.Site

// goFiles lists the Go files of the working tree inside the shared read
// domain, which leaves out testdata, the change records, and the staged
// proposals. The working tree includes the untracked files that are not
// ignored, so the helpers report on work not yet committed.
func goFiles(ctx context.Context, r repo) ([]string, error) {
	return annotation.GoFiles(ctx, func(ctx context.Context) ([]string, error) {
		return r.worktreeFiles(ctx, "*.go")
	})
}

// findSites returns, for each wanted identifier, the annotations in the
// working tree's Go files that cite it. It reads the annotation index of
// the files that mention one of the identifiers, and compares
// identifiers in lower case, because an anchor is the lower-cased
// identifier.
func findSites(ctx context.Context, r repo, wanted []string) (map[string][]Site, error) {
	out := map[string][]Site{}
	if len(wanted) == 0 {
		return out, nil
	}
	needles := make([][]byte, 0, len(wanted))
	for _, w := range wanted {
		needles = append(needles, []byte(strings.ToLower(w)))
	}
	files, err := goFiles(ctx, r)
	if err != nil {
		return nil, err
	}
	keep := func(_ string, src []byte) bool { return containsAny(bytes.ToLower(src), needles) }
	idx, err := annotation.IndexFiles(files, scope.DirReader(r.root), keep)
	if err != nil {
		return nil, err
	}
	for _, w := range wanted {
		if sites := idx.Sites(w); len(sites) > 0 {
			out[w] = append(out[w], sites...)
		}
	}
	return out, nil
}

// containsAny reports whether src contains one of the needles.
func containsAny(src []byte, needles [][]byte) bool {
	for _, n := range needles {
		if bytes.Contains(src, n) {
			return true
		}
	}
	return false
}

// splitSites separates the code sites, the tests, and the package doc
// comments of one identifier.
func splitSites(sites []Site) (code, tests, packages []Site) {
	for _, s := range sites {
		switch s.Kind {
		case SiteTest:
			tests = append(tests, s)
		case SitePackage:
			packages = append(packages, s)
		default:
			code = append(code, s)
		}
	}
	return code, tests, packages
}

// deciders returns the distinct functions and methods outside test files
// that cite an identifier.
func deciders(sites []Site) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sites {
		if s.Kind != SiteCode || !s.Func {
			continue
		}
		key := s.Path + " " + s.Decl
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	return out
}

// decidersWarning is the number of functions above which the digest lists
// a requirement, so a reviewer checks that its decision is not
// duplicated.
const decidersWarning = 3
