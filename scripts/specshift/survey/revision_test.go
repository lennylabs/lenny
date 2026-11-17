// SPDX-License-Identifier: MIT

package survey

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// These tests cover contributor tooling rather than a platform behavior,
// so they carry no specification annotation. They pin the handling of a
// user-given revision: `since` and `digest -base` resolve it with
// `git rev-parse --verify --end-of-options` and refuse one that git would
// read as an option.

func TestSinceAndDigestRefuseARevisionThatGitWouldReadAsAnOption(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	out := t.TempDir()
	for i, run := range []func(rev string) error{
		func(rev string) error { return Since(ctx, f.root, rev, &bytes.Buffer{}) },
		func(rev string) error {
			_, err := Digest(ctx, f.root, DigestOptions{Step: "0200.S1", Base: rev})
			return err
		},
	} {
		target := filepath.Join(out, "pwned"+string(rune('a'+i)))
		rev := "--output=" + target
		err := run(rev)
		if err == nil {
			t.Errorf("revision %q was accepted; want an error", rev)
		} else if !strings.Contains(err.Error(), `starts with "-"`) {
			t.Errorf("revision %q: error %q does not say the revision starts with a dash", rev, err)
		}
		matches, _ := filepath.Glob(target + "*")
		if len(matches) != 0 {
			t.Errorf("revision %q made git write %v", rev, matches)
		}
	}
}

func TestResolveCommitReturnsTheFullHashOfAShortRevision(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	r := repo{root: f.root}
	got, err := r.resolveCommit(context.Background(), f.base[:7])
	if err != nil {
		t.Fatal(err)
	}
	if got != f.base {
		t.Errorf("resolveCommit(%q) = %q, want %q", f.base[:7], got, f.base)
	}
	for _, bad := range []string{"", "-n1", "no-such-ref", "HEAD:spec"} {
		if h, err := r.resolveCommit(context.Background(), bad); err == nil {
			t.Errorf("resolveCommit(%q) = %q; want an error", bad, h)
		}
	}
}
