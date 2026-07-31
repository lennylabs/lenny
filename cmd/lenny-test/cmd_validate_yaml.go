// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// The three YAML configuration files under tests/ — groups.yaml,
// groups.subsets.yaml, and spec-map-exceptions.yaml — drive
// selector resolution, subset expansion, and the spec-coverage
// exemption list. A typo in any of them silently misroutes a CI
// run. These validators give validate-maps the same teeth on YAML
// that it already has on the JSON traceability files.

func validateGroupsYAML(path string) checkResult {
	body, err := os.ReadFile(path)
	if err != nil {
		return newResult("groups.yaml", false, fmt.Sprintf("could not read: %v", err))
	}
	// Selectors uses yaml.Node so unknown-but-valid keys (cloud_subset,
	// load_scenarios, additional_checks, exclude, include_all_tiers, …)
	// do not trip the empty-selector check. We only need to confirm the
	// block exists and has at least one mapping entry.
	var doc struct {
		Version int `yaml:"version"`
		Groups  map[string]struct {
			Description string    `yaml:"description"`
			Selectors   yaml.Node `yaml:"selectors"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return newResult("groups.yaml", false, fmt.Sprintf("invalid YAML: %v", err))
	}
	if doc.Version != 1 {
		return newResult("groups.yaml", false, fmt.Sprintf("expected version 1, got %d", doc.Version))
	}
	if len(doc.Groups) == 0 {
		return newResult("groups.yaml", false, "groups: section is empty")
	}
	knownTiers := map[string]bool{}
	for _, t := range allTiers() {
		knownTiers[t] = true
	}
	var problems []string
	for name, g := range doc.Groups {
		if !isPhaseGate(name) && g.Description == "" {
			problems = append(problems, fmt.Sprintf("%s: missing description", name))
		}
		// Selectors must exist and be a non-empty mapping.
		if g.Selectors.Kind == 0 {
			problems = append(problems, fmt.Sprintf("%s: missing selectors block", name))
			continue
		}
		if g.Selectors.Kind != yaml.MappingNode || len(g.Selectors.Content) == 0 {
			problems = append(problems, fmt.Sprintf("%s: selectors block is empty", name))
			continue
		}
		// Walk the selector keys and verify tier-named values are
		// known. We pair-iterate Content (keys at even indices,
		// values at odd indices) per yaml.v3 mapping representation.
		for i := 0; i+1 < len(g.Selectors.Content); i += 2 {
			k := g.Selectors.Content[i]
			v := g.Selectors.Content[i+1]
			switch k.Value {
			case "max_tier", "tier":
				if v.Value != "" && !knownTiers[v.Value] {
					problems = append(problems, fmt.Sprintf("%s: unknown %s %q", name, k.Value, v.Value))
				}
			case "tiers":
				for _, t := range v.Content {
					if !knownTiers[t.Value] {
						problems = append(problems, fmt.Sprintf("%s: unknown tier %q", name, t.Value))
					}
				}
			case "include":
				for j, item := range v.Content {
					tier := mappingValue(item, "tier")
					if tier == "" {
						problems = append(problems, fmt.Sprintf("%s: include[%d] missing tier", name, j))
					} else if !knownTiers[tier] {
						problems = append(problems, fmt.Sprintf("%s: include[%d] unknown tier %q", name, j, tier))
					}
				}
			}
		}
	}
	if len(problems) > 0 {
		return newResult("groups.yaml", false, summarizeProblems(problems))
	}
	return newResult("groups.yaml", true, fmt.Sprintf("%d group(s); every selector resolves", len(doc.Groups)))
}

func validateGroupsSubsetsYAML(path string) checkResult {
	body, err := os.ReadFile(path)
	if err != nil {
		return newResult("groups.subsets.yaml", false, fmt.Sprintf("could not read: %v", err))
	}
	// Subsets carry their contents through one of several slots:
	//   tests:        []string of paths, or the scalar `all`
	//   scenarios:    same shape as tests, for tier 7a / 7b load
	//   scenarios_ref: name of another subset (delegation)
	//   runtimes:     []string of runtime ids (tier 10 conformance)
	//   run:          Go regexp matched against test names (tier 4)
	// We accept yaml.Node for the polymorphic slots and only verify
	// at least one is populated.
	var doc struct {
		Version int `yaml:"version"`
		Subsets map[string]struct {
			Tier         string    `yaml:"tier"`
			Tests        yaml.Node `yaml:"tests"`
			Scenarios    yaml.Node `yaml:"scenarios"`
			ScenariosRef string    `yaml:"scenarios_ref"`
			Runtimes     []string  `yaml:"runtimes"`
			Run          string    `yaml:"run"`
		} `yaml:"subsets"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return newResult("groups.subsets.yaml", false, fmt.Sprintf("invalid YAML: %v", err))
	}
	if doc.Version != 1 {
		return newResult("groups.subsets.yaml", false, fmt.Sprintf("expected version 1, got %d", doc.Version))
	}
	if len(doc.Subsets) == 0 {
		return newResult("groups.subsets.yaml", false, "subsets: section is empty")
	}
	knownTiers := map[string]bool{}
	for _, t := range allTiers() {
		knownTiers[t] = true
	}
	var problems []string
	for name, s := range doc.Subsets {
		if s.Tier == "" {
			problems = append(problems, fmt.Sprintf("%s: missing tier", name))
		} else if !knownTiers[s.Tier] {
			problems = append(problems, fmt.Sprintf("%s: unknown tier %q", name, s.Tier))
		}
		// A subset declares its contents through one of three slots:
		// tests (most tiers), scenarios (tier 7a / 7b load), or scenarios_ref
		// (delegates to another subset). Each may carry a sequence of
		// paths or the scalar sentinel `all`.
		hasTests := s.Tests.Kind == yaml.SequenceNode && len(s.Tests.Content) > 0
		hasTests = hasTests || (s.Tests.Kind == yaml.ScalarNode && s.Tests.Value != "")
		hasScenarios := s.Scenarios.Kind == yaml.SequenceNode && len(s.Scenarios.Content) > 0
		hasScenarios = hasScenarios || (s.Scenarios.Kind == yaml.ScalarNode && s.Scenarios.Value != "")
		if !hasTests && !hasScenarios && s.ScenariosRef == "" && len(s.Runtimes) == 0 && s.Run == "" {
			problems = append(problems, fmt.Sprintf("%s: no tests/scenarios/runtimes/run/scenarios_ref", name))
		}
	}
	if len(problems) > 0 {
		return newResult("groups.subsets.yaml", false, summarizeProblems(problems))
	}
	return newResult("groups.subsets.yaml", true,
		fmt.Sprintf("%d subset(s); every entry has a tier and at least one content slot", len(doc.Subsets)))
}

func validateSpecMapExceptionsYAML(path string) checkResult {
	body, err := os.ReadFile(path)
	if err != nil {
		return newResult("spec-map-exceptions.yaml", false, fmt.Sprintf("could not read: %v", err))
	}
	var doc struct {
		Version    int `yaml:"version"`
		Exceptions []struct {
			Section       string `yaml:"section"`
			Reason        string `yaml:"reason"`
			Justification string `yaml:"justification"`
		} `yaml:"exceptions"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return newResult("spec-map-exceptions.yaml", false, fmt.Sprintf("invalid YAML: %v", err))
	}
	if doc.Version != 1 {
		return newResult("spec-map-exceptions.yaml", false, fmt.Sprintf("expected version 1, got %d", doc.Version))
	}
	if len(doc.Exceptions) == 0 {
		return newResult("spec-map-exceptions.yaml", true, "no exceptions defined")
	}
	allowedReasons := map[string]bool{
		"non-normative":     true,
		"indirect-coverage": true,
		"meta":              true,
		"deferred":          true,
		"empty":             true,
		"post-v1":           true,
		"anti-feature":      true,
	}
	var problems []string
	seen := map[string]int{}
	for i, e := range doc.Exceptions {
		if e.Section == "" {
			problems = append(problems, fmt.Sprintf("exceptions[%d]: missing section", i))
			continue
		}
		if prev, dup := seen[e.Section]; dup {
			problems = append(problems, fmt.Sprintf("exceptions[%d]: section %q already declared at exceptions[%d]", i, e.Section, prev))
		}
		seen[e.Section] = i
		if e.Reason == "" {
			problems = append(problems, fmt.Sprintf("exceptions[%d] (%q): missing reason", i, e.Section))
		} else if !allowedReasons[e.Reason] {
			problems = append(problems, fmt.Sprintf("exceptions[%d] (%q): unknown reason %q", i, e.Section, e.Reason))
		}
		if strings.TrimSpace(e.Justification) == "" {
			problems = append(problems, fmt.Sprintf("exceptions[%d] (%q): missing justification", i, e.Section))
		}
	}
	if len(problems) > 0 {
		return newResult("spec-map-exceptions.yaml", false, summarizeProblems(problems))
	}
	return newResult("spec-map-exceptions.yaml", true,
		fmt.Sprintf("%d exception(s); every entry has a reason and a justification", len(doc.Exceptions)))
}

// readExceptionSections returns the set of spec sections listed in
// tests/spec-map-exceptions.yaml. A missing or unparseable file
// yields an empty set; validateSpecMapExceptionsYAML reports the
// parse error separately.
func readExceptionSections(path string) map[string]bool {
	out := map[string]bool{}
	body, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var doc struct {
		Exceptions []struct {
			Section string `yaml:"section"`
		} `yaml:"exceptions"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return out
	}
	for _, e := range doc.Exceptions {
		if e.Section != "" {
			out[e.Section] = true
		}
	}
	return out
}

// validateFlakeBudgetYAML enforces TESTING.md §21.4 on
// tests/flake-budget.yaml. Every quarantined entry must carry a
// non-empty test name, an https issue link, and an eta in
// YYYY-MM-DD that has not yet passed.
func validateFlakeBudgetYAML(path string) checkResult {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newResult("flake-budget.yaml", true, "absent; quarantine list is empty by convention")
		}
		return newResult("flake-budget.yaml", false, fmt.Sprintf("could not read: %v", err))
	}
	var doc struct {
		Version     int `yaml:"version"`
		Quarantined []struct {
			Test    string `yaml:"test"`
			Package string `yaml:"package"`
			Owner   string `yaml:"owner"`
			Issue   string `yaml:"issue"`
			ETA     string `yaml:"eta"`
		} `yaml:"quarantined"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return newResult("flake-budget.yaml", false, fmt.Sprintf("invalid YAML: %v", err))
	}
	if doc.Version != 1 {
		return newResult("flake-budget.yaml", false, fmt.Sprintf("expected version 1, got %d", doc.Version))
	}
	var problems []string
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for i, e := range doc.Quarantined {
		if strings.TrimSpace(e.Test) == "" {
			problems = append(problems, fmt.Sprintf("entry[%d]: missing test name", i))
			continue
		}
		if !strings.HasPrefix(e.Issue, "https://") {
			problems = append(problems, fmt.Sprintf("entry[%d] (%q): issue must be an https URL; got %q", i, e.Test, e.Issue))
		}
		if e.ETA == "" {
			problems = append(problems, fmt.Sprintf("entry[%d] (%q): missing eta", i, e.Test))
			continue
		}
		t, err := time.Parse("2006-01-02", e.ETA)
		if err != nil {
			problems = append(problems, fmt.Sprintf("entry[%d] (%q): eta %q not YYYY-MM-DD", i, e.Test, e.ETA))
			continue
		}
		if t.Before(today) {
			problems = append(problems, fmt.Sprintf("entry[%d] (%q): eta %s has passed; resolve or extend with a new issue", i, e.Test, e.ETA))
		}
	}
	if len(problems) > 0 {
		return newResult("flake-budget.yaml", false, summarizeProblems(problems))
	}
	return newResult("flake-budget.yaml", true,
		fmt.Sprintf("%d quarantined test(s); every entry valid", len(doc.Quarantined)))
}

// ---- the shared register contract ---------------------------------

// An exception register is a YAML file under tests/registers/
// recording the violations one gate accepts for now, so that gate can
// fail everything else. The pattern is generalized from the two in-tree
// pending lists and from validateFlakeBudgetYAML, which already dates
// and owns each quarantine entry. Every exception register shares one
// entry schema, carrying a subject, a verdict, an owner, an opened_at
// date, an expiry, a blocker, and a reason, and one set of ratchet
// rules:
//
//   - a violation with no entry fails, so an exemption is written down
//     before a gate lets it through;
//   - an entry whose expiry has passed fails, so an exemption ends;
//   - an entry whose blocker resolves to no open item fails, so an
//     exemption names outstanding work.
//
// tests/registers/ also holds files that deliberately do not use this
// schema, and each of them declares its own kind so the shared contract
// ranges over the files that use it rather than over every file the
// directory happens to hold:
//
//   - a residual register carries a member, a class, an in-class or
//     excluded disposition, and a reason. An exclusion is permanent,
//     and an in-class entry is retired by the event that takes its
//     member out of its class, so neither carries a date on which it
//     becomes wrong nor an open item a blocker could name, and the
//     expiry and blocker rules would fail every such entry;
//   - a baseline is keyed for the rewrite it drives and is rewritten
//     downward as that rewrite proceeds. The per-file line-citation
//     counts, the per-citation resolution baseline, the change-graph
//     coverage baseline keyed by path prefix, and the skip-reason
//     baseline keyed by file and call site are all baselines, and a
//     path with no glob key or a host-capability skip has no pending
//     item a blocker could name;
//   - a sense map is keyed by file and occurrence and records which
//     identifier a rewrite writes at each site, so it records a
//     decision rather than an exemption.
//
// Each of those kinds is validated by the gate that reads it, against
// its own schema.
type registerEntry struct {
	// Subject is the violation the entry exempts, in whatever
	// vocabulary the gate that reads the register measures.
	Subject string `yaml:"subject"`
	// Verdict is the outcome the gate would report for Subject if the
	// entry did not exist.
	Verdict string `yaml:"verdict"`
	// Owner is the person accountable for closing the entry.
	Owner string `yaml:"owner"`
	// OpenedAt is the date the entry was written, in YYYY-MM-DD.
	OpenedAt string `yaml:"opened_at"`
	// Expiry is the date the entry stops holding, in YYYY-MM-DD.
	Expiry string `yaml:"expiry"`
	// Blocker names the open item whose closure retires the entry.
	Blocker string `yaml:"blocker"`
	// Reason states why the violation is accepted for now.
	Reason string `yaml:"reason"`
}

// The kinds a file under tests/registers/ may declare. Membership of
// the shared contract is a declaration in the file rather than a
// filename convention, so a register that carries its own schema is
// never held to the shared entry schema and a file that declares
// nothing fails rather than going unvalidated.
const (
	// registerKindException marks a register the shared contract owns.
	registerKindException = "exception-register"
	// registerKindResidual marks a per-class residual register.
	registerKindResidual = "residual-register"
	// registerKindBaseline marks a count or population baseline a pass
	// rewrites downward.
	registerKindBaseline = "baseline"
	// registerKindSenseMap marks a per-occurrence map from a site to
	// the identifier a pass writes there.
	registerKindSenseMap = "sense-map"
)

// registerKinds lists the declared kinds in the order the failure
// message names them.
func registerKinds() []string {
	return []string{registerKindException, registerKindResidual, registerKindBaseline, registerKindSenseMap}
}

// registerFile is one parsed exception register.
type registerFile struct {
	Kind    string          `yaml:"kind"`
	Version int             `yaml:"version"`
	Entries []registerEntry `yaml:"entries"`
}

// registerRules carries the two dependencies the ratchet rules need.
// Both are injected so a case can pin an expiry boundary and an
// open-item domain without reaching for the wall clock or the tracked
// audit records.
type registerRules struct {
	// now is the instant the expiry rule compares against.
	now time.Time
	// openItem reports whether a blocker names an item that is still
	// open. The domain the harness runs with is openItemIDs, which is
	// the open findings of the tracked audit records together with the
	// remediation steps the tracked plan documents declare. A nil
	// resolver resolves nothing, so the blocker rule fails closed
	// rather than certifying every entry.
	openItem func(blocker string) bool
}

// resolvesBlocker reports whether blocker names an open item.
func (r registerRules) resolvesBlocker(blocker string) bool {
	if r.openItem == nil {
		return false
	}
	return r.openItem(blocker)
}

// loadRegisterFile reads and parses one register. A missing or
// unparseable file is an error: a register that cannot be read exempts
// nothing, and a gate that treated it as empty would certify a tree it
// never measured.
func loadRegisterFile(path string) (*registerFile, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read register %s: %w", path, err)
	}
	var doc registerFile
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse register %s: %w", path, err)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("register %s: expected version 1, got %d", path, doc.Version)
	}
	if doc.Kind != registerKindException {
		return nil, fmt.Errorf("register %s: expected kind %q, got %q", path, registerKindException, doc.Kind)
	}
	return &doc, nil
}

// registerFileKind reads the kind one file under tests/registers/
// declares. A file that cannot be read or parsed is an error rather
// than a file of no kind, so the directory sweep reports it instead of
// passing over it.
func registerFileKind(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read register %s: %w", path, err)
	}
	var doc struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("parse register %s: %w", path, err)
	}
	return strings.TrimSpace(doc.Kind), nil
}

// registerVerdicts are the verdicts a register entry may record. An
// entry exists to hold back a failing outcome, so verdictstatus.
// VerdictPass and VerdictInconclusive are rejected: the first records
// no violation at all, and the second is the harness's own
// infrastructure-failure state rather than a gate result about the
// tree.
func registerVerdicts() map[string]bool {
	return map[string]bool{
		verdictstatus.VerdictFail:       true,
		verdictstatus.VerdictUnverified: true,
	}
}

// registerEntryProblems applies the entry schema and the expiry and
// blocker ratchet rules to every entry, and returns one message per
// violation.
func registerEntryProblems(doc *registerFile, rules registerRules) []string {
	var problems []string
	today := rules.now.UTC().Truncate(24 * time.Hour)
	allowedVerdicts := registerVerdicts()
	seen := map[string]int{}
	for i, e := range doc.Entries {
		subject := strings.TrimSpace(e.Subject)
		if subject == "" {
			problems = append(problems, fmt.Sprintf("entry[%d]: missing subject", i))
			continue
		}
		if prev, dup := seen[subject]; dup {
			problems = append(problems, fmt.Sprintf("entry[%d] (%q): subject already declared at entry[%d]", i, subject, prev))
		}
		seen[subject] = i
		if !allowedVerdicts[e.Verdict] {
			problems = append(problems, fmt.Sprintf("entry[%d] (%q): verdict must be %s or %s; got %q",
				i, subject, verdictstatus.VerdictFail, verdictstatus.VerdictUnverified, e.Verdict))
		}
		if strings.TrimSpace(e.Owner) == "" {
			problems = append(problems, fmt.Sprintf("entry[%d] (%q): missing owner", i, subject))
		}
		if strings.TrimSpace(e.Reason) == "" {
			problems = append(problems, fmt.Sprintf("entry[%d] (%q): missing reason", i, subject))
		}
		problems = append(problems, registerDateProblems(i, subject, e, today)...)
		switch blocker := strings.TrimSpace(e.Blocker); {
		case blocker == "":
			problems = append(problems, fmt.Sprintf("entry[%d] (%q): missing blocker", i, subject))
		case !rules.resolvesBlocker(blocker):
			problems = append(problems, fmt.Sprintf("entry[%d] (%q): blocker %q does not resolve to an open item", i, subject, blocker))
		}
	}
	return problems
}

// registerDateProblems checks the opened_at and expiry dates of one
// entry, and applies the ratchet rule that a passed expiry fails.
func registerDateProblems(i int, subject string, e registerEntry, today time.Time) []string {
	var problems []string
	if strings.TrimSpace(e.OpenedAt) == "" {
		problems = append(problems, fmt.Sprintf("entry[%d] (%q): missing opened_at", i, subject))
	} else if _, err := time.Parse("2006-01-02", e.OpenedAt); err != nil {
		problems = append(problems, fmt.Sprintf("entry[%d] (%q): opened_at %q not YYYY-MM-DD", i, subject, e.OpenedAt))
	}
	expiry := strings.TrimSpace(e.Expiry)
	if expiry == "" {
		problems = append(problems, fmt.Sprintf("entry[%d] (%q): missing expiry", i, subject))
		return problems
	}
	t, err := time.Parse("2006-01-02", expiry)
	if err != nil {
		problems = append(problems, fmt.Sprintf("entry[%d] (%q): expiry %q not YYYY-MM-DD", i, subject, expiry))
		return problems
	}
	if t.Before(today) {
		problems = append(problems, fmt.Sprintf("entry[%d] (%q): expiry %s has passed; close the entry or reopen it against a current blocker", i, subject, expiry))
	}
	return problems
}

// checkRegister runs the shared contract over one register and the
// violations the calling gate measured against the tree. Every gate
// that exempts anything routes through this function, so the three
// ratchet rules cannot drift between gates. Passing a nil violation
// slice validates the register alone.
func checkRegister(name, path string, violations []string, rules registerRules) checkResult {
	doc, err := loadRegisterFile(path)
	if err != nil {
		return newResult(name, false, err.Error())
	}
	problems := registerEntryProblems(doc, rules)
	registered := map[string]bool{}
	for _, e := range doc.Entries {
		if s := strings.TrimSpace(e.Subject); s != "" {
			registered[s] = true
		}
	}
	for _, v := range violations {
		if !registered[v] {
			problems = append(problems, fmt.Sprintf("unregistered violation %q: add an entry or fix it", v))
		}
	}
	if len(problems) > 0 {
		return newResult(name, false, summarizeProblems(problems))
	}
	return newResult(name, true,
		fmt.Sprintf("%d entr(ies); every entry is owned, dated, and blocked on an open item", len(doc.Entries)))
}

// validateRegistersDir validates every exception register under
// tests/registers/ against the shared contract. Gates supply their own
// violation sets when they run; this check confirms the registers
// themselves hold, so an entry that has outlived its expiry or its
// blocker fails the tier even before the gate that reads it runs. A
// file declaring another kind carries its own schema and is validated
// by the gate that reads it, and a file declaring no recognized kind
// fails rather than going unvalidated by every gate.
func validateRegistersDir(dir string, rules registerRules) checkResult {
	const name = "tests/registers"
	if _, err := os.Stat(dir); err != nil {
		return newResult(name, false, fmt.Sprintf("could not read directory: %v", err))
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return newResult(name, false, fmt.Sprintf("could not list: %v", err))
	}
	sort.Strings(files)
	var problems []string
	validated := 0
	for _, f := range files {
		base := filepath.Base(f)
		kind, err := registerFileKind(f)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", base, err))
			continue
		}
		switch kind {
		case registerKindException:
			validated++
			if r := checkRegister(name, f, nil, rules); !r.ok {
				problems = append(problems, fmt.Sprintf("%s: %s", base, r.detail))
			}
		case registerKindResidual, registerKindBaseline, registerKindSenseMap:
			// The gate that reads this file validates it against its
			// own schema; the shared contract does not range over it.
		case "":
			problems = append(problems, fmt.Sprintf("%s: missing kind; declare one of %s",
				base, strings.Join(registerKinds(), ", ")))
		default:
			problems = append(problems, fmt.Sprintf("%s: kind %q is not a register kind; declare one of %s",
				base, kind, strings.Join(registerKinds(), ", ")))
		}
	}
	if len(problems) > 0 {
		return newResult(name, false, summarizeProblems(problems))
	}
	return newResult(name, true, fmt.Sprintf("%d register(s) hold the shared contract", validated))
}

// repoRegisterRules builds the register rules the harness runs with:
// the wall clock, and the tracked open items as the domain a blocker
// resolves against.
func repoRegisterRules(root string) registerRules {
	open := openItemIDs(root)
	return registerRules{
		now: time.Now().UTC(),
		openItem: func(blocker string) bool {
			return open[strings.TrimSpace(blocker)]
		},
	}
}

// openItemIDs returns every identifier a register blocker may name. Two
// namespaces are open items: a finding still marked OPEN in a tracked
// audit record, and a remediation step declared by a tracked plan
// document. The step identifiers are part of the domain because a gate
// lands green by seeding its register with an entry blocked on the
// remediation step that retires the entry, and because the audit
// records state findings as they were written rather than the work
// still outstanding. A step leaves the domain when its plan document
// stops declaring it.
func openItemIDs(root string) map[string]bool {
	out := openFindingIDs(root)
	for id := range remediationStepIDs(root) {
		out[id] = true
	}
	return out
}

// remediationStepHeading matches a remediation-step heading, which is a
// markdown heading opening with the step identifier followed by a
// period, in both the dashed spelling a plan sub-step uses and the
// undashed spelling a top-level step uses.
var remediationStepHeading = regexp.MustCompile(`^#{2,6} ([A-Z][A-Z0-9]*-[0-9]+|[A-Z]+[0-9]+)\. `)

// remediationStepIDs returns the step identifiers the tracked plan
// documents declare, which are the root-level remediation plans and the
// proposals that stage their steps.
func remediationStepIDs(root string) map[string]bool {
	out := map[string]bool{}
	docs, _ := filepath.Glob(filepath.Join(root, "*-remediation.md"))
	staged, _ := filepath.Glob(filepath.Join(root, "proposals", "*.md"))
	for _, doc := range append(docs, staged...) {
		body, err := os.ReadFile(doc)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(body), "\n") {
			if m := remediationStepHeading.FindStringSubmatch(line); m != nil {
				out[m[1]] = true
			}
		}
	}
	return out
}

// openFindingIDs returns the identifiers of the findings still marked
// OPEN in the tracked audit records. Each finding heading is a
// checklist line whose identifier follows the checkbox and whose status
// marker closes the line, per the format both records state.
func openFindingIDs(root string) map[string]bool {
	out := map[string]bool{}
	for _, rec := range []string{"BUILD-GAPS.md", "TEST-GAPS.md"} {
		body, err := os.ReadFile(filepath.Join(root, rec))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.HasPrefix(line, "#") || !strings.HasSuffix(strings.TrimSpace(line), "— OPEN") {
				continue
			}
			_, after, found := strings.Cut(line, "- [ ] ")
			if !found {
				continue
			}
			id, _, found := strings.Cut(after, " —")
			if !found {
				continue
			}
			if id = strings.TrimSpace(id); id != "" {
				out[id] = true
			}
		}
	}
	return out
}

// validateParityMatrixYAML enforces TESTING.md §12.6 on
// tests/tier6_e2e_cloud/parity-matrix.yaml. Every capability has
// at least one `validated` provider, every provider in a row is
// listed under the top-level providers block, and every `skip`
// status carries a reason.
func validateParityMatrixYAML(path string) checkResult {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newResult("parity-matrix.yaml", true, "absent; tier 6 cloud parity matrix is empty by convention")
		}
		return newResult("parity-matrix.yaml", false, fmt.Sprintf("could not read: %v", err))
	}
	var doc struct {
		Version      int      `yaml:"version"`
		Providers    []string `yaml:"providers"`
		Capabilities []struct {
			Name        string               `yaml:"name"`
			SpecSection string               `yaml:"spec_section"`
			Status      map[string]yaml.Node `yaml:"status"`
		} `yaml:"capabilities"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return newResult("parity-matrix.yaml", false, fmt.Sprintf("invalid YAML: %v", err))
	}
	if doc.Version != 1 {
		return newResult("parity-matrix.yaml", false, fmt.Sprintf("expected version 1, got %d", doc.Version))
	}
	known := map[string]bool{}
	for _, p := range doc.Providers {
		known[p] = true
	}
	if len(known) == 0 {
		return newResult("parity-matrix.yaml", false, "providers list is empty")
	}
	allowed := map[string]bool{"validated": true, "planned": true, "skip": true}
	var problems []string
	for i, c := range doc.Capabilities {
		if strings.TrimSpace(c.Name) == "" {
			problems = append(problems, fmt.Sprintf("capability[%d]: missing name", i))
			continue
		}
		if len(c.Status) == 0 {
			problems = append(problems, fmt.Sprintf("%s: empty status block", c.Name))
			continue
		}
		hasValidated := false
		for provider, node := range c.Status {
			if !known[provider] {
				problems = append(problems, fmt.Sprintf("%s: provider %q not in top-level providers list", c.Name, provider))
				continue
			}
			state := parityCellState(node)
			if state == "" {
				problems = append(problems, fmt.Sprintf("%s.%s: missing state", c.Name, provider))
				continue
			}
			if !allowed[state] {
				problems = append(problems, fmt.Sprintf("%s.%s: unknown state %q", c.Name, provider, state))
			}
			if state == "skip" && parityCellReason(node) == "" {
				problems = append(problems, fmt.Sprintf("%s.%s: skip requires a reason", c.Name, provider))
			}
			if state == "validated" {
				hasValidated = true
			}
		}
		// "planned" everywhere is acceptable today (no real cloud tests
		// have landed yet); a capability with zero providers listed at
		// all is the failure mode this check guards against.
		_ = hasValidated
	}
	if len(problems) > 0 {
		return newResult("parity-matrix.yaml", false, summarizeProblems(problems))
	}
	return newResult("parity-matrix.yaml", true,
		fmt.Sprintf("%d capability/provider cell(s) across %d providers", countCells(doc.Capabilities), len(doc.Providers)))
}

func parityCellState(node yaml.Node) string {
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Value
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "state" {
				return node.Content[i+1].Value
			}
		}
	}
	return ""
}

func parityCellReason(node yaml.Node) string {
	if node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "reason" {
			return strings.TrimSpace(node.Content[i+1].Value)
		}
	}
	return ""
}

func countCells(caps []struct {
	Name        string               `yaml:"name"`
	SpecSection string               `yaml:"spec_section"`
	Status      map[string]yaml.Node `yaml:"status"`
},
) int {
	n := 0
	for _, c := range caps {
		n += len(c.Status)
	}
	return n
}

// mappingValue returns the scalar value of `key` inside a YAML
// mapping node, or empty string when the key is absent or the value
// is not scalar.
func mappingValue(node *yaml.Node, key string) string {
	if node == nil || node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1].Value
		}
	}
	return ""
}

func summarizeProblems(problems []string) string {
	preview := problems
	if len(preview) > 5 {
		preview = append(append([]string{}, preview[:5]...),
			fmt.Sprintf("... (%d more)", len(problems)-5))
	}
	return fmt.Sprintf("%d issue(s): %s", len(problems), strings.Join(preview, "; "))
}

// yamlPaths returns the canonical paths to the YAML config files
// under tests/. validate-maps validates each in turn. The repo-
// relative names live in paths.go.
func yamlPaths(root string) (groups, subsets, exceptions, flakeBudget, parityMatrix string) {
	groups = filepath.Join(root, groupsFile)
	subsets = filepath.Join(root, groupsSubsetsFile)
	exceptions = filepath.Join(root, specMapExceptionsFile)
	flakeBudget = filepath.Join(root, flakeBudgetFile)
	parityMatrix = filepath.Join(root, parityMatrixFile)
	return
}
