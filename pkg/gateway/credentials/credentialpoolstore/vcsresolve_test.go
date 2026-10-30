// SPDX-License-Identifier: MIT

package credentialpoolstore_test

import (
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
)

func vcsPool(name, provider string, patterns ...string) credentialpoolstore.CredentialPool {
	return credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: name, Provider: provider, HostPatterns: patterns,
	}
}

func TestResolveVCSPoolExactHost(t *testing.T) {
	pools := []credentialpoolstore.CredentialPool{
		vcsPool("gh", "github", "github.com"),
		vcsPool("gl", "gitlab", "gitlab.com"),
	}
	got, err := credentialpoolstore.ResolveVCSPool(pools, "github", "github.com")
	if err != nil {
		t.Fatalf("ResolveVCSPool: %v", err)
	}
	if got.Name != "gh" {
		t.Errorf("resolved pool = %q, want gh", got.Name)
	}
}

func TestResolveVCSPoolWildcardSubdomain(t *testing.T) {
	pools := []credentialpoolstore.CredentialPool{
		vcsPool("ent", "github", "*.github.example.com"),
	}
	for _, host := range []string{"git.github.example.com", "a.b.github.example.com"} {
		got, err := credentialpoolstore.ResolveVCSPool(pools, "github", host)
		if err != nil {
			t.Fatalf("ResolveVCSPool(%q): %v", host, err)
		}
		if got.Name != "ent" {
			t.Errorf("host %q resolved to %q, want ent", host, got.Name)
		}
	}
}

func TestResolveVCSPoolWildcardDoesNotMatchApex(t *testing.T) {
	pools := []credentialpoolstore.CredentialPool{
		vcsPool("ent", "github", "*.github.example.com"),
	}
	// The apex github.example.com is not a subdomain of itself.
	_, err := credentialpoolstore.ResolveVCSPool(pools, "github", "github.example.com")
	var re *credentialpoolstore.VCSResolveError
	if !errors.As(err, &re) || re.Reason != credentialpoolstore.VCSHostUnsupported {
		t.Errorf("apex host: err = %v, want VCSHostUnsupported", err)
	}
}

func TestResolveVCSPoolUnsupportedHost(t *testing.T) {
	pools := []credentialpoolstore.CredentialPool{vcsPool("gh", "github", "github.com")}
	_, err := credentialpoolstore.ResolveVCSPool(pools, "github", "git.acme.internal")
	var re *credentialpoolstore.VCSResolveError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want *VCSResolveError", err)
	}
	if re.Reason != credentialpoolstore.VCSHostUnsupported {
		t.Errorf("reason = %q, want unsupported_host", re.Reason)
	}
}

func TestResolveVCSPoolAmbiguousHost(t *testing.T) {
	pools := []credentialpoolstore.CredentialPool{
		vcsPool("gh-a", "github", "github.com"),
		vcsPool("gh-b", "github", "*.com"),
	}
	_, err := credentialpoolstore.ResolveVCSPool(pools, "github", "github.com")
	var re *credentialpoolstore.VCSResolveError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want *VCSResolveError", err)
	}
	if re.Reason != credentialpoolstore.VCSHostAmbiguous {
		t.Fatalf("reason = %q, want host_ambiguous", re.Reason)
	}
	if len(re.MatchingPools) != 2 {
		t.Errorf("MatchingPools = %v, want both gh-a and gh-b", re.MatchingPools)
	}
}

func TestResolveVCSPoolFiltersByProvider(t *testing.T) {
	// A pool whose host pattern matches but whose provider differs is
	// not a candidate — the leaseScope provider must match.
	pools := []credentialpoolstore.CredentialPool{
		vcsPool("gitlab-pool", "gitlab", "git.acme.com"),
		vcsPool("github-pool", "github", "git.acme.com"),
	}
	got, err := credentialpoolstore.ResolveVCSPool(pools, "github", "git.acme.com")
	if err != nil {
		t.Fatalf("ResolveVCSPool: %v", err)
	}
	if got.Name != "github-pool" {
		t.Errorf("resolved %q, want github-pool — the gitlab pool must be filtered out", got.Name)
	}
}

func TestResolveVCSPoolIgnoresSoftDeletedPool(t *testing.T) {
	deleted := vcsPool("gh-old", "github", "github.com")
	deleted.DeletedAt = time.Now()
	pools := []credentialpoolstore.CredentialPool{deleted}
	_, err := credentialpoolstore.ResolveVCSPool(pools, "github", "github.com")
	var re *credentialpoolstore.VCSResolveError
	if !errors.As(err, &re) || re.Reason != credentialpoolstore.VCSHostUnsupported {
		t.Errorf("a soft-deleted pool must not match: err = %v", err)
	}
}

func TestResolveVCSPoolHostCaseInsensitive(t *testing.T) {
	pools := []credentialpoolstore.CredentialPool{vcsPool("gh", "github", "GitHub.com")}
	got, err := credentialpoolstore.ResolveVCSPool(pools, "github", "GITHUB.COM")
	if err != nil {
		t.Fatalf("ResolveVCSPool: %v", err)
	}
	if got.Name != "gh" {
		t.Errorf("case-insensitive match failed: resolved %q", got.Name)
	}
}
