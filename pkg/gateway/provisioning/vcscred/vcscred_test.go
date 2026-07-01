// SPDX-License-Identifier: MIT

package vcscred

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
)

// fakePools serves a fixed pool set, ignoring the tenant filter.
type fakePools struct {
	pools []credentialpoolstore.CredentialPool
	err   error
}

func (f fakePools) List(_ context.Context, _ string, _ credentialpoolstore.ListFilter) ([]credentialpoolstore.CredentialPool, error) {
	return f.pools, f.err
}

// fakeSecrets resolves a ref to a fixed value or error.
type fakeSecrets struct {
	byRef map[string]string
	err   error
}

func (f fakeSecrets) Resolve(_ context.Context, ref string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.byRef[ref], nil
}

func githubPool() credentialpoolstore.CredentialPool {
	return credentialpoolstore.CredentialPool{
		Name:         "gh",
		Provider:     "github",
		HostPatterns: []string{"github.com"},
		Credentials: []credentialpoolstore.Credential{
			{ID: "c1", SecretRef: "lenny-system/gh-token"},
		},
	}
}

// spec: §14 line 95 — a public clone (no leaseScope) resolves to a zero
// credential without consulting the pool store.
func TestResolvePublicSourceIsZeroCredential(t *testing.T) {
	r := &StoreResolver{Pools: fakePools{err: errors.New("must not be called")}, Secrets: fakeSecrets{}}
	cred, err := r.Resolve(context.Background(), "acme", "https://github.com/acme/repo.git", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !cred.IsZero() {
		t.Errorf("public source resolved to %+v, want zero credential", cred)
	}
}

// spec: §14 line 95 / §4.9 — an authenticated source binds the URL host
// to the matching VCS pool and returns the token from the credential's
// Secret, paired with the GitHub-compatible username.
func TestResolveAuthenticatedReturnsToken(t *testing.T) {
	r := &StoreResolver{
		Pools:   fakePools{pools: []credentialpoolstore.CredentialPool{githubPool()}},
		Secrets: fakeSecrets{byRef: map[string]string{"lenny-system/gh-token": "ghs_abc123"}},
	}
	cred, err := r.Resolve(context.Background(), "acme", "https://github.com/acme/repo.git", "vcs.github.read")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Token != "ghs_abc123" {
		t.Errorf("Token = %q, want ghs_abc123", cred.Token)
	}
	if cred.Username != GitHubTokenUsername {
		t.Errorf("Username = %q, want %q", cred.Username, GitHubTokenUsername)
	}
}

// spec: §14 — an unmatched host surfaces the credentialpoolstore binding
// error so the §15.1 GIT_CLONE_AUTH_UNSUPPORTED_HOST code can be derived.
func TestResolveUnsupportedHost(t *testing.T) {
	r := &StoreResolver{
		Pools:   fakePools{pools: []credentialpoolstore.CredentialPool{githubPool()}},
		Secrets: fakeSecrets{},
	}
	_, err := r.Resolve(context.Background(), "acme", "https://gitlab.com/acme/repo.git", "vcs.github.read")
	var ve *credentialpoolstore.VCSResolveError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %v, want *VCSResolveError", err)
	}
	if ve.Reason != credentialpoolstore.VCSHostUnsupported {
		t.Errorf("Reason = %q, want unsupported_host", ve.Reason)
	}
}

// spec: §4.9 — a pool whose only credential is revoked (or carries no
// SecretRef) has no usable credential.
func TestResolveNoUsableCredential(t *testing.T) {
	pool := githubPool()
	pool.Credentials[0].Status = credentialpoolstore.CredentialRevoked
	r := &StoreResolver{
		Pools:   fakePools{pools: []credentialpoolstore.CredentialPool{pool}},
		Secrets: fakeSecrets{byRef: map[string]string{"lenny-system/gh-token": "ghs_abc123"}},
	}
	_, err := r.Resolve(context.Background(), "acme", "https://github.com/acme/repo.git", "vcs.github.read")
	if !errors.Is(err, ErrNoUsableCredential) {
		t.Fatalf("error = %v, want ErrNoUsableCredential", err)
	}
}

// spec: §4.9 — a pool whose Secret resolves empty is treated as having
// no usable credential, not as an empty (header-less) token.
func TestResolveEmptySecretIsNoUsableCredential(t *testing.T) {
	r := &StoreResolver{
		Pools:   fakePools{pools: []credentialpoolstore.CredentialPool{githubPool()}},
		Secrets: fakeSecrets{byRef: map[string]string{"lenny-system/gh-token": "   "}},
	}
	_, err := r.Resolve(context.Background(), "acme", "https://github.com/acme/repo.git", "vcs.github.read")
	if !errors.Is(err, ErrNoUsableCredential) {
		t.Fatalf("error = %v, want ErrNoUsableCredential for an empty secret", err)
	}
}

// A Secret read failure is wrapped verbatim so the create path can fail
// with the underlying cause.
func TestResolveSecretReadError(t *testing.T) {
	sentinel := errors.New("secret unreadable")
	r := &StoreResolver{
		Pools:   fakePools{pools: []credentialpoolstore.CredentialPool{githubPool()}},
		Secrets: fakeSecrets{err: sentinel},
	}
	_, err := r.Resolve(context.Background(), "acme", "https://github.com/acme/repo.git", "vcs.github.read")
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to wrap the secret-read failure", err)
	}
}

func TestResolveMalformedLeaseScope(t *testing.T) {
	r := &StoreResolver{Pools: fakePools{}, Secrets: fakeSecrets{}}
	if _, err := r.Resolve(context.Background(), "acme", "https://github.com/acme/repo.git", "not-a-scope"); err == nil {
		t.Fatal("Resolve accepted a malformed leaseScope")
	}
}

func TestResolveURLWithoutHost(t *testing.T) {
	r := &StoreResolver{Pools: fakePools{}, Secrets: fakeSecrets{}}
	if _, err := r.Resolve(context.Background(), "acme", "::::", "vcs.github.read"); err == nil {
		t.Fatal("Resolve accepted a URL with no host")
	}
}

// pickActiveCredential skips a revoked or secret-less credential and
// selects the first usable one.
func TestPickActiveCredentialSkipsUnusable(t *testing.T) {
	pool := credentialpoolstore.CredentialPool{
		Credentials: []credentialpoolstore.Credential{
			{ID: "revoked", SecretRef: "ns/a", Status: credentialpoolstore.CredentialRevoked},
			{ID: "no-ref"},
			{ID: "good", SecretRef: "ns/b"},
		},
	}
	c, ok := pickActiveCredential(pool)
	if !ok || c.ID != "good" {
		t.Fatalf("pickActiveCredential = (%+v, %v), want the 'good' credential", c, ok)
	}
}
