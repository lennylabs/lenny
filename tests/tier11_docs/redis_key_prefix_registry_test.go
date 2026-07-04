// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the §12.4 canonical Redis
// key-prefix registry (proposal 0032). Proposal 0032 completed the §12.4
// table with the tenant-embedded, session-scoped, and platform-scoped
// prefix families the gateway actively writes (`rl:`, `sq:`,
// `derive_lock:`, `lenny:events:`, the tenant-leading `pg:*` family,
// `pg:sess-tenant:`, `conn:oauth:state:`), scoped the table intro so it no
// longer claims platform-wide completeness, rewrote the exception-class
// summary into two categories with no explicit count, gave the two
// spec-orphaned playground keys (`pg:user`, `pg:sess-tenant`) an
// owning-section home in §27, and reconciled the circuit-breaker
// pub/sub channel to the single literal `cb:events` the code publishes.
//
// These tests pin that reconciliation so a later spec or doc edit cannot
// silently re-introduce an undocumented wired prefix, a stale registry row
// with no emitting code, a fixed exception count, an orphaned key, or a
// second literal channel name. They read the repository state directly (no
// build tag, no infrastructure), the same posture as the other tier-11
// doc checks. The shared helpers repoRoot (docs_test.go), specSection and
// requireAllContain/requireNoneContain (budget_extension_trigger_consistency_test.go)
// are reused rather than redefined.
//
// spec: §12.4 (canonical key-prefix table, exception-class summary),
// §12.6 (infrastructure pub/sub exclusion), §11.4 (user invalidation
// fan-out), §11.6 (circuit breakers), §25.5 (operational event stream),
// §27.3.1 (session record backing store), §27.6 (session lifecycle).

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// wiredGatewayPrefix names one literal Redis key or channel prefix emitted
// by wired gateway code and the documented home it must resolve to. The
// registry-completeness check looks up `prefix` in the §12.4 table body;
// `home` is a human-readable label for the documented home, used only in
// the diagnostic message when a prefix is missing from its registry home.
type wiredGatewayPrefix struct {
	prefix string // the literal wire prefix the gateway code emits
	home   string // human label for the documented home (for diagnostics)
}

// table12_4Prefixes are the prefixes proposal 0032 dispositions as §12.4
// canonical-registry rows. Each MUST appear literally in the §12.4 table
// body.
var table12_4Prefixes = []wiredGatewayPrefix{
	{"rl:{key}:{minute_epoch}", "§12.4 rate-limit row"},
	{"sq:{tenant_id}", "§12.4 storage-quota row"},
	{"cb:{name}", "§12.4 circuit-breaker row"},
	{"lenny:pod:{pod_id}:active_slots", "§12.4 slot-counter row"},
	{"t:{tenant_id}:lease:session:{session_id}", "§12.4 lease row"},
	{"derive_lock:{source_session_id}", "§12.4 derive-lock row"},
	{"lenny:events:{session_id}", "§12.4 relay-stream row"},
	{"t:{tenant_id}:pg:sess:{session_id}", "§12.4 playground row"},
	{"pg:sess-tenant:{session_id}", "§12.4 playground fan-in row"},
	{"conn:oauth:state:{state}", "§12.4 connector-OAuth row"},
	{"{root_session_id}:dlg:tokens", "§12.4 delegation row"},
}

// spec: 12.4 (canonical key-prefix table), 11.4 (user invalidation fan-out),
// 25.5 (operational event stream)
// diagnosis: a Redis key or channel prefix emitted by wired gateway code
// has no documented home, or a §12.4 registry row names a prefix no code
// emits. Proposal 0032 established that every wired gateway prefix resolves
// to exactly one home: a §12.4 table row, the §12.6 "Infrastructure pub/sub
// exclusion" paragraph (`cb:events`), or a documented fan-out mechanism in
// its owning section (`ops:events:stream` in §25.5, `lenny:session:terminate`
// in §11.4). A failure here means the registry drifted from the code — a
// wired prefix went undocumented (the F-0032 completeness defect) or a stale
// row survived a prefix the code no longer emits. The unwired `lenny:route:`
// prefix and the component-owned Token Service / lenny-ops prefixes are
// resolved against their own sections and are excluded from this gateway
// datastore registry.
func TestRedisKeyPrefixRegistryCompleteness_0032(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	s124 := specSection(t, filepath.Join(specDir, "12_storage-architecture.md"), "### 12.4 ")
	s126 := specSection(t, filepath.Join(specDir, "12_storage-architecture.md"), "### 12.6 ")
	s114 := specSection(t, filepath.Join(specDir, "11_policy-and-controls.md"), "### 11.4 ")
	s255 := specSection(t, filepath.Join(specDir, "25_agent-operability.md"), "## 25.5 ")

	// (a) Every §12.4-dispositioned prefix appears as a literal in the §12.4
	// table body.
	for _, p := range table12_4Prefixes {
		if !strings.Contains(s124, p.prefix) {
			t.Errorf("§12.4 registry is missing wired gateway prefix %q (%s); the table must register every prefix wired gateway code emits", p.prefix, p.home)
		}
	}

	// (b) The pub/sub channel `cb:events` resolves to the §12.6
	// "Infrastructure pub/sub exclusion" paragraph, not to a §12.4 row (it is
	// a replica-sync pathway, not a datastore key).
	if !strings.Contains(s126, "Infrastructure pub/sub exclusion") {
		t.Fatalf("§12.6 no longer carries the \"Infrastructure pub/sub exclusion\" paragraph; the cb:events channel lost its documented home")
	}
	if !strings.Contains(s126, "`cb:events`") {
		t.Errorf("§12.6 \"Infrastructure pub/sub exclusion\" paragraph does not name the `cb:events` channel; it is the documented home for the circuit-breaker replica-sync pathway")
	}

	// (c) The platform-scoped `ops:events:stream` resolves to §25.5 and the
	// gateway-internal `lenny:session:terminate` fan-out resolves to the
	// §11.4 full-revoke propagation note; neither is a §12.4 row.
	if !strings.Contains(s255, "`ops:events:stream`") {
		t.Errorf("§25.5 does not name the `ops:events:stream` operational event stream; that gateway-emitted prefix is dispositioned to its owning §25.5 section rather than §12.4")
	}
	if strings.Contains(s124, "ops:events:stream") {
		t.Errorf("§12.4 registers `ops:events:stream`; it is platform-scoped and owned by §25.5, not a tenant-datastore registry row")
	}
	// §11.4 documents the cross-replica Terminate pub/sub fan-out mechanism
	// (the literal channel name lenny:session:terminate is not spelled in the
	// spec; the note describes the mechanism). The disposition is prose, so
	// assert the note describes the Redis pub/sub Terminate fan-out.
	requireAllContain(t, "§11.4 full-revoke propagation note", s114, []string{
		"publishes the step-2 `Terminate` request",
		"Redis pub/sub channels",
	})

	// (d) No §12.4 row names the unwired `lenny:route:` routing-cache prefix
	// (it has no caller under cmd/ or pkg/, so it emits no live key). Its
	// absence from the registry is deliberate.
	if strings.Contains(s124, "lenny:route:") {
		t.Errorf("§12.4 registers the unwired `lenny:route:` routing-cache prefix; it emits no live key (no cmd/ or pkg/ caller) and must not appear in the registry")
	}

	// (e) The component-owned Token Service and lenny-ops prefixes are owned by
	// their own sections (§4.3 / §25.4), not by this gateway datastore
	// registry. The §12.4 table must not register them.
	for _, componentPrefix := range []string{"lenny:token:", "ops:lock:", "ops:escalations:", "ops:lockidx:"} {
		if strings.Contains(s124, componentPrefix) {
			t.Errorf("§12.4 registers component-owned prefix %q; it is documented in its owning §4.3/§25.4 section, not in the gateway datastore registry", componentPrefix)
		}
	}

	// (f) The §12.4 intro makes no platform-wide "lists all keys in use"
	// completeness claim. Proposal 0032 scoped the intro to the tenant-isolation
	// datastore domain the table registers.
	if strings.Contains(s124, "lists all canonical key prefix patterns in use") {
		t.Errorf("§12.4 intro still claims to list \"all canonical key prefix patterns in use\"; the completeness claim overreaches to component-owned keys and was scoped to the tenant-datastore domain by proposal 0032")
	}
	if !strings.Contains(s124, "registers the canonical tenant-scoped key prefix patterns and the documented exceptions") {
		t.Errorf("§12.4 intro no longer scopes the registry to the canonical tenant-scoped prefixes and documented exceptions; the scoped claim from proposal 0032 is missing")
	}
}

// spec: 12.4 (exception-class summary)
// diagnosis: the §12.4 exception-class summary and the table disagree, or
// the summary reasserts a fixed count of exception classes. Proposal 0032
// rewrote the summary into two categories (non-tenant-scoped and
// tenant-scoped-but-not-leading) and removed the stale "three exception
// classes" count. A failure here means the summary omits a non-`t:`-leading
// table row (drifting from the registry the documentation-content rule
// requires the prose and table to agree on) or reintroduced an explicit
// count the doc-style rule bans. Every non-`t:`-leading row must be named
// in the summary, and no count may appear.
func TestRedisKeyPrefixExceptionSummaryAgreesWithTable_0032(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")
	s124 := specSection(t, filepath.Join(specDir, "12_storage-architecture.md"), "### 12.4 ")

	// The summary names every non-`t:`-leading exception the table carries.
	// These are the literal prefixes the summary paragraph must mention.
	requireAllContain(t, "§12.4 exception-class summary", s124, []string{
		"`lenny:pod:{pod_id}:*`",
		"`cb:{name}`",
		"`pg:sess-tenant:{session_id}`",
		"`conn:oauth:state:{state}`",
		"`derive_lock:{source_session_id}`",
		"`lenny:events:{session_id}`",
		"`{root_session_id}:dlg:*`",
		"`rl:{key}:{minute_epoch}`",
		"`sq:{tenant_id}`",
	})

	// The summary uses the two-category framing and names both categories.
	requireAllContain(t, "§12.4 exception-class summary categories", s124, []string{
		"two categories of exception to the tenant-prefix rule",
		"carries no tenant component by design",
		"tenant-scoped but not tenant-leading",
	})

	// The summary states no explicit count of exception classes. The stale
	// "three exception classes" phrasing and any other numeric count of the
	// exceptions must be gone (the doc-style rule bans explicit capability
	// counts). Guard both the exact stale phrase and the generic
	// "<n> exception classes" form.
	requireNoneContain(t, "§12.4 exception-class summary count", s124, []string{
		"three exception classes",
		"two exception classes",
		"exception classes to the tenant-prefix rule",
	})
}

// spec: 27.3.1 (session record backing store), 27.6 (session lifecycle),
// 11.4 (user invalidation)
// diagnosis: a §12.4 registry row for a `pg:*` key has no owning section
// that defines its semantics, or the §27 backing-store intro contradicts
// the platform-scoped `pg:sess-tenant` row directly below it. Proposal 0032
// gave the two spec-orphaned keys (`pg:user`, `pg:sess-tenant`) an
// owning-section home in the §27.3.1 backing-store table, linked the §12.4
// `pg:*` rows back to §27, and reworded the §27 intro to name the
// platform-scoped fan-in index as the documented exception. A failure here
// means an orphaned key regressed to having a registry row with no defined
// semantics, a cross-reference dangles, or the §27 intro reasserts that
// every backing-store row is tenant-prefixed while a platform-scoped row
// sits below it.
func TestPlaygroundKeysOwningSectionResolves_0032(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	s2731 := specSection(t, filepath.Join(specDir, "27_web-playground.md"), "#### 27.3.1 ")
	s124 := specSection(t, filepath.Join(specDir, "12_storage-architecture.md"), "### 12.4 ")

	// (a) The §27.3.1 backing-store table carries rows for pg:user and
	// pg:sess-tenant with their defining semantics.
	requireAllContain(t, "§27.3.1 backing-store table", s2731, []string{
		"`t:{tenant_id}:pg:user:{user_id}`",
		"Per-user playground session index",
		"`pg:sess-tenant:{session_id}`",
		"Session-to-tenant fan-in index",
	})

	// (b) The pg:user row cross-references §11.4/§27.6 (the user-invalidation
	// fan-out) and those headings resolve to real sections.
	requireAllContain(t, "§27.3.1 pg:user cross-references", s2731, []string{
		"11_policy-and-controls.md#114-user-invalidation",
		"#276-session-lifecycle-and-cleanup",
	})
	// The referenced headings exist. §11.4 heading anchors to
	// #114-user-invalidation; §27.6 to #276-session-lifecycle-and-cleanup.
	if specSection(t, filepath.Join(specDir, "11_policy-and-controls.md"), "### 11.4 User Invalidation") == "" {
		t.Errorf("§11.4 User Invalidation heading missing; the §27.3.1 pg:user cross-reference dangles")
	}
	if specSection(t, filepath.Join(specDir, "27_web-playground.md"), "### 27.6 Session lifecycle and cleanup") == "" {
		t.Errorf("§27.6 Session lifecycle and cleanup heading missing; the §27.3.1 pg:user cross-reference dangles")
	}

	// (c) The §12.4 pg:* rows link back to §27.3.1 (the owning section holds
	// the semantics; the registry links to it).
	requireAllContain(t, "§12.4 pg:* rows link back to §27", s124, []string{
		"27_web-playground.md#2731-oidc-cookie-to-mcp-bearer-exchange",
	})
	// The §27.3.1 heading the §12.4 rows link to resolves.
	if specSection(t, filepath.Join(specDir, "27_web-playground.md"), "#### 27.3.1 OIDC cookie-to-MCP-bearer exchange") == "" {
		t.Errorf("§27.3.1 heading missing; the §12.4 pg:* backlinks dangle")
	}

	// (d) The §27 backing-store intro names the platform-scoped pg:sess-tenant
	// exception rather than asserting every row is tenant-prefixed.
	requireAllContain(t, "§27.3.1 backing-store intro", s2731, []string{
		"the platform-scoped `pg:sess-tenant:{session_id}` fan-in index is the documented exception",
	})
	// The intro must not claim the whole backing store is anchored on the
	// per-tenant prefix (the pre-fix overclaim the platform-scoped row
	// contradicts).
	if strings.Contains(s2731, "anchored on the per-tenant prefix convention pinned in") {
		t.Errorf("§27.3.1 intro still claims the backing store is \"anchored on the per-tenant prefix convention\"; the platform-scoped pg:sess-tenant row directly below contradicts that overclaim (proposal 0032)")
	}
}

// spec: 11.6 (circuit breakers), 12.6 (infrastructure pub/sub exclusion)
// diagnosis: two literal names exist for one circuit-breaker pub/sub
// channel across the spec and the code. Proposal 0032 reconciled the two
// spec occurrences (§11.6 and §12.6) to the literal `cb:events` the code
// publishes at cachingstore.go's `const channel`. A failure here means the
// stale `circuit_breaker_events` literal reappeared under spec/, or the
// spec and the code diverged on the channel name again — the divergence the
// proposal closed. The spec must carry the single literal `cb:events`, and
// it must match the code constant.
func TestCircuitBreakerChannelSingleName_0032(t *testing.T) {
	root := repoRoot(t)
	specDir := filepath.Join(root, "spec")

	// (a) The stale literal circuit_breaker_events appears nowhere under spec/.
	specFiles := listSpecMarkdown(t, specDir)
	for _, f := range specFiles {
		body := readDoc(t, f)
		if strings.Contains(body, "circuit_breaker_events") {
			t.Errorf("%s still names the stale channel literal `circuit_breaker_events`; proposal 0032 reconciled every spec occurrence to `cb:events`", filepath.Base(f))
		}
	}

	// (b) cb:events is the single literal name across §11.6 and §12 (§12.6
	// exclusion paragraph). Both sections must name it.
	s116 := specSection(t, filepath.Join(specDir, "11_policy-and-controls.md"), "### 11.6 ")
	s126 := specSection(t, filepath.Join(specDir, "12_storage-architecture.md"), "### 12.6 ")
	if !strings.Contains(s116, "`cb:events`") {
		t.Errorf("§11.6 does not publish state changes on the `cb:events` channel; the reconciled literal is missing")
	}
	if !strings.Contains(s126, "`cb:events`") {
		t.Errorf("§12.6 \"Infrastructure pub/sub exclusion\" paragraph does not name the `cb:events` channel; the reconciled literal is missing")
	}

	// (c) The spec literal matches the code constant at cachingstore.go.
	code := readDoc(t, filepath.Join(root, "pkg", "gateway", "middleware", "circuitbreaker",
		"breakerstore", "cachingstore", "cachingstore.go"))
	if !strings.Contains(code, `channel = "cb:events"`) {
		t.Errorf("cachingstore.go no longer defines `const channel = \"cb:events\"`; the spec and code channel names diverged again (proposal 0032)")
	}
}

// listSpecMarkdown returns every .md file directly under spec/, so the
// single-channel-name check can sweep the whole spec for the stale literal
// rather than a single hand-listed section.
func listSpecMarkdown(t *testing.T, specDir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(specDir, "*.md"))
	if err != nil {
		t.Fatalf("glob spec markdown: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no spec markdown found under %s", specDir)
	}
	return matches
}
