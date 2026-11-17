// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise contributor tooling and carry no spec annotation.

type imageCommands struct {
	calls  []string
	envs   [][]string
	arch   string
	exists bool
}

func (f *imageCommands) run(_ context.Context, _ io.Writer, env []string, _, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	f.envs = append(f.envs, env)
	switch {
	case strings.HasPrefix(call, "docker version"):
		return f.arch + "\n", nil
	case strings.HasPrefix(call, "go build"):
		out := args[slicesIndex(args, "-o")+1]
		return "", os.WriteFile(out, []byte("lenny-ctl binary"), 0o755)
	case strings.HasPrefix(call, "docker image inspect"):
		if f.exists {
			return "sha256:1\n", nil
		}
		return "", errors.New("no such image")
	case strings.HasPrefix(call, "docker build"):
		f.exists = true
	}
	return "", nil
}

func slicesIndex(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func TestImageBuilderBuildsOnceForTheContextDigest(t *testing.T) {
	f := &imageCommands{arch: "arm64"}
	b := &ImageBuilder{RepoRoot: filepath.Join("..", "..", ".."), Dir: t.TempDir(), Run: f.run, Log: io.Discard}
	ref, err := b.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ref != "sha256:1" {
		t.Errorf("the image is not pinned by its ID: %q", ref)
	}
	joined := strings.Join(f.calls, "\n")
	if !strings.Contains(joined, "docker build --platform linux/arm64 -t "+AgentImageRepo+":") {
		t.Errorf("no build:\n%s", joined)
	}
	if !strings.Contains(joined, "go build -trimpath -o "+filepath.Join(b.Dir, "agent-image", "journey-egress-proxy")+" ./cmd/lenny-test/journeys/egress/proxy") {
		t.Errorf("the egress proxy was not built:\n%s", joined)
	}
	if env := strings.Join(f.envs[1], " "); !strings.Contains(env, "GOOS=linux") || !strings.Contains(env, "GOARCH=arm64") || !strings.Contains(env, "CGO_ENABLED=0") {
		t.Errorf("lenny-ctl is not cross-compiled for the Docker host: %s", env)
	}
	ctxDir := filepath.Join(b.Dir, "agent-image")
	for _, name := range []string{"Dockerfile", "agent-entrypoint.sh", "lenny-ctl", "journey-egress-proxy"} {
		if _, err := os.Stat(filepath.Join(ctxDir, name)); err != nil {
			t.Errorf("the build context lacks %s: %v", name, err)
		}
	}
	entries, _ := os.ReadDir(ctxDir)
	if len(entries) != 4 {
		t.Errorf("the build context holds %d files, want only the files the image needs", len(entries))
	}

	f2 := &imageCommands{arch: "arm64", exists: true}
	b.Run = f2.run
	ref2, err := b.Ensure(context.Background())
	if err != nil || ref2 != ref {
		t.Fatalf("second reference = %q, %v; want %q", ref2, err, ref)
	}
	if strings.Contains(strings.Join(f2.calls, "\n"), "docker build") {
		t.Error("an existing image was rebuilt")
	}
	f3 := &imageCommands{arch: "arm64"}
	b.Run = f3.run
	b.RepoRoot = t.TempDir()
	if _, err := b.Ensure(context.Background()); err == nil {
		t.Error("a checkout without tests/journeys/Dockerfile built an image")
	}
	b.Run = (&imageCommands{arch: "s390x"}).run
	if _, err := b.Ensure(context.Background()); err == nil {
		t.Error("an unsupported Docker host architecture was accepted")
	}
}

// TestAgentDockerfilePinsItsInputs checks that the agent image pins its
// base by digest and every download by checksum, as the gate image does.
func TestAgentDockerfilePinsItsInputs(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "journeys", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	df := string(b)
	for _, line := range strings.Split(df, "\n") {
		if strings.HasPrefix(line, "FROM ") && !strings.Contains(line, "@sha256:") {
			t.Errorf("base image not pinned by digest: %s", line)
		}
	}
	if got, want := strings.Count(df, "sha256sum -c -"), 3; got != want {
		t.Errorf("%d checksum checks, want one per download (claude, kubectl, helm)", got)
	}
	for _, want := range []string{"CLAUDE_CODE_VERSION=2.1.326", "DISABLE_AUTOUPDATER=1", "DISABLE_UPDATES=1", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "COPY lenny-ctl journey-egress-proxy"} {
		if !strings.Contains(df, want) {
			t.Errorf("the Dockerfile lacks %q", want)
		}
	}
	for _, banned := range []string{"docker.sock", "/root/.kube", "ANTHROPIC_API_KEY"} {
		if strings.Contains(df, banned) {
			t.Errorf("the Dockerfile names %q", banned)
		}
	}
}
