// SPDX-License-Identifier: MIT

package survey

import (
	"context"
	"go/build/constraint"
	"path"
	"strings"
)

// maxConstraintTags bounds the tags a build constraint is searched over.
// A constraint with more tags is read as buildable, which keeps the search
// small; the constraints in the tree name a few tags each.
const maxConstraintTags = 12

// ignoredPath reports whether the go tool leaves a file out of every
// package: a path with a folder or file name that starts with `_` or `.`,
// or a file under a nested module, which the root module does not build.
func ignoredPath(p string, modules []string) bool {
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, "_") || strings.HasPrefix(seg, ".") {
			return true
		}
	}
	for _, m := range modules {
		if strings.HasPrefix(p, m+"/") {
			return true
		}
	}
	return false
}

// nestedModules returns the folders of the working tree that hold a
// go.mod other than the root's.
func nestedModules(ctx context.Context, r repo) ([]string, error) {
	files, err := r.worktreeFiles(ctx, ":(glob)**/go.mod")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range files {
		if dir := path.Dir(f); dir != "." {
			out = append(out, dir)
		}
	}
	return out, nil
}

// buildable reports whether some set of build tags builds a Go source:
// it has no `//go:build` line before its package clause, or the line's
// expression holds for some assignment of its tags with `ignore` unset.
// A file constrained to `ignore`, or to a contradiction, never builds, so
// a test in it runs in no tier.
func buildable(src []byte) bool {
	expr := buildConstraint(src)
	if expr == nil {
		return true
	}
	var tags []string
	collectTags(expr, map[string]bool{"ignore": true}, &tags)
	if len(tags) > maxConstraintTags {
		return true
	}
	for mask := 0; mask < 1<<len(tags); mask++ {
		set := map[string]bool{}
		for i, t := range tags {
			set[t] = mask&(1<<i) != 0
		}
		if expr.Eval(func(tag string) bool { return set[tag] }) {
			return true
		}
	}
	return false
}

// collectTags appends the tags of an expression not yet seen.
func collectTags(e constraint.Expr, seen map[string]bool, tags *[]string) {
	switch x := e.(type) {
	case *constraint.TagExpr:
		if !seen[x.Tag] {
			seen[x.Tag] = true
			*tags = append(*tags, x.Tag)
		}
	case *constraint.NotExpr:
		collectTags(x.X, seen, tags)
	case *constraint.AndExpr:
		collectTags(x.X, seen, tags)
		collectTags(x.Y, seen, tags)
	case *constraint.OrExpr:
		collectTags(x.X, seen, tags)
		collectTags(x.Y, seen, tags)
	}
}

// buildConstraint returns the `//go:build` expression of a Go source, read
// before its package clause as the go tool reads it, or nil when it has
// none or the line does not parse. A line that does not parse is left to
// the compiler, which refuses it.
func buildConstraint(src []byte) constraint.Expr {
	inBlock := false
	for _, raw := range strings.Split(string(src), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case inBlock:
			inBlock = !strings.Contains(line, "*/")
		case constraint.IsGoBuild(line):
			expr, err := constraint.Parse(line)
			if err != nil {
				return nil
			}
			return expr
		case line == "" || strings.HasPrefix(line, "//"):
		case strings.HasPrefix(line, "/*"):
			inBlock = !strings.Contains(line[2:], "*/")
		default:
			return nil
		}
	}
	return nil
}
