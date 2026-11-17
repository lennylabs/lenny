// SPDX-License-Identifier: MIT

package defs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These cases cover specification tooling rather than a platform behavior,
// so they carry no specification annotation. The contract they pin is the
// one the package comment states: every block validates against its
// kind's schema, anchors are unique, and every page code a block names is
// registered, with each problem located by file and line.

// writeTree writes files (repo-relative path to content) under a fresh
// root and returns the root.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// loadTree writes and loads a tree.
func loadTree(t *testing.T, files map[string]string) *Tree {
	t.Helper()
	tr, err := LoadTree(writeTree(t, files))
	if err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	return tr
}

// placeholder returns a placeholder page with a code, which a block may
// name without the page carrying a generated section.
func placeholder(code string) string {
	return "---\ncode: " + code + "\ntype: component\nstatus: placeholder\nsummary: A placeholder.\n---\n\n# " + code + "\n"
}

// blockPage returns a page with code CAP holding one block under an anchor
// line. The front matter is five lines and the page opens with a title, a
// blank line, the anchor line, and a blank line, so the fence is on line 10
// and the block's first field on line 11.
func blockPage(anchor, yaml string) string {
	return "---\ncode: CAP\ntype: capability\nsummary: The page under test.\n---\n# Page\n\n<a id=\"" + anchor + "\"></a>\n\n```lenny-def\n" + yaml + "```\n"
}

// baseFiles are the pages every block case may name.
func baseFiles(page string) map[string]string {
	return map[string]string{
		"spec/caps/page.md":      page,
		"spec/caps/index.md":     "---\ncode: CAPIDX\ntype: index\nsummary: Lists the fixture pages.\n---\n",
		"spec/components/wpc.md": placeholder("WPC"),
		"spec/components/gw.md":  placeholder("GW"),
	}
}

func checkPage(t *testing.T, page string) []Problem {
	t.Helper()
	return Check(loadTree(t, baseFiles(page)))
}

// validBlocks holds one valid block of every kind, with its anchor.
var validBlocks = map[string][2]string{
	KindSetting:          {"setting-gateway-claimholdttlseconds", "kind: setting\nname: gateway.claimHoldTTLSeconds\ntype: integer\ndefault: 10\nrange: [0, 300]\nunit: seconds\nscopes: [platform, pool]\ndefaults-by-tier: {small: 10, medium: 10, large: 10}\nread-by: [WPC]\nsummary: How long a claim stays reserved.\n"},
	KindLimit:            {"limit-maxuploadbytes", "kind: limit\nname: maxUploadBytes\nvalue: 1073741824\nunit: bytes\nscopes: [session]\nenforced-by: WPC\nsummary: The largest upload.\n"},
	KindError:            {"error-warm-pool-exhausted", "kind: error\nname: WARM_POOL_EXHAUSTED\nstatus: 503\ncategory: TRANSIENT\nretryable: true\ndetails: {reason: no_idle_pods}\nreturned-by: [CAPISES-createSession]\nrule: CAP-R1\nsummary: No pod could be acquired.\n"},
	KindAdmissionRefusal: {"admission-refusal-podclaim-concurrent", "kind: admission-refusal\nname: podclaim concurrent\nmessage: \"PodClaim already exists\"\nstatus: 403\nresource: PodClaim\noperations: [CREATE]\nwebhook: lenny-podclaim-guard\nrule: [CAP-R11]\nsummary: The guard refused a second claim.\n"},
	KindConditionReason:  {"condition-reason-podsetkeycollision", "kind: condition-reason\nname: PodSetKeyCollision\nresource: PodSet\ncondition: Degraded\nrule: CAP-R4\nsummary: Two keys gave one name.\n"},
	KindMetric:           {"metric-lenny-pod-claim-duration-seconds", "kind: metric\nname: lenny_pod_claim_duration_seconds\ntype: histogram\nunit: seconds\nlabels: []\nemitter: WPC\nsummary: Time to a bound pod.\n"},
	KindAlert:            {"alert-warmpoollow", "kind: alert\nname: WarmPoolLow\nexpr: lenny_warmpool_idle_pods < 1\nseverity: warning\nfor: 5m\ndetects: [CAP-R9]\nrunbook: warm-pool-low\nsummary: Idle pods are low.\n"},
	KindEvent:            {"event-dev-lenny-session-created", "kind: event\ntype: dev.lenny.session.created\nemitter: [GW]\npayload: The session's identifier.\nsummary: A session was created.\n"},
	KindLabel:            {"label-lenny-dev-pool", "kind: label\nkey: lenny.dev/pool\ncarriers: [PodSet]\nvalues: the pool name\nmutability: required; immutable\ntransitions: none\nrefusal: tenant_label_immutable\nwriters: [WPC]\nreaders: [GW]\nmeaning: CAP-R5\nsummary: The pool of a pod.\n"},
	KindAnnotation:       {"annotation-lenny-dev-drain-request", "kind: annotation\nkey: lenny.dev/drain-request\ncarriers: [agent pod]\nvalues: [requested]\nwriters: [GW]\nreaders: [WPC]\nsummary: Asks for a drain.\n"},
	KindFinalizer:        {"finalizer-lenny-dev-session-cleanup", "kind: finalizer\nkey: lenny.dev/session-cleanup\ncarriers: [AgentPod]\nwriters: [WPC]\nreaders: [GW]\nmeaning: CAP-R16\nsummary: Holds a pod until its session is safe.\n"},
	KindReadinessGate:    {"readiness-gate-lenny-dev-agent-pod-ready", "kind: readiness-gate\nkey: lenny.dev/agent-pod-ready\ncarriers: [agent pod]\nwriters: [WPC]\nsummary: Marks a pod ready.\n"},
	KindCustomResource:   {"custom-resource-podclaim", "kind: custom-resource\nname: PodClaim\ngroup: lenny.dev\nversion: v1alpha1\nscope: Namespaced (agent namespace)\nplural: podclaims\ncrd: podclaims.lenny.dev\nshort-name: pclaim\nwriters: [GW]\nreaders: [WPC]\nsummary: A claim on one pod.\n"},
	KindOperation:        {"cap-createpool", "kind: operation\nname: createPool\nmethod: POST\npath: /v1/admin/pools\nreturns: [RESOURCE_ALREADY_EXISTS]\nrequirements: [CAP-R2]\nsummary: Creates a pool.\n"},
	KindState:            {"state-claim-binding-state", "kind: state\nname: claim binding state\nobject: claim\nstates: [bound, reserved, released]\ninitial: bound\nterminal: [released]\ntransitions:\n  - {from: bound, to: reserved, on: session end}\n  - {from: [bound, reserved], to: released, rule: CAP-R2}\nsummary: The state of a claim.\n"},
	KindRegister:         {"register-slot-set", "kind: register\nname: slot set\nidentifier: REG-SLOTSET\nstore: Redis\nkeys: [\"lenny:pod:{pod_id}:active_slots\"]\nmembers: [\"<sessionId>\"]\nwriters: [GW]\nreaders: [GW]\nsummary: A pod's slots.\n"},
	KindGrant:            {"grant-pods-write", "kind: grant\nname: pods write\nservice-account: lenny-controller\napi-groups: [\"\"]\nresources: [pods]\nverbs: [create, patch]\nnamespace-scope: agent namespace\nrequirement: CAP-R1\nsummary: Creates pods.\n"},
	KindRBACGrant:        {"rbac-grant-podclaims-write", "kind: rbac-grant\nname: podclaims write\nunit: GW\napi-groups: [lenny.dev]\nresources: [podclaims/status]\nverbs: [get, patch]\nnamespace-scope: agent namespace\nrequirement: CAP-R4\n"},
	KindNetworkRule:      {"network-rule-gateway-to-adapter", "kind: network-rule\nname: gateway to adapter\nunit: GW\ndirection: egress\npeer: agent pod\nports: [8443/TCP]\nrequirement: CAP-R7\nsummary: Dials the adapter.\n"},
}

func TestEveryKindHasAValidBlockThatPassesCheck(t *testing.T) {
	t.Parallel()
	for _, k := range Kinds() {
		if _, ok := validBlocks[k.Name]; !ok {
			t.Errorf("kind %s has no valid fixture block", k.Name)
		}
	}
	for kind, b := range validBlocks {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			if problems := checkPage(t, blockPage(b[0], b[1])); len(problems) > 0 {
				t.Errorf("a valid %s block was refused:\n%s", kind, joinProblems(problems))
			}
		})
	}
}

func TestEveryKindRefusesAMissingRequiredFieldAndAnUnknownField(t *testing.T) {
	t.Parallel()
	for kind, b := range validBlocks {
		k, _ := KindByName(kind)
		for _, f := range k.Fields {
			if !f.Required || f.Name == k.NameField {
				continue
			}
			t.Run(kind+"/without "+f.Name, func(t *testing.T) {
				t.Parallel()
				yaml := dropField(b[1], f.Name)
				assertProblem(t, checkPage(t, blockPage(b[0], yaml)), "spec/caps/page.md", 10, "requires the field \""+f.Name+"\"")
			})
		}
		t.Run(kind+"/unknown field", func(t *testing.T) {
			t.Parallel()
			yaml := b[1] + "bogus: 1\n"
			line := 10 + strings.Count(b[1], "\n") + 1
			assertProblem(t, checkPage(t, blockPage(b[0], yaml)), "spec/caps/page.md", line, "has no field \"bogus\"")
		})
	}
}

// dropField removes a top-level field and its indented continuation lines.
func dropField(yaml, field string) string {
	var out []string
	skipping := false
	for _, line := range strings.SplitAfter(yaml, "\n") {
		switch {
		case strings.HasPrefix(line, field+":"):
			skipping = true
			continue
		case skipping && strings.HasPrefix(line, " "):
			continue
		}
		skipping = false
		out = append(out, line)
	}
	return strings.Join(out, "")
}

func TestCheckRefusesValuesOfTheWrongType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, kind, from, to, want string
	}{
		{"error status not an integer", KindError, "status: 503", "status: five hundred", "status: must be an integer"},
		{"error category outside the enum", KindError, "category: TRANSIENT", "category: SOMETIMES", "category: must be one of"},
		{"error retryable not a boolean", KindError, "retryable: true", "retryable: maybe", "retryable: must be true or false"},
		{"error details not a mapping", KindError, "details: {reason: no_idle_pods}", "details: [a]", "details: must be a non-empty mapping"},
		{"metric type outside the enum", KindMetric, "type: histogram", "type: timer", "type: must be one of counter, gauge, histogram, summary"},
		{"metric labels not a list", KindMetric, "labels: []", "labels: {a: b}", "labels: must be a list of values"},
		{"alert severity outside the enum", KindAlert, "severity: warning", "severity: loud", "severity: must be one of"},
		{"setting summary empty", KindSetting, "summary: How long a claim stays reserved.", "summary: \"\"", "summary: must be a non-empty string"},
		{"setting default null", KindSetting, "default: 10", "default: ~", "default: must be a scalar value"},
		{"setting range and values both", KindSetting, "unit: seconds", "values: [a, b]", "carries range or values, not both"},
		{"setting tier default not a scalar", KindSetting, "defaults-by-tier: {small: 10, medium: 10, large: 10}", "defaults-by-tier: {small: [10]}", "the value of \"small\" must be a scalar"},
		{"grant verb outside the enum", KindGrant, "verbs: [create, patch]", "verbs: [create, own]", "\"own\" is not one of"},
		{"grant verbs empty", KindGrant, "verbs: [create, patch]", "verbs: []", "verbs: must be a non-empty list"},
		{"network rule direction outside the enum", KindNetworkRule, "direction: egress", "direction: sideways", "direction: must be one of egress, ingress"},
		{"rbac grant unit a list", KindRBACGrant, "unit: GW", "unit: [GW]", "unit: must be one page code"},
		{"custom resource scope", KindCustomResource, "scope: Namespaced (agent namespace)", "scope: Regional", "does not start with Namespaced or Cluster"},
		{"state initial not a state", KindState, "initial: bound", "initial: born", "initial: \"born\" is not one of the states"},
		{"state terminal not a state", KindState, "terminal: [released]", "terminal: [gone]", "terminal: \"gone\" is not one of the states"},
		{"state transition to an unknown state", KindState, "to: reserved", "to: parked", "transitions: \"parked\" is not one of the states"},
		{"state transition without to", KindState, "{from: bound, to: reserved, on: session end}", "{from: bound, on: session end}", "needs from (a state or a list) and to (a state)"},
		{"state transition with an unknown field", KindState, "on: session end}", "when: later}", "has no field \"when\""},
		{"state transitions not a list", KindState, "transitions:\n  - {from: bound, to: reserved, on: session end}\n  - {from: [bound, reserved], to: released, rule: CAP-R2}\n", "transitions: none\n", "must be a non-empty list of transitions"},
		{"label values a mapping", KindLabel, "values: the pool name", "values: {a: b}", "values: must be a value or a list of values"},
		{"read-by an empty list", KindSetting, "read-by: [WPC]", "read-by: []", "must be a page code or a non-empty list of page codes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := validBlocks[c.kind]
			if !strings.Contains(b[1], c.from) {
				t.Fatalf("fixture for %s lacks %q", c.kind, c.from)
			}
			yaml := strings.Replace(b[1], c.from, c.to, 1)
			assertProblem(t, checkPage(t, blockPage(b[0], yaml)), "spec/caps/page.md", -1, c.want)
		})
	}
}

func TestCheckRefusesAnUnknownKindAndABlockWithNoKind(t *testing.T) {
	t.Parallel()
	assertProblem(t, checkPage(t, blockPage("x", "kind: gadget\nname: x\n")), "spec/caps/page.md", 11, "unknown kind \"gadget\"")
	assertProblem(t, checkPage(t, blockPage("x", "name: x\n")), "spec/caps/page.md", 10, "the block has no kind field")
}

func TestCheckRefusesABlockThatIsNotAMappingOrNotYAML(t *testing.T) {
	t.Parallel()
	assertProblem(t, checkPage(t, blockPage("x", "- a\n- b\n")), "spec/caps/page.md", 10, "is not a YAML mapping")
	assertProblem(t, checkPage(t, blockPage("x", "kind: [unclosed\n")), "spec/caps/page.md", 10, "is not valid YAML")
	assertProblem(t, checkPage(t, blockPage("x", "kind: setting\nkind: metric\n")), "spec/caps/page.md", 12, "field \"kind\" appears twice")
}

func TestCheckRefusesAnUnclosedBlock(t *testing.T) {
	t.Parallel()
	page := "---\ncode: CAP\ntype: capability\nsummary: s\n---\n# Page\n\n```lenny-def\nkind: setting\n"
	assertProblem(t, checkPage(t, page), "spec/caps/page.md", 8, "has no closing fence")
}

func TestCheckRefusesAMissingOrWrongAnchor(t *testing.T) {
	t.Parallel()
	b := validBlocks[KindMetric]
	wrong := blockPage("metric-something-else", b[1])
	assertProblem(t, checkPage(t, wrong), "spec/caps/page.md", 8, "the anchor above the block is \"metric-something-else\"; its kind and name give \"metric-lenny-pod-claim-duration-seconds\"")
	missing := strings.Replace(blockPage(b[0], b[1]), "<a id=\""+b[0]+"\"></a>", "Some prose.", 1)
	assertProblem(t, checkPage(t, missing), "spec/caps/page.md", 10, "needs the anchor line <a id=\"metric-lenny-pod-claim-duration-seconds\"></a>")
}

func TestCheckRefusesANameWithNoLetterOrDigit(t *testing.T) {
	t.Parallel()
	assertProblem(t, checkPage(t, blockPage("x", "kind: limit\nname: \"--\"\nvalue: 1\nsummary: s\n")), "spec/caps/page.md", 12, "holds no letter or digit")
}

func TestCheckRefusesUnregisteredPageCodes(t *testing.T) {
	t.Parallel()
	cases := []struct{ kind, from, to, field string }{
		{KindSetting, "read-by: [WPC]", "read-by: [WPC, NOPE]", "read-by"},
		{KindMetric, "emitter: WPC", "emitter: NOPE", "emitter"},
		{KindEvent, "emitter: [GW]", "emitter: [GHOST]", "emitter"},
		{KindRBACGrant, "unit: GW", "unit: NOPE", "unit"},
		{KindNetworkRule, "unit: GW", "unit: NOPE", "unit"},
		{KindLimit, "enforced-by: WPC", "enforced-by: NOPE", "enforced-by"},
	}
	for _, c := range cases {
		t.Run(c.kind+"/"+c.field, func(t *testing.T) {
			t.Parallel()
			b := validBlocks[c.kind]
			yaml := strings.Replace(b[1], c.from, c.to, 1)
			line := 10 + lineOfField(yaml, c.field)
			assertProblem(t, checkPage(t, blockPage(b[0], yaml)), "spec/caps/page.md", line, "is not a page code any page of the tree registers")
		})
	}
}

// lineOfField returns the 1-based line of a field within a block body.
func lineOfField(yaml, field string) int {
	for i, line := range strings.Split(yaml, "\n") {
		if strings.HasPrefix(line, field+":") {
			return i + 1
		}
	}
	return 0
}

func TestCheckRefusesDuplicateAnchorsAcrossPages(t *testing.T) {
	t.Parallel()
	b := validBlocks[KindSetting]
	files := baseFiles(blockPage(b[0], b[1]))
	other := strings.Replace(blockPage(b[0], b[1]), "code: CAP", "code: CAPTWO", 1)
	files["spec/caps/other.md"] = other
	assertProblem(t, Check(loadTree(t, files)), "spec/caps/page.md", 10, "anchor \"setting-gateway-claimholdttlseconds\" is already defined at spec/caps/other.md:10")
}

func TestCheckRefusesAnAnchorThatABlockATermAndARequirementShare(t *testing.T) {
	t.Parallel()
	page := "---\ncode: CAP\ntype: capability\nsummary: s\n---\n# Page\n\n" +
		"| Term | Definition |\n| :-- | :-- |\n| <a id=\"claim\"></a>Claim | One occupancy. |\n\n" +
		"<a id=\"cap-r1\"></a>**CAP-R1. First.** Text.\n\n"
	files := baseFiles(page)
	files["spec/caps/twin.md"] = "---\ncode: TWIN\ntype: capability\nsummary: s\n---\n# Twin\n\n" +
		"| Term | Definition |\n| :-- | :-- |\n| <a id=\"claim\"></a>Claim | Another occupancy. |\n\n" +
		"<a id=\"cap-r1\"></a>**CAP-R1. Copied.** Text.\n"
	problems := Check(loadTree(t, files))
	assertProblem(t, problems, "spec/caps/twin.md", 10, "anchor \"claim\" is already defined at spec/caps/page.md:10")
	assertProblem(t, problems, "spec/caps/twin.md", 12, "anchor \"cap-r1\" is already defined at spec/caps/page.md:12")
}

func TestCheckRefusesADuplicatePageCode(t *testing.T) {
	t.Parallel()
	files := baseFiles(placeholder("CAP"))
	files["spec/caps/again.md"] = placeholder("CAP")
	assertProblem(t, Check(loadTree(t, files)), "spec/caps/page.md", 1, "page code CAP is already registered by spec/caps/again.md")
}

func TestCheckReportsInvalidFrontMatter(t *testing.T) {
	t.Parallel()
	files := baseFiles(placeholder("CAP"))
	files["spec/caps/broken.md"] = "---\ncode: [unclosed\n---\n"
	assertProblem(t, Check(loadTree(t, files)), "spec/caps/broken.md", 1, "front matter is not valid YAML")
}

func TestCheckIgnoresBlocksInsideAnOuterFenceAndFilesOutsideTheTree(t *testing.T) {
	t.Parallel()
	page := "---\ncode: CAP\ntype: conventions\nsummary: s\n---\n# Conventions\n\n````markdown\n```lenny-def\nkind: gadget\n```\n````\n\n~~~\n```lenny-def\nkind: gadget\n```\n~~~\n"
	files := baseFiles(page)
	files["spec/01_numbered.md"] = "# 1. Numbered\n\n```lenny-def\nkind: gadget\n```\n"
	files["spec/notes.md"] = "---\ntitle: no code\n---\n```lenny-def\nkind: gadget\n```\n"
	tr := loadTree(t, files)
	if problems := Check(tr); len(problems) > 0 {
		t.Fatalf("a block outside the tree or inside an outer fence was read:\n%s", joinProblems(problems))
	}
	if len(tr.Blocks()) != 0 {
		t.Errorf("read %d blocks, want 0", len(tr.Blocks()))
	}
	if tr.PageByPath("spec/notes.md") != nil || tr.PageByPath("spec/01_numbered.md") != nil {
		t.Errorf("a file without a code in its front matter was read as a page")
	}
}

func TestCheckRefusesMalformedGeneratedSectionMarkers(t *testing.T) {
	t.Parallel()
	head := "---\ncode: CAP\ntype: component\nsummary: s\n---\n# Page\n\n"
	cases := []struct {
		name, body, want string
		line             int
	}{
		{"unknown section", "<!-- generated:gadgets -->\n<!-- /generated:gadgets -->\n", "unknown generated section \"gadgets\"", 8},
		{"unclosed", "<!-- generated:settings-read -->\ntext\n", "generated section \"settings-read\" has no closing marker", 8},
		{"close without open", "<!-- /generated:settings-read -->\n", "closing marker for \"settings-read\" has no matching opening marker", 8},
		{"nested", "<!-- generated:settings-read -->\n<!-- generated:metrics-emitted -->\n<!-- /generated:settings-read -->\n", "generated section \"metrics-emitted\" opens inside \"settings-read\"", 9},
		{"twice", "<!-- generated:settings-read -->\n<!-- /generated:settings-read -->\n<!-- generated:settings-read -->\n<!-- /generated:settings-read -->\n", "generated section \"settings-read\" appears twice", 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assertProblem(t, checkPage(t, head+c.body), "spec/caps/page.md", c.line, c.want)
		})
	}
}

func TestCheckRequiresTheSectionABlockFillsUnlessThePageIsAPlaceholder(t *testing.T) {
	t.Parallel()
	b := validBlocks[KindSetting]
	files := baseFiles(blockPage(b[0], b[1]))
	files["spec/components/wpc.md"] = "---\ncode: WPC\ntype: component\nsummary: s\n---\n# WPC\n"
	assertProblem(t, Check(loadTree(t, files)), "spec/components/wpc.md", 0, "the page needs the generated section <!-- generated:settings-read -->")

	files["spec/components/wpc.md"] = "---\ncode: WPC\ntype: component\nsummary: s\n---\n# WPC\n\n<!-- generated:settings-read -->\n<!-- /generated:settings-read -->\n"
	if problems := Check(loadTree(t, files)); len(problems) > 0 {
		t.Errorf("a page with the section was refused:\n%s", joinProblems(problems))
	}
}

func TestSlugAndAnchor(t *testing.T) {
	t.Parallel()
	cases := []struct{ kind, name, code, want string }{
		{KindSetting, "gateway.claimHoldTTLSeconds", "CLAIM", "setting-gateway-claimholdttlseconds"},
		{KindLabel, "lenny.dev/tenant-id", "KLABEL", "label-lenny-dev-tenant-id"},
		{KindSetting, "--create-qps", "WPC", "setting-create-qps"},
		{KindError, "WARM_POOL_EXHAUSTED", "CLAIM", "error-warm-pool-exhausted"},
		{KindRegister, "slot  set", "GWCLAIM", "register-slot-set"},
		{KindOperation, "createPool", "ADMPOOLS", "admpools-createpool"},
		{KindOperation, "ReportPodScrub", "ADAPTERRPC", "adapterrpc-reportpodscrub"},
	}
	for _, c := range cases {
		if got := AnchorFor(c.kind, c.name, c.code); got != c.want {
			t.Errorf("AnchorFor(%s, %q, %s) = %q, want %q", c.kind, c.name, c.code, got, c.want)
		}
	}
	if got := Slug("..."); got != "" {
		t.Errorf("Slug(\"...\") = %q, want empty", got)
	}
}

func TestSchemaTableCoversTheKindsAndFieldsOfTheTaxonomy(t *testing.T) {
	t.Parallel()
	want := map[string][]string{
		KindSetting:          {"name", "type", "default", "range", "values", "unit", "scopes", "defaults-by-tier", "read-by", "summary"},
		KindLimit:            {"name", "value", "summary"},
		KindError:            {"name", "status", "category", "retryable", "summary"},
		KindMetric:           {"name", "type", "unit", "labels", "emitter", "summary"},
		KindAlert:            {"name", "expr", "severity", "for", "runbook", "summary"},
		KindEvent:            {"type", "emitter", "payload", "summary"},
		KindLabel:            {"key", "values", "carriers", "writers", "readers", "mutability"},
		KindAnnotation:       {"key", "values", "carriers", "writers", "readers", "mutability"},
		KindCustomResource:   {"name", "group", "version", "scope"},
		KindOperation:        {"name", "summary"},
		KindState:            {"object", "states", "transitions"},
		KindRegister:         {"name", "store", "keys", "writers", "readers"},
		KindGrant:            {"api-groups", "resources", "verbs", "namespace-scope", "requirement"},
		KindAdmissionRefusal: {"name", "message"},
		KindRBACGrant:        {"unit", "api-groups", "resources", "verbs", "namespace-scope", "requirement"},
		KindNetworkRule:      {"unit", "direction", "peer", "ports", "requirement"},
	}
	for kind, fields := range want {
		k, ok := KindByName(kind)
		if !ok {
			t.Errorf("kind %s is missing", kind)
			continue
		}
		for _, f := range fields {
			if _, ok := k.Field(f); !ok {
				t.Errorf("kind %s has no field %s", kind, f)
			}
		}
		if _, ok := k.Field(k.NameField); !ok {
			t.Errorf("kind %s names %s as its name field but has no such field", kind, k.NameField)
		}
	}
}

func assertProblem(t *testing.T, problems []Problem, path string, line int, want string) {
	t.Helper()
	for _, p := range problems {
		if p.Path == path && (line < 0 || p.Line == line) && strings.Contains(p.Message, want) {
			return
		}
	}
	t.Errorf("no problem at %s:%d containing %q; got:\n%s", path, line, want, joinProblems(problems))
}

func joinProblems(problems []Problem) string {
	lines := make([]string, 0, len(problems))
	for _, p := range problems {
		lines = append(lines, "  "+p.String())
	}
	return strings.Join(lines, "\n")
}

func TestCheckRequiresTheConventionsPageToCarryTheCodeRegistryAndTheKinds(t *testing.T) {
	t.Parallel()
	files := baseFiles(placeholder("CAP"))
	files[ConventionsPath] = "---\ncode: CONV\ntype: conventions\nsummary: s\n---\n# Conventions\n\n<!-- generated:definition-kinds -->\n<!-- /generated:definition-kinds -->\n"
	problems := Check(loadTree(t, files))
	assertProblem(t, problems, ConventionsPath, 0, "needs the generated section <!-- generated:code-registry -->")
	for _, p := range problems {
		if strings.Contains(p.Message, "generated:definition-kinds") {
			t.Errorf("the definition-kinds section was reported although the page carries it: %s", p)
		}
	}

	files[ConventionsPath] = "---\ncode: CONV\ntype: conventions\nsummary: s\n---\n# Conventions\n"
	problems = Check(loadTree(t, files))
	assertProblem(t, problems, ConventionsPath, 0, "generated:code-registry")
	assertProblem(t, problems, ConventionsPath, 0, "generated:definition-kinds")

	files[ConventionsPath] = "---\ncode: CONV\ntype: conventions\nsummary: s\n---\n# Conventions\n\n<!-- generated:definition-kinds -->\n<!-- /generated:definition-kinds -->\n\n<!-- generated:code-registry -->\n<!-- /generated:code-registry -->\n"
	if problems := Check(loadTree(t, files)); len(problems) > 0 {
		t.Errorf("a conventions page with both sections was refused:\n%s", joinProblems(problems))
	}
}
