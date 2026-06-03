// SPDX-License-Identifier: MIT

// Package gitref implements the §14 gitClone ref-resolution backend:
// an LsRemoteResolver that resolves a gitClone source's ref to an
// immutable commit SHA by running `git ls-remote` against the remote.
// It implements workspaceplan.RefResolver.
//
// A public repository resolves unauthenticated. A private repository
// resolves when the caller supplies the §4.9 VCS credential
// (workspaceplan.VCSCredential): the resolver injects it as an HTTP
// Authorization header through the process environment. A private
// repository with no credential fails fast with a non-retryable auth
// error because interactive prompts are disabled.
package gitref

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// DefaultTimeout bounds a single ls-remote invocation. A remote that
// does not respond within the window is classified as a transient
// network failure per §14.
const DefaultTimeout = 30 * time.Second

// Options configures an LsRemoteResolver. A zero field selects its
// default.
type Options struct {
	// GitPath is the git executable. The empty string resolves "git"
	// on PATH.
	GitPath string
	// Timeout overrides DefaultTimeout.
	Timeout time.Duration
}

// LsRemoteResolver resolves a §14 gitClone ref to a commit SHA via
// `git ls-remote`. It is stateless and goroutine-safe.
type LsRemoteResolver struct {
	gitPath string
	timeout time.Duration
}

// NewLsRemoteResolver returns a resolver configured by opts.
func NewLsRemoteResolver(opts Options) *LsRemoteResolver {
	r := &LsRemoteResolver{gitPath: opts.GitPath, timeout: opts.Timeout}
	if r.gitPath == "" {
		r.gitPath = "git"
	}
	if r.timeout <= 0 {
		r.timeout = DefaultTimeout
	}
	return r
}

// Resolve runs `git ls-remote` for src and returns the commit SHA its
// ref points to. When cred carries a token the ls-remote authenticates
// with it (the §14 line 102 "same credential-lease as the clone")
// injected through the process environment, so a private repository
// resolves; a zero cred resolves a public repository. Interactive
// credential prompts are disabled, so a private repository with no
// credential fails fast rather than blocking. A failure is returned as a
// *workspaceplan.ResolveError classified per §14.
func (r *LsRemoteResolver) Resolve(ctx context.Context, src workspaceplan.GitClone, cred workspaceplan.VCSCredential) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	// `--` separates the option list from the URL so a URL cannot be
	// interpreted as a git flag; it is passed as a literal argv entry,
	// so there is no shell to inject into. The ref is matched locally
	// from the full ref advertisement rather than passed as a filter:
	// a ref-pattern argument suppresses the peeled "^{}" lines that an
	// annotated tag needs to resolve to its commit.
	cmd := exec.CommandContext(ctx, r.gitPath, "ls-remote", "--", src.URL)
	// authEnv disables interactive prompts and, when cred is non-zero,
	// adds the host-scoped Authorization header without writing the
	// token to argv.
	cmd.Env = authEnv(src.URL, Credential{Username: cred.Username, Token: cred.Token})
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", &workspaceplan.ResolveError{
			URL:    src.URL,
			Ref:    src.Ref,
			Reason: classify(ctx.Err(), stderr.String()),
			Err:    err,
		}
	}

	sha, ok := pickSHA(stdout.String(), src.Ref)
	if !ok {
		// ls-remote exited cleanly but matched no ref: the ref does not
		// exist on the remote.
		return "", &workspaceplan.ResolveError{
			URL:    src.URL,
			Ref:    src.Ref,
			Reason: workspaceplan.ResolveRefNotFound,
			Err:    errors.New("ref matched no branch or tag on the remote"),
		}
	}
	return sha, nil
}

// classify maps an ls-remote failure to a §14 ResolveErrorReason. A
// context deadline and an unrecognized failure are treated as
// transient (retryable); a credential rejection is permanent.
func classify(ctxErr error, stderr string) workspaceplan.ResolveErrorReason {
	if ctxErr == context.DeadlineExceeded {
		return workspaceplan.ResolveNetworkError
	}
	s := strings.ToLower(stderr)
	for _, marker := range []string{
		"authentication failed", "could not read username", "could not read password",
		"terminal prompts disabled", "permission denied", "403 forbidden", "invalid credentials",
	} {
		if strings.Contains(s, marker) {
			return workspaceplan.ResolveAuthFailed
		}
	}
	// Connection-level failures and anything unrecognized are transient:
	// retrying a genuinely permanent failure simply fails again, while a
	// transient one may succeed.
	return workspaceplan.ResolveNetworkError
}

// pickSHA selects the commit SHA for ref from full `git ls-remote`
// output. Output lines are "<sha>\t<refname>". A branch wins over a
// tag of the same short name, an annotated tag resolves to its peeled
// commit, and a fully-qualified ref matches by exact name.
func pickSHA(output, ref string) (string, bool) {
	byName := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sha, name, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		byName[name] = sha
	}
	// Precedence: branch, peeled annotated tag (commit), lightweight tag,
	// then an exact fully-qualified ref match.
	for _, candidate := range []string{
		"refs/heads/" + ref,
		"refs/tags/" + ref + "^{}",
		"refs/tags/" + ref,
		ref,
	} {
		if sha, ok := byName[candidate]; ok {
			return sha, true
		}
	}
	return "", false
}
