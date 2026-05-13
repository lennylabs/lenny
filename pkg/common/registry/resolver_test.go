// SPDX-License-Identifier: MIT

package registry_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/common/registry"
)

// spec: 18.1 (ImageResolver precedence: override > url > default).
// diagnosis: An explicit override in Config.Overrides did not win over
//
//	the URL+name default. The override rung must always take
//	precedence, even when the override does not start with the
//	base URL.
func TestOverrideWinsOverDefault(t *testing.T) {
	t.Parallel()
	r := registry.New(registry.Config{
		URL: "ghcr.io/lennylabs",
		Overrides: map[string]string{
			"echo": "ghcr.io/mirror/echo:custom",
		},
	})
	got, err := r.Resolve("echo")
	if err != nil {
		t.Fatalf("Resolve(echo): %v", err)
	}
	if got != "ghcr.io/mirror/echo:custom" {
		t.Errorf("got %q, want %q", got, "ghcr.io/mirror/echo:custom")
	}
}

// spec: 18.1
// diagnosis: When no override is set, the default rung must combine
//
//	URL + "/" + short-name (and optional DefaultTag). A trailing
//	slash on URL must not produce a double slash.
func TestDefaultRungBuildsURLPlusName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  registry.Config
		in   string
		want string
	}{
		{"plain", registry.Config{URL: "ghcr.io/lennylabs"}, "echo", "ghcr.io/lennylabs/echo"},
		{"trailing-slash", registry.Config{URL: "ghcr.io/lennylabs/"}, "echo", "ghcr.io/lennylabs/echo"},
		{"with-default-tag", registry.Config{URL: "ghcr.io/lennylabs", DefaultTag: "v1.2.3"}, "echo", "ghcr.io/lennylabs/echo:v1.2.3"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := registry.New(c.cfg)
			got, err := r.Resolve(c.in)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// spec: 18.1 (digest enforcement)
// diagnosis: RequireDigest = true must reject any reference lacking
//
//	`@sha256:`. The error must wrap ErrDigestRequired so callers
//	can use errors.Is for branching.
func TestRequireDigestRejectsUnpinned(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  registry.Config
		in   string
	}{
		{
			"default-without-digest",
			registry.Config{URL: "ghcr.io/lennylabs", RequireDigest: true},
			"echo",
		},
		{
			"default-with-tag-only",
			registry.Config{URL: "ghcr.io/lennylabs", DefaultTag: "v1", RequireDigest: true},
			"echo",
		},
		{
			"override-without-digest",
			registry.Config{
				URL:           "ghcr.io/lennylabs",
				RequireDigest: true,
				Overrides:     map[string]string{"echo": "ghcr.io/mirror/echo:custom"},
			},
			"echo",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := registry.New(c.cfg)
			_, err := r.Resolve(c.in)
			if !errors.Is(err, registry.ErrDigestRequired) {
				t.Errorf("got err=%v, want ErrDigestRequired", err)
			}
		})
	}
}

// spec: 18.1
// diagnosis: RequireDigest = true must accept references that include
//
//	@sha256:, on either the override or the default rung.
func TestRequireDigestAcceptsPinned(t *testing.T) {
	t.Parallel()
	const digest = "@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	t.Run("default-rung", func(t *testing.T) {
		t.Parallel()
		r := registry.New(registry.Config{
			URL:           "ghcr.io/lennylabs",
			RequireDigest: true,
			Overrides: map[string]string{
				"echo": "ghcr.io/lennylabs/echo" + digest,
			},
		})
		got, err := r.Resolve("echo")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got != "ghcr.io/lennylabs/echo"+digest {
			t.Errorf("got %q", got)
		}
	})
}

// spec: 18.1
// diagnosis: An empty short name must be rejected with ErrEmptyName.
//
//	The resolver does not invent names from defaults.
func TestEmptyNameRejected(t *testing.T) {
	t.Parallel()
	r := registry.New(registry.Config{URL: "ghcr.io/lennylabs"})
	for _, s := range []string{"", "  ", "\t"} {
		s := s
		t.Run(s, func(t *testing.T) {
			t.Parallel()
			_, err := r.Resolve(s)
			if !errors.Is(err, registry.ErrEmptyName) {
				t.Errorf("got err=%v, want ErrEmptyName", err)
			}
		})
	}
}

// spec: 18.1
// diagnosis: When no override exists and Config.URL is empty, Resolve
//
//	must fail loudly with ErrNoBase. Producing a relative
//	reference would lead to confused upstream pulls.
func TestNoOverrideNoURLFails(t *testing.T) {
	t.Parallel()
	r := registry.New(registry.Config{})
	_, err := r.Resolve("echo")
	if !errors.Is(err, registry.ErrNoBase) {
		t.Errorf("got err=%v, want ErrNoBase", err)
	}
}

// spec: 18.1
// diagnosis: Mutating the Overrides map passed to New must not affect a
//
//	previously-constructed Resolver. New is expected to defensively
//	copy the map.
func TestNewDefensivelyCopiesOverrides(t *testing.T) {
	t.Parallel()
	overrides := map[string]string{"echo": "ghcr.io/mirror/echo:v1"}
	r := registry.New(registry.Config{
		URL:       "ghcr.io/lennylabs",
		Overrides: overrides,
	})
	overrides["echo"] = "ghcr.io/mutated/echo:v9"

	got, err := r.Resolve("echo")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "ghcr.io/mirror/echo:v1" {
		t.Errorf("got %q, expected the resolver to ignore post-construction mutation", got)
	}
}

// spec: 18.1
// diagnosis: PullSecretName must return the configured value verbatim.
//
//	Downstream consumers (controllers that attach pull secrets to
//	pod specs) depend on this contract.
func TestPullSecretNamePassThrough(t *testing.T) {
	t.Parallel()
	r := registry.New(registry.Config{
		URL:            "ghcr.io/lennylabs",
		PullSecretName: "ghcr-pull",
	})
	if got := r.PullSecretName(); got != "ghcr-pull" {
		t.Errorf("got %q, want ghcr-pull", got)
	}
}
