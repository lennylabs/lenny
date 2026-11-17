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

// Ensure builds lenny-ctl for the Docker host's Linux architecture,
// assembles a build context of the Dockerfile, the entrypoint, and that
// binary, and builds the image unless an image with the context's digest
// tag exists. It returns the image reference.
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
	if _, err := b.Run(ctx, b.Log, env, b.RepoRoot, "go", "build", "-trimpath", "-o", filepath.Join(ctxDir, "lenny-ctl"), "./cmd/lenny-ctl"); err != nil {
		return "", fmt.Errorf("build lenny-ctl for linux/%s: %w", arch, err)
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
	for _, f := range append(agentImageFiles, "lenny-ctl") {
		data, err := os.ReadFile(filepath.Join(ctxDir, f))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s %d\n", f, len(data))
		h.Write(data)
	}
	fmt.Fprintf(h, "arch %s\n", arch)
	ref := AgentImageRepo + ":" + hex.EncodeToString(h.Sum(nil))[:12]
	if _, err := b.Run(ctx, nil, nil, b.RepoRoot, "docker", "image", "inspect", "--format", "{{.Id}}", ref); err == nil {
		return ref, nil
	}
	if _, err := b.Run(ctx, b.Log, nil, ctxDir, "docker", "build", "--platform", "linux/"+arch, "-t", ref, "."); err != nil {
		return "", fmt.Errorf("build the agent image: %w", err)
	}
	return ref, nil
}
