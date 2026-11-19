// SPDX-License-Identifier: MIT

package gitref

import (
	"encoding/base64"
	"net/url"
	"os"
	"strings"
)

// Credential is a §14 VCS credential the gateway injects into a git
// invocation. The gateway sends Token as an HTTP Basic Authorization
// header so a private repository's ls-remote and fetch authenticate; a
// zero Credential (empty Token) runs the invocation unauthenticated. The
// pod never sees the credential: the gateway clones on its own network
// path and the token is injected through the process environment, so it
// never lands in argv (visible via `ps`) and never persists into the
// archived `.git/config`.
type Credential struct {
	// Username is the HTTP Basic username paired with Token. Empty
	// defaults to `x-access-token`, the GitHub App-compatible value.
	Username string
	// Token is the short-lived VCS token. Empty disables injection.
	Token string
}

// authEnv returns the environment for a git invocation against repoURL.
// It always disables interactive credential prompts. When cred carries a
// token it adds an HTTP Authorization header scoped to repoURL's
// scheme+host via the GIT_CONFIG_COUNT/GIT_CONFIG_KEY_n/GIT_CONFIG_VALUE_n
// env protocol (git 2.31+), so the header is applied without writing it
// to argv or to any on-disk git config. Scoping to the host keeps the
// header off a cross-host redirect.
func authEnv(repoURL string, cred Credential) []string {
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if cred.Token == "" {
		return env
	}
	user := cred.Username
	if user == "" {
		user = "x-access-token"
	}
	header := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+cred.Token))
	key := "http." + authHeaderScope(repoURL) + ".extraHeader"
	return append(
		env,
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0="+key,
		"GIT_CONFIG_VALUE_0="+header,
	)
}

// authHeaderScope returns the `scheme://host/` URL prefix git matches the
// extraHeader config against, so the Authorization header is sent only to
// the credential's own host. A URL that does not parse falls back to an
// empty subsection, which applies the header to every http(s) request of
// the single-remote invocation.
func authHeaderScope(repoURL string) string {
	u, err := url.Parse(repoURL)
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + strings.ToLower(u.Host) + "/"
}
