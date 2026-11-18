// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Setup levels name what the harness installs on the journey cluster
// before the agent starts.
const (
	// SetupPrerequisites gives the agent a Kind cluster with the Lenny
	// images loaded onto its nodes and the prerequisites the chart needs
	// (cert-manager, the prometheus-operator CRDs, ingress-nginx, and the
	// Postgres, Redis, and MinIO data stores with the schema migrated),
	// and no Lenny release. An operator journey that installs the chart
	// starts here.
	SetupPrerequisites = "prerequisites"
	// SetupLenny gives the agent a cluster with Lenny installed and the
	// echo reference runtime's warm pool running, as the e2e_kind tier
	// installs it.
	SetupLenny = "lenny"
	// SetupCluster is a bare Kind cluster with nothing installed. The
	// agent configuration check (--probe) uses it; no scenario does.
	SetupCluster = "cluster"
)

// AllowedCommands are the shell commands a journey agent may run. A
// scenario's tools list names a subset of them.
var AllowedCommands = []string{"kubectl", "helm", "lenny-ctl"}

// DefaultTimeLimit is the wall-clock limit of one agent run when the
// scenario names none.
const DefaultTimeLimit = 45 * time.Minute

// Scenario is one journey, parsed from a file under tests/journeys/.
type Scenario struct {
	// Name identifies the journey. It equals the file name without the
	// .yaml extension, and --journey selects it.
	Name string `yaml:"name"`
	// Persona names who the agent plays, such as operator,
	// tenant-admin, or end-user.
	Persona string `yaml:"persona"`
	// Summary is one sentence for the run summary.
	Summary string `yaml:"summary"`
	// Target names the cluster target the trial runs on. Empty means
	// TargetKind, the only target the tier has.
	Target string `yaml:"target"`
	// Setup is SetupPrerequisites or SetupLenny.
	Setup string `yaml:"setup"`
	// Credentials names what the agent receives in its environment,
	// from CredentialEnv. It receives nothing else: the harness builds
	// the agent's environment explicitly.
	Credentials []string `yaml:"credentials"`
	// Prepare lists preparation steps the harness runs against the
	// gateway after the setup and before the agent starts, for state a
	// person of the persona would find already in place, such as
	// "sessions-allowed:default". Only SetupLenny has a gateway to
	// prepare.
	Prepare []string `yaml:"prepare"`
	// Docs are the documentation pages the agent starts from, as paths
	// under docs/. The agent may also read the pages they link to,
	// which are under docs/ as well.
	Docs []string `yaml:"docs"`
	// Goal is what the persona wants, in the words a person of that
	// persona would use.
	Goal string `yaml:"goal"`
	// Steps are the steps the documentation gives, as hints the agent
	// need not follow literally.
	Steps []string `yaml:"steps"`
	// Environment states the facts about the environment a person of
	// the persona would know without the documentation, such as where
	// the images and the data stores are.
	Environment string `yaml:"environment"`
	// PublicValues are placeholder credentials the scenario publishes in
	// its goal, steps, or environment, such as the data-store passwords
	// of the environment or the placeholder key of the goal, written as
	// they appear there ({{nonce}} included). The records keep each
	// expanded value, compared exactly, where it is the value of a
	// credential key such as password, which the redactor otherwise
	// removes (redact_keys.go). A value the scenario's text does not
	// contain is an error, so the list cannot exempt a credential the
	// scenario does not publish.
	PublicValues []string `yaml:"publicValues"`
	// Tools are the shell commands the agent may run, a subset of
	// AllowedCommands.
	Tools []string `yaml:"tools"`
	// TimeLimit bounds one agent run. Zero means DefaultTimeLimit.
	TimeLimit Duration `yaml:"timeLimit"`
	// Postconditions are the checks the harness runs after the agent
	// finishes. Every one must hold for the trial to pass.
	Postconditions []Postcondition `yaml:"postconditions"`

	// Path is the file the scenario was read from.
	Path string `yaml:"-"`
}

// Duration is a time.Duration written in a scenario as a Go duration
// string such as "45m" or "90s".
type Duration time.Duration

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("line %d: a duration is a string such as \"45m\": %w", node.Line, err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	if v < 0 {
		return fmt.Errorf("line %d: duration %q is negative", node.Line, s)
	}
	*d = Duration(v)
	return nil
}

// TargetName returns the scenario's target, defaulting to TargetKind.
func (s *Scenario) TargetName() string {
	if s.Target == "" {
		return TargetKind
	}
	return s.Target
}

// Limit returns the scenario's time limit, or DefaultTimeLimit when the
// scenario names none.
func (s *Scenario) Limit() time.Duration {
	if s.TimeLimit == 0 {
		return DefaultTimeLimit
	}
	return time.Duration(s.TimeLimit)
}

var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ParseScenario parses one scenario file's content. Unknown fields are
// errors, so a misspelled key fails rather than being ignored.
func ParseScenario(content []byte, filePath string) (*Scenario, error) {
	dec := yaml.NewDecoder(bytes.NewReader(content))
	dec.KnownFields(true)
	var s Scenario
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", filePath, err)
	}
	s.Path = filePath
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", filePath, err)
	}
	return &s, nil
}

// Validate reports every structural error in the scenario together.
func (s *Scenario) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if !namePattern.MatchString(s.Name) {
		add("name %q is not lowercase words joined by hyphens", s.Name)
	}
	if s.Path != "" {
		if stem := strings.TrimSuffix(filepath.Base(s.Path), filepath.Ext(s.Path)); stem != s.Name {
			add("name %q differs from the file name %q", s.Name, stem)
		}
	}
	if strings.TrimSpace(s.Persona) == "" {
		add("persona is empty")
	}
	if s.Target != "" && !namePattern.MatchString(s.Target) {
		add("target %q is not lowercase words joined by hyphens", s.Target)
	}
	if s.Setup != SetupPrerequisites && s.Setup != SetupLenny {
		add("setup %q is neither %q nor %q", s.Setup, SetupPrerequisites, SetupLenny)
	}
	for _, c := range s.Credentials {
		if _, ok := CredentialEnv[c]; !ok {
			add("credential %q is not one of %s", c, strings.Join(sortedStringKeys(CredentialEnv), ", "))
		}
		if s.Setup == SetupPrerequisites && c != CredKubeconfig {
			add("credential %q needs a running gateway, which setup %q does not install", c, SetupPrerequisites)
		}
	}
	for _, p := range s.Prepare {
		if _, _, err := ParsePrepare(p); err != nil {
			add("%w", err)
		}
		if s.Setup != SetupLenny {
			add("preparation %q needs setup %q", p, SetupLenny)
		}
	}
	if strings.TrimSpace(s.Goal) == "" {
		add("goal is empty")
	}
	published := strings.Join(append([]string{s.Goal, s.Environment}, s.Steps...), "\n")
	for _, v := range s.PublicValues {
		if strings.TrimSpace(v) == "" || !strings.Contains(published, v) {
			add("public value %q does not appear in the goal, the steps, or the environment", v)
		}
	}
	errs = append(errs, validateDocs(s.Docs)...)
	errs = append(errs, validateTools(s.Tools)...)
	if len(s.Postconditions) == 0 {
		add("the scenario has no postconditions, so nothing would decide its verdict")
	}
	seen := map[string]bool{}
	for i := range s.Postconditions {
		p := &s.Postconditions[i]
		if seen[p.Name] {
			add("postcondition %q appears twice", p.Name)
		}
		seen[p.Name] = true
		if err := p.Validate(); err != nil {
			add("postcondition %d (%q): %w", i+1, p.Name, err)
		}
	}
	return errors.Join(errs...)
}

func validateDocs(docs []string) []error {
	if len(docs) == 0 {
		return []error{errors.New("docs is empty: the agent needs at least one page to start from")}
	}
	var errs []error
	for _, d := range docs {
		clean := path.Clean(d)
		if clean != d || !strings.HasPrefix(d, "docs/") || strings.Contains(d, "..") {
			errs = append(errs, fmt.Errorf("documentation page %q is not a clean path under docs/", d))
		}
	}
	return errs
}

func validateTools(tools []string) []error {
	if len(tools) == 0 {
		return []error{fmt.Errorf("tools is empty: name the commands the agent may run, from %s", strings.Join(AllowedCommands, ", "))}
	}
	var errs []error
	for _, t := range tools {
		if !contains(AllowedCommands, t) {
			errs = append(errs, fmt.Errorf("tool %q is not one of %s", t, strings.Join(AllowedCommands, ", ")))
		}
	}
	return errs
}

// CheckDocsExist reports every documentation page the scenario names
// that does not exist under root.
func (s *Scenario) CheckDocsExist(root string) error {
	var errs []error
	for _, d := range s.Docs {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(d))); err != nil {
			errs = append(errs, fmt.Errorf("%s: documentation page %s: %w", s.Path, d, err))
		}
	}
	return errors.Join(errs...)
}

// LoadDir parses every .yaml file in dir, sorted by journey name.
func LoadDir(dir string) ([]*Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*Scenario
	var errs []error
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		content, err := os.ReadFile(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s, err := ParseScenario(content, p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, s)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Select returns the scenarios named in names, or all of them when names
// is empty. An unknown name is an error.
func Select(all []*Scenario, names []string) ([]*Scenario, error) {
	if len(names) == 0 {
		return all, nil
	}
	byName := map[string]*Scenario{}
	for _, s := range all {
		byName[s.Name] = s
	}
	out := make([]*Scenario, 0, len(names))
	for _, n := range names {
		s, ok := byName[n]
		if !ok {
			known := make([]string, 0, len(all))
			for _, s := range all {
				known = append(known, s.Name)
			}
			return nil, fmt.Errorf("no journey named %q; the journeys are %s", n, strings.Join(known, ", "))
		}
		out = append(out, s)
	}
	return out, nil
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
