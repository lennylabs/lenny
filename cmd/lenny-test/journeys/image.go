// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// AgentImageRepo is the repository of the journey agent's image.
const AgentImageRepo = "lenny-journeys-agent"

// agentImageFiles are the files of tests/journeys/ the image's build
// context holds, beside the lenny-ctl the harness builds.
var agentImageFiles = []string{"Dockerfile", "agent-entrypoint.sh"}

// ImageBuilder builds the journey agent's image. Run runs commands, so a
// test substitutes a fake for docker and go.
type ImageBuilder struct {
	RepoRoot string
	// Dir is a scratch directory for the build context.
	Dir string
	Run CommandRunner
	Log io.Writer
}

// Ensure builds lenny-ctl and the egress proxy for the Docker host's Linux
// architecture, assembles a build context of the Dockerfile, the
// entrypoint, and those binaries, builds the image unless an image with
// the context's digest tag exists, and returns the image's ID
// (sha256:...), which pins every container the run starts.
func (b *ImageBuilder) Ensure(ctx context.Context) (string, error) {
	arch, err := b.Run(ctx, nil, nil, b.RepoRoot, "docker", "version", "-f", "{{.Server.Arch}}")
	if err != nil {
		return "", fmt.Errorf("read the Docker host architecture: %w", err)
	}
	arch = strings.TrimSpace(arch)
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("the Docker host architecture %q is neither amd64 nor arm64", arch)
	}
	ctxDir := filepath.Join(b.Dir, "agent-image")
	if err := os.MkdirAll(ctxDir, 0o700); err != nil {
		return "", err
	}
	env := []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=" + arch, "GOFLAGS=-buildvcs=false"}
	for _, bin := range agentImageBinaries {
		if _, err := b.Run(ctx, b.Log, env, b.RepoRoot, "go", "build", "-trimpath", "-o", filepath.Join(ctxDir, bin.name), bin.pkg); err != nil {
			return "", fmt.Errorf("build %s for linux/%s: %w", bin.name, arch, err)
		}
	}
	h := sha256.New()
	for _, f := range agentImageFiles {
		if err := copyFile(filepath.Join(b.RepoRoot, "tests", "journeys", f), filepath.Join(ctxDir, f)); err != nil {
			return "", err
		}
	}
	if err := os.Chmod(filepath.Join(ctxDir, "agent-entrypoint.sh"), 0o755); err != nil {
		return "", err
	}
	names := append([]string{}, agentImageFiles...)
	for _, bin := range agentImageBinaries {
		names = append(names, bin.name)
	}
	for _, f := range names {
		data, err := os.ReadFile(filepath.Join(ctxDir, f))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s %d\n", f, len(data))
		h.Write(data)
	}
	fmt.Fprintf(h, "arch %s\n", arch)
	ref := AgentImageRepo + ":" + hex.EncodeToString(h.Sum(nil))[:12]
	if _, err := b.Run(ctx, nil, nil, b.RepoRoot, "docker", "image", "inspect", "--format", "{{.Id}}", ref); err != nil {
		if _, err := b.Run(ctx, b.Log, nil, ctxDir, "docker", "build", "--platform", "linux/"+arch, "-t", ref, "."); err != nil {
			return "", fmt.Errorf("build the agent image: %w", err)
		}
	}
	// Every container runs by the image's content-addressed ID, so a tag
	// moved after this point cannot change what runs.
	id, err := b.Run(ctx, nil, nil, b.RepoRoot, "docker", "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return "", fmt.Errorf("read the agent image ID: %w", err)
	}
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, "sha256:") {
		return "", fmt.Errorf("the agent image %s has ID %q, not a sha256 digest", ref, id)
	}
	return id, nil
}

// agentImageBinaries are the tree's binaries the image holds.
var agentImageBinaries = []struct{ name, pkg string }{
	{"lenny-ctl", "./cmd/lenny-ctl"},
	{"journey-egress-proxy", "./cmd/lenny-test/journeys/egress/proxy"},
}
