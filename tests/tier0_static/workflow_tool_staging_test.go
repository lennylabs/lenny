// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The static tier runs a proto no-drift check that reproduces the
// `generate-proto` target, and that check reports an unverified result
// when any of buf, goimports, protoc-gen-go, or protoc-gen-go-grpc is
// absent from PATH. A tier whose status is not pass ends the run, so a
// job that runs the static tier without those binaries leaves the whole
// job red and verifies pkg/proto/ nowhere. This test holds every job
// that runs the static tier to installing the producer's binaries and
// putting the GOPATH bin directory on PATH.
//
// It also holds the shared ~/go/bin cache to a single writer. The job
// that installs the complete tool set is the only one that may save
// under the shared key: a job that installs a subset and saves would
// publish a directory missing gofumpt and golangci-lint, and the next
// job to take an exact hit on that key would skip its own install and
// run the static tier with both checks silently inert.
//
// spec: TESTING.md §20.6 (caching strategy — the ~/go/bin tool cache),
// TESTING.md §20.8 (PR pipeline), TESTING.md §20.11 (per-phase gate pipeline), and TESTING.md §20.16
// (action pinning discipline).

// protoProducerToolModules maps each binary the proto no-drift check
// needs to the module path a `go install` of it names.
var protoProducerToolModules = map[string]string{
	"buf":                "github.com/bufbuild/buf/cmd/buf",
	"goimports":          "golang.org/x/tools/cmd/goimports",
	"protoc-gen-go":      "google.golang.org/protobuf/cmd/protoc-gen-go",
	"protoc-gen-go-grpc": "google.golang.org/grpc/cmd/protoc-gen-go-grpc",
}

// lintToolModules are the binaries whose absence turns a static-tier
// check into a no-op. Only a job installing these may write the shared
// cache key.
var lintToolModules = map[string]string{
	"gofumpt":       "mvdan.cc/gofumpt",
	"golangci-lint": "github.com/golangci/golangci-lint/v2/cmd/golangci-lint",
}

// sharedToolCacheKey matches the primary key of the ~/go/bin cache the
// jobs share. A key carrying a further suffix addresses a different
// namespace and is out of scope for the single-writer rule.
var sharedToolCacheKey = regexp.MustCompile(`^lenny-go-bin-v\d+-\$\{\{ runner\.os \}\}-\$\{\{ hashFiles\('go\.sum'\) \}\}$`)

// toolCacheKeyVersion captures the version marker of any ~/go/bin cache
// key or restore-key prefix, wherever it appears in a workflow file.
var toolCacheKeyVersion = regexp.MustCompile(`lenny-go-bin-(v\d+)-`)

// staticTierInvocation matches a harness invocation whose plan opens
// with the static tier. Every named group's plan starts there, and the
// direct form names the tier itself.
var staticTierInvocation = regexp.MustCompile(`lenny-test\s+--(tier\s+static|group\s)`)

// ghWorkflow is the subset of the GitHub Actions workflow schema these
// cases read.
type ghWorkflow struct {
	Jobs map[string]ghJob `yaml:"jobs"`
}

type ghJob struct {
	Steps []ghStep `yaml:"steps"`
}

type ghStep struct {
	Name string     `yaml:"name"`
	Uses string     `yaml:"uses"`
	Run  string     `yaml:"run"`
	With ghStepWith `yaml:"with"`
}

type ghStepWith struct {
	Path        string `yaml:"path"`
	Key         string `yaml:"key"`
	RestoreKeys string `yaml:"restore-keys"`
}

// jobRef names one job inside one workflow file.
type jobRef struct {
	file string
	job  string
}

func (r jobRef) String() string { return r.file + ":" + r.job }

// loadWorkflows parses every workflow file under .github/workflows/,
// including the reusable ones, keyed by its repository-relative path.
func loadWorkflows(t *testing.T) map[string]ghWorkflow {
	t.Helper()
	root := schematest.RepoRoot(t)
	dir := filepath.Join(root, ".github", "workflows")
	out := map[string]ghWorkflow{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".yml" {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		var wf ghWorkflow
		if unmarshalErr := yaml.Unmarshal(raw, &wf); unmarshalErr != nil {
			return unmarshalErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out[filepath.ToSlash(rel)] = wf
		return nil
	})
	if err != nil {
		t.Fatalf("load workflows: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no workflow files found under .github/workflows/")
	}
	return out
}

// jobScript joins every run: script in a job so a module install can be
// found wherever in the job it sits.
func jobScript(job ghJob) string {
	var b strings.Builder
	for _, step := range job.Steps {
		b.WriteString(step.Run)
		b.WriteString("\n")
	}
	return b.String()
}

// runsStaticTier reports whether any step in the job invokes the
// harness with a plan that opens with the static tier.
func runsStaticTier(job ghJob) bool {
	for _, step := range job.Steps {
		if staticTierInvocation.MatchString(step.Run) {
			return true
		}
	}
	return false
}

// installsGoBinary reports whether the job's scripts carry a
// `go install` of the named module at a pinned version.
func installsGoBinary(job ghJob, module string) bool {
	return strings.Contains(jobScript(job), "go install "+module+"@")
}

// addsGoPathBinToPATH reports whether the job exports the GOPATH bin
// directory into the step PATH, which is where the installs land.
func addsGoPathBinToPATH(job ghJob) bool {
	script := jobScript(job)
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(line, "go env GOPATH") && strings.Contains(line, "GITHUB_PATH") {
			return true
		}
	}
	return false
}

// staticTierJobs returns every job that runs the static tier, sorted so
// a failure reports in a stable order.
func staticTierJobs(t *testing.T, workflows map[string]ghWorkflow) []jobRef {
	t.Helper()
	var refs []jobRef
	for file, wf := range workflows {
		for name, job := range wf.Jobs {
			if runsStaticTier(job) {
				refs = append(refs, jobRef{file: file, job: name})
			}
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
	return refs
}

func TestStaticTierJobsInstallTheProtoProducerBinaries(t *testing.T) {
	workflows := loadWorkflows(t)
	refs := staticTierJobs(t, workflows)
	if len(refs) == 0 {
		t.Fatal("no job runs the static tier; the detection here has gone stale")
	}
	binaries := make([]string, 0, len(protoProducerToolModules))
	for name := range protoProducerToolModules {
		binaries = append(binaries, name)
	}
	sort.Strings(binaries)

	for _, ref := range refs {
		job := workflows[ref.file].Jobs[ref.job]
		for _, binary := range binaries {
			if !installsGoBinary(job, protoProducerToolModules[binary]) {
				t.Errorf("%s runs the static tier without installing %s (%s); the proto no-drift check reports unverified there and ends the run",
					ref, binary, protoProducerToolModules[binary])
			}
		}
		if !addsGoPathBinToPATH(job) {
			t.Errorf("%s installs the proto producer's binaries without adding $(go env GOPATH)/bin to PATH, so buf resolves neither local plugin", ref)
		}
	}
}

// cacheStep pairs a ~/go/bin cache step with the job that carries it.
type cacheStep struct {
	ref  jobRef
	step ghStep
}

// sharedToolCacheSteps returns every step addressing the shared ~/go/bin
// cache key, in a stable order.
func sharedToolCacheSteps(workflows map[string]ghWorkflow) []cacheStep {
	var steps []cacheStep
	for file, wf := range workflows {
		for name, job := range wf.Jobs {
			for _, step := range job.Steps {
				if !strings.Contains(step.With.Path, "~/go/bin") {
					continue
				}
				if !sharedToolCacheKey.MatchString(strings.TrimSpace(step.With.Key)) {
					continue
				}
				steps = append(steps, cacheStep{ref: jobRef{file: file, job: name}, step: step})
			}
		}
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].ref.String() < steps[j].ref.String() })
	return steps
}

func TestOnlyACompleteToolInstallWritesTheSharedToolCache(t *testing.T) {
	workflows := loadWorkflows(t)
	steps := sharedToolCacheSteps(workflows)
	if len(steps) == 0 {
		t.Fatal("no step restores the shared ~/go/bin cache key; the detection here has gone stale")
	}

	writers := 0
	for _, cs := range steps {
		job := workflows[cs.ref.file].Jobs[cs.ref.job]
		switch {
		case strings.HasPrefix(cs.step.Uses, "actions/cache/restore@"):
			continue
		case strings.HasPrefix(cs.step.Uses, "actions/cache@"):
			writers++
			for binary, module := range lintToolModules {
				if !installsGoBinary(job, module) {
					t.Errorf("%s saves the shared ~/go/bin key without installing %s; a job taking an exact hit on that cache skips its own install and runs the static tier with the %s check inert",
						cs.ref, binary, binary)
				}
			}
			for binary, module := range protoProducerToolModules {
				if !installsGoBinary(job, module) {
					t.Errorf("%s saves the shared ~/go/bin key without installing %s; a job taking an exact hit on that cache runs the proto no-drift check against a missing binary",
						cs.ref, binary)
				}
			}
		default:
			t.Errorf("%s addresses the shared ~/go/bin key through %q; use actions/cache/restore to read it or actions/cache to write it", cs.ref, cs.step.Uses)
		}
	}
	if writers != 1 {
		t.Errorf("the shared ~/go/bin key has %d writers; exactly one job may save it so its contents stay complete", writers)
	}
}

func TestToolCacheKeyVersionIsUniformAcrossWorkflows(t *testing.T) {
	root := schematest.RepoRoot(t)
	dir := filepath.Join(root, ".github", "workflows")
	versions := map[string][]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".yml" {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		for _, m := range toolCacheKeyVersion.FindAllStringSubmatch(string(raw), -1) {
			versions[m[1]] = append(versions[m[1]], filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan workflows for tool cache keys: %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("no ~/go/bin cache key found; the detection here has gone stale")
	}
	if len(versions) > 1 {
		found := make([]string, 0, len(versions))
		for version, files := range versions {
			sort.Strings(files)
			found = append(found, version+" in "+strings.Join(files, ", "))
		}
		sort.Strings(found)
		t.Errorf("the ~/go/bin cache key carries more than one version marker (%s); a job reading a stale marker restores a directory the current install list does not describe",
			strings.Join(found, "; "))
	}
}
