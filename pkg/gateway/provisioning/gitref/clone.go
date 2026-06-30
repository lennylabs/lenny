// SPDX-License-Identifier: MIT

package gitref

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// DefaultCloneTimeout bounds a single Clone.
const DefaultCloneTimeout = 5 * time.Minute

// CloneOptions configures a Clone.
type CloneOptions struct {
	// Depth, when positive, fetches only that many commits — the §14
	// gitClone.depth shallow-clone setting.
	Depth int
	// Submodules, when true, initializes and updates submodules
	// recursively — the §14 gitClone.submodules setting.
	Submodules bool
	// GitPath is the git executable; the empty string resolves "git".
	GitPath string
	// Timeout bounds the whole clone; zero selects DefaultCloneTimeout.
	Timeout time.Duration
	// Credential is the §14 VCS credential injected into the fetch when
	// the gitClone source carries an auth block. The zero value clones
	// unauthenticated (a public repository).
	Credential Credential
}

// Clone materializes a Git repository at a pinned commit into destDir.
// It fetches commitSHA — the §14 resolvedCommitSha pinned at session
// creation — and checks it out detached, so the workspace tree is the
// session-immutable snapshot regardless of any branch movement after
// pinning. commitSHA must be a 40-character lowercase hex SHA.
//
// The §14 gitClone runs on the gateway's network path, not the pod's,
// so the runtime never sees raw credentials. A private repository clones
// when opts.Credential carries a §4.9 VCS token, injected as an HTTP
// Authorization header through the process environment so it never lands
// in argv or in the archived .git/config. Interactive credential prompts
// are disabled, so a private repository with no credential fails fast.
func Clone(ctx context.Context, url, commitSHA, destDir string, opts CloneOptions) error {
	if !workspaceplan.IsCommitSHA(commitSHA) {
		return fmt.Errorf("gitref: Clone requires a pinned 40-char commit SHA, got %q", commitSHA)
	}
	gitPath := opts.GitPath
	if gitPath == "" {
		gitPath = "git"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultCloneTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// §14 line 95: the authenticated fetch carries the credential in the
	// process environment, so the token never lands in argv or in the
	// archived .git/config. init and checkout need no remote contact, so
	// they run with the no-prompt base environment only.
	env := authEnv(url, opts.Credential)
	run := func(authenticated bool, args ...string) error {
		cmd := exec.CommandContext(ctx, gitPath, args...)
		if authenticated {
			cmd.Env = env
		} else {
			cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("gitref: git %v: %w (%s)", args, err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	if err := run(false, "init", "--quiet", destDir); err != nil {
		return err
	}
	// `--` separates the option list from the URL and SHA so neither can
	// be read as a git flag. Fetching the pinned SHA directly avoids a
	// branch advertisement and lands exactly the pinned commit.
	fetch := []string{"-C", destDir, "fetch", "--quiet"}
	if opts.Depth > 0 {
		fetch = append(fetch, "--depth", strconv.Itoa(opts.Depth))
	}
	fetch = append(fetch, "--", url, commitSHA)
	if err := run(true, fetch...); err != nil {
		return err
	}
	if err := run(false, "-C", destDir, "checkout", "--quiet", "--detach", "FETCH_HEAD"); err != nil {
		return err
	}
	if opts.Submodules {
		// Submodule update contacts each submodule's remote, so it runs
		// with the credential too; a submodule on the same host inherits
		// the host-scoped Authorization header.
		if err := run(true, "-C", destDir, "submodule", "update", "--init", "--recursive", "--quiet"); err != nil {
			return err
		}
	}
	return nil
}
