// SPDX-License-Identifier: MIT

package gitref

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"
)

// spec: §14 line 95 — a zero credential adds no Authorization header and
// only disables interactive prompts.
func TestAuthEnvPublicHasNoHeader(t *testing.T) {
	env := authEnv("https://github.com/acme/repo.git", Credential{})
	if !slices.Contains(env, "GIT_TERMINAL_PROMPT=0") {
		t.Error("authEnv must disable interactive prompts")
	}
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_CONFIG_COUNT=") {
			t.Errorf("a public clone must not set git config injection, got %q", e)
		}
	}
}

// spec: §14 line 95 — a token is injected as a host-scoped HTTP Basic
// Authorization header through the process environment, so it never
// appears in argv or in on-disk git config.
func TestAuthEnvInjectsScopedBasicHeader(t *testing.T) {
	env := authEnv("https://github.com/acme/repo.git", Credential{Username: "x-access-token", Token: "ghs_secret"})
	get := func(key string) (string, bool) {
		for _, e := range env {
			if v, ok := strings.CutPrefix(e, key+"="); ok {
				return v, true
			}
		}
		return "", false
	}
	if c, _ := get("GIT_CONFIG_COUNT"); c != "1" {
		t.Errorf("GIT_CONFIG_COUNT = %q, want 1", c)
	}
	key, ok := get("GIT_CONFIG_KEY_0")
	if !ok || key != "http.https://github.com/.extraHeader" {
		t.Errorf("GIT_CONFIG_KEY_0 = %q, want the host-scoped extraHeader key", key)
	}
	val, ok := get("GIT_CONFIG_VALUE_0")
	if !ok {
		t.Fatal("GIT_CONFIG_VALUE_0 not set")
	}
	wantHeader := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:ghs_secret"))
	if val != wantHeader {
		t.Errorf("GIT_CONFIG_VALUE_0 = %q, want %q", val, wantHeader)
	}
	// The token must never reach argv via the environment leaking into a
	// flag-shaped variable; the only place it lives is the VALUE entry.
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_CONFIG_VALUE_0=") {
			continue
		}
		if strings.Contains(e, "ghs_secret") {
			t.Errorf("token leaked into env entry %q", e)
		}
	}
}

// An empty username defaults to the GitHub App-compatible value.
func TestAuthEnvDefaultsUsername(t *testing.T) {
	env := authEnv("https://github.com/acme/repo.git", Credential{Token: "tok"})
	want := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:tok"))
	found := false
	for _, e := range env {
		if e == "GIT_CONFIG_VALUE_0="+want {
			found = true
		}
	}
	if !found {
		t.Errorf("env did not carry the default-username header %q", want)
	}
}

func TestAuthHeaderScope(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://github.com/acme/repo.git", "https://github.com/"},
		{"https://Git.ACME.com:8443/x", "https://git.acme.com:8443/"},
		{"git@github.com:acme/repo.git", ""}, // scp-form has no parseable host
		{"", ""},
	}
	for _, tc := range cases {
		if got := authHeaderScope(tc.url); got != tc.want {
			t.Errorf("authHeaderScope(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}
