// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"go/ast"
	"sort"
	"strings"
	"testing"
)

// callersOf returns, sorted, the "file func" keys of the functions under
// pkg/ and cmd/ that call a function or method named name. A selector whose
// receiver is a package named in skipPkgs is not counted.
func callersOf(funcs map[string]scannedFunc, name string, skipPkgs ...string) []string {
	skip := map[string]bool{}
	for _, p := range skipPkgs {
		skip[p] = true
	}
	var out []string
	for key, sf := range funcs {
		found := false
		ast.Inspect(sf.decl.Body, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok || found {
				return !found
			}
			switch f := c.Fun.(type) {
			case *ast.SelectorExpr:
				if f.Sel.Name == name {
					if id, ok := f.X.(*ast.Ident); ok && skip[id.Name] {
						return true
					}
					found = true
				}
			case *ast.Ident:
				found = f.Name == name
			}
			return true
		})
		if found {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// TestCredentialLeaseReturnHasNamedReleasersOnly_spec_7_3 asserts that the
// callers under pkg/ and cmd/ of the session-wide and generation-scoped lease
// releases, and of releasePodCredentials and releaseAttemptCredentials, are
// exactly the releasers the credential lease return rule names: a published
// binding's release through Binder.Release and Binder.ReleaseSlot, an
// unpublished attempt's release by identifier, a pre-running claim's release
// through releaseCredentials (failPhase and ReclaimClaimed), and the recorded
// revokeFinalizeLease departure. A terminal writer, sweep, or handler that
// returns leases by session ID for a binding it does not hold fails it.
//
// spec: §7.1 (Normal Flow), §7.3 (Credential leases on the replacement pod)
func TestCredentialLeaseReturnHasNamedReleasersOnly_spec_7_3(t *testing.T) {
	t.Parallel()
	funcs, _ := scanRepo(t)
	const ps = "pkg/gateway/podlifecycle/podsession/"
	exclude := func(keys []string) []string {
		// The assigners' own internal calls are excluded.
		var out []string
		for _, k := range keys {
			if strings.HasPrefix(k, "pkg/gateway/credentials/") {
				continue
			}
			out = append(out, k)
		}
		return out
	}
	for _, tc := range []struct {
		name string
		skip []string
		want []string
	}{
		{"releasePodCredentials", nil, []string{ps + "release.go Release", ps + "slotbinder.go ReleaseSlot"}},
		{"ReleaseThrough", nil, []string{ps + "credentiallease.go releasePodCredentials"}},
		{"ReleaseSession", []string{"executor"}, []string{
			ps + "credentiallease.go releaseCredentials",
			"pkg/gateway/sessionserver/finalize.go revokeFinalizeLease",
		}},
		{"releaseCredentials", nil, []string{ps + "reclaim.go ReclaimClaimed", ps + "reclaim.go failPhase"}},
		{"releaseAttemptCredentials", nil, []string{
			ps + "bindlaunch.go Launch",
			ps + "bindprepare.go Prepare",
			ps + "credentiallease.go releasePodCredentials",
			ps + "resume.go ReleaseUncommitted",
			ps + "resume.go failResume",
			ps + "slotbinder.go materializeSlot",
		}},
	} {
		got := exclude(callersOf(funcs, tc.name, tc.skip...))
		sort.Strings(tc.want)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("callers of %s = %v, want %v", tc.name, got, tc.want)
		}
	}
}
