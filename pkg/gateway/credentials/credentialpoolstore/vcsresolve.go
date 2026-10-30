// SPDX-License-Identifier: MIT

package credentialpoolstore

import (
	"fmt"
	"strings"
)

// VCSResolveErrorReason classifies a §14 gitClone-to-VCS-pool
// resolution failure.
type VCSResolveErrorReason string

const (
	// VCSHostUnsupported: the gitClone URL host matched no VCS
	// credential pool. The §15.1 code is GIT_CLONE_AUTH_UNSUPPORTED_HOST.
	VCSHostUnsupported VCSResolveErrorReason = "unsupported_host"
	// VCSHostAmbiguous: the gitClone URL host matched more than one VCS
	// credential pool. The §15.1 code is GIT_CLONE_AUTH_HOST_AMBIGUOUS.
	VCSHostAmbiguous VCSResolveErrorReason = "host_ambiguous"
)

// VCSResolveError is a §14 failure to bind a gitClone source's URL to
// exactly one VCS credential pool. The session handler maps it to the
// §15.1 GIT_CLONE_AUTH_UNSUPPORTED_HOST (422) or
// GIT_CLONE_AUTH_HOST_AMBIGUOUS (422) response; its fields supply the
// response details.
type VCSResolveError struct {
	Host     string
	Provider string
	Reason   VCSResolveErrorReason
	// MatchingPools names the pools that matched. It is populated only
	// for VCSHostAmbiguous.
	MatchingPools []string
}

func (e *VCSResolveError) Error() string {
	switch e.Reason {
	case VCSHostAmbiguous:
		return fmt.Sprintf("credentialpoolstore: gitClone host %q matches multiple %q VCS pools: %s",
			e.Host, e.Provider, strings.Join(e.MatchingPools, ", "))
	default:
		return fmt.Sprintf("credentialpoolstore: gitClone host %q matches no %q VCS pool",
			e.Host, e.Provider)
	}
}

// ResolveVCSPool binds a §14 gitClone source to a VCS credential pool.
// Among pools, it selects the active pools whose Provider equals
// provider and whose HostPatterns match host. Exactly one match
// resolves; zero is a *VCSResolveError with VCSHostUnsupported and more
// than one is a *VCSResolveError with VCSHostAmbiguous. provider is the
// `<provider>` segment of the gitClone.auth.leaseScope
// (`vcs.<provider>.read|write`); host is the lowercased authority of
// the parsed HTTPS gitClone URL.
func ResolveVCSPool(pools []CredentialPool, provider, host string) (CredentialPool, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	var matches []CredentialPool
	for _, p := range pools {
		if !p.IsActive() || p.Provider != provider {
			continue
		}
		if hostMatchesPatterns(host, p.HostPatterns) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return CredentialPool{}, &VCSResolveError{
			Host: host, Provider: provider, Reason: VCSHostUnsupported,
		}
	default:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.Name
		}
		return CredentialPool{}, &VCSResolveError{
			Host: host, Provider: provider, Reason: VCSHostAmbiguous, MatchingPools: names,
		}
	}
}

// hostMatchesPatterns reports whether host matches one of the §4.9
// VCS-pool host patterns. A pattern is either an exact host or a
// `*.suffix` wildcard that matches any proper subdomain of suffix
// (`*.example.com` matches `a.example.com` and `a.b.example.com` but
// not `example.com` itself). Matching is case-insensitive.
func hostMatchesPatterns(host string, patterns []string) bool {
	for _, pat := range patterns {
		pat = strings.ToLower(strings.TrimSpace(pat))
		if pat == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(pat, "*."); ok {
			suffix := "." + rest
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
			continue
		}
		if host == pat {
			return true
		}
	}
	return false
}
