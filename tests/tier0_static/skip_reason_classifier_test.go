// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/cmd/lenny-test/skipreason"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The skip-reason classifier holds every skip call site in the Go tree
// to the reason categories TESTING.md §17.9 names. A skip whose reason
// states nothing a reader can act on is a test that reports neither pass
// nor fail and names no route back, which is the state the categories
// exist to make visible.
//
// It reads the syntax tree rather than the source line. The convention
// was carried by a shell block whose allow pattern matched any formatted
// skip call regardless of its reason, so a formatted call could carry
// any text at all, and the check ran downgraded to a warning that also
// passed when its script was absent. Parsing the call is what makes the
// predicate the convention rather than the spelling of the line, and
// running here makes it fatal at tier 0.
//
// A site the classifier reports is either held by
// tests/registers/skip-reasons.yaml or fails the run. The register is a
// baseline rewritten downward only: a reason rewritten into a category
// drops its entry in the same run, and no run adds one, so the
// population the classifier landed against cannot regrow.

// skipRegisterPath is the repo-relative path of the classifier's
// register.
const skipRegisterPath = "tests/registers/skip-reasons.yaml"

// The declaration the register carries. It is a baseline rather than an
// exception register (tests/registers/README.md): a skip that fires
// because a host capability is absent names no pending item a blocker
// could resolve to and no date on which it becomes wrong, so the shared
// entry schema does not fit it.
const (
	skipRegisterKind    = "skip-reason-baseline"
	skipRegisterVersion = 1
)

// skipDomainPrefixes are the trees the classifier reads. The shell block
// it replaces read the test tree and the library tree, so the command
// tree was ungoverned and its skip call sites enter the population here
// for the first time.
var skipDomainPrefixes = []string{"cmd/", "pkg/", "tests/"}

// The call forms the classifier reads. A helper whose name opens with
// SkipUnless gates a test on an infrastructure precondition and states
// its reason in its own body, so the call itself carries no reason to
// classify.
const (
	skipCallPlain     = "Skip"
	skipCallFormatted = "Skipf"
	skipHelperPrefix  = "SkipUnless"
)

// skipSite is one skip call site the classifier reports, and one entry
// of the register.
//
// The site is keyed by its file, the function that holds it, and its
// ordinal among the skip call sites of that function. The line the call
// sits on moves whenever anything above it is edited, so a register
// keyed on the line would fail the run on an edit that changed no skip.
// The ordinal counts every skip call site the classifier inspects rather
// than the reported ones alone, so rewriting one reason into a category
// leaves the ordinals of the sites below it where they were.
type skipSite struct {
	// Path is the repo-relative, slash-separated file.
	Path string `yaml:"path"`
	// Function is the function declaration holding the call, with its
	// receiver when it has one.
	Function string `yaml:"function"`
	// Site is the call's 1-based ordinal among the skip call sites of
	// that function.
	Site int `yaml:"site"`
	// Call is the call form, either Skip or Skipf.
	Call string `yaml:"call"`
	// Reason is the string literal the call passes. It is nil when the
	// first argument is not a string literal, which is the branch the
	// classifier reports because it cannot read the reason at all.
	Reason *string `yaml:"reason,omitempty"`
	// Line is where the call sits, for a report. It is outside the key
	// and outside the register.
	Line int `yaml:"-"`
}

// key identifies the site across runs.
func (s skipSite) key() string {
	return s.Path + "\x00" + s.Function + "\x00" + strconv.Itoa(s.Site)
}

// reasonText renders the reason for a message.
func (s skipSite) reasonText() string {
	if s.Reason == nil {
		return "a reason built from a non-literal expression"
	}
	return strconv.Quote(*s.Reason)
}

// String renders the site, naming the file and the line so a reader can
// open the call, and the key so a reader can find its register entry.
func (s skipSite) String() string {
	return fmt.Sprintf("%s:%d: t.%s in %s (site %d) carries %s",
		s.Path, s.Line, s.Call, s.Function, s.Site, s.reasonText())
}

// skipFailure is one reported site the register does not hold.
type skipFailure struct {
	// Site is the site the run read.
	Site skipSite
	// Absent reports that the register carries no entry under the site's
	// key, so the failure is a site the run would have to add.
	Absent bool
	// Registered is the entry the register carries under that key, and is
	// unset when Absent is set.
	Registered *skipSite
}

// String renders the failure for the gate's message.
func (f skipFailure) String() string {
	if f.Absent {
		return fmt.Sprintf("%s, and %s carries no entry for it", f.Site, skipRegisterPath)
	}
	return fmt.Sprintf("%s, where %s carries t.%s with %s",
		f.Site, skipRegisterPath, f.Registered.Call, f.Registered.reasonText())
}

// skipReport is what one run of the classifier measured.
type skipReport struct {
	// Files is the number of Go files inside the classifier's domain.
	Files int
	// Sites is the number of skip call sites inspected across them,
	// including the accepted ones.
	Sites int
	// Reported is the number of sites the predicate rejects, each of them
	// either held by the register or a failure.
	Reported int
	// Carried is how many entries the register held when the run loaded
	// it. It reaches zero when the last reason is rewritten.
	Carried int
	// Held is how many of those entries the run read again unchanged.
	Held int
	// Removed is every entry the run dropped, because its site carries a
	// category now, because its reason changed, or because the site left
	// the tree. Together with Held it accounts for every entry Carried
	// counts, because an entry the run reads again unchanged is held and
	// every other entry is dropped.
	Removed []skipSite
	// Failures is every reported site the register does not hold. A
	// non-empty list is the gate's failure.
	Failures []skipFailure
}

// skipClassifier is the tier-0 skip-reason gate. Its dependencies are
// injected so a case drives it over a fixture tree.
type skipClassifier struct {
	// List enumerates the tracked tree.
	List scope.Lister
	// Read reads a repo-relative tracked path.
	Read scope.FileReader
	// Write writes a repo-relative tracked path. It is used for the
	// downward rewrite of the register alone.
	Write func(path string, content []byte) error
	// Register is the repo-relative path of the register.
	Register string
}

// newSkipClassifier returns the gate over the tracked tree at root.
func newSkipClassifier(root string) *skipClassifier {
	return &skipClassifier{
		List:     scope.GitLister(root),
		Read:     scope.DirReader(root),
		Write:    scope.DirWriter(root),
		Register: skipRegisterPath,
	}
}

// newSkipClassifierOver returns the gate with each dependency supplied,
// so a caller drives it over a tree other than a git checkout.
func newSkipClassifierOver(list scope.Lister, read scope.FileReader, write func(string, []byte) error) *skipClassifier {
	return &skipClassifier{List: list, Read: read, Write: write, Register: skipRegisterPath}
}

// Run classifies every skip call site in the domain and compares the
// reported ones with the register.
//
// A run that could not inspect the tree returns an error rather than an
// empty report. A walk that selected no Go file, a scan that inspected
// no skip call site, a file that does not parse, and a missing or
// malformed register are all states in which a report of no failure
// would certify content the run never classified.
//
// A run that reports no failure rewrites the register downward in the
// same run, so a rewritten reason cannot return under the entry it used
// to hold. The rewrite never adds an entry, so a new site fails the
// run, and a run that fails writes nothing at all.
func (c *skipClassifier) Run(ctx context.Context) (skipReport, error) {
	var rep skipReport
	if c.List == nil || c.Read == nil {
		return rep, fmt.Errorf("skip-reason classifier: a lister and a reader are required")
	}
	domain, err := c.domain(ctx)
	if err != nil {
		return rep, err
	}
	carried, err := loadSkipRegister(c.Read, c.Register)
	if err != nil {
		return rep, fmt.Errorf("skip-reason classifier: %w", err)
	}
	sites, reported, err := c.scan(ctx, domain)
	if err != nil {
		return rep, err
	}
	if sites == 0 {
		return rep, fmt.Errorf("skip-reason classifier: %d Go file(s) hold no skip call site; "+
			"a run that classified nothing does not certify the tree", len(domain))
	}
	rep.Files = len(domain)
	rep.Sites = sites
	rep.Reported = len(reported)
	rep.Carried = len(carried)

	kept := c.compare(reported, carried, &rep)
	// The rewrite lands on a clean run alone. A run that reports a
	// failure leaves the register byte-identical, so the gate measures
	// the next run against the same baseline, reports the same site,
	// and reverting the edit that failed it restores green.
	if len(rep.Failures) == 0 && len(rep.Removed) > 0 {
		if err := writeSkipRegister(c.Write, c.Register, kept); err != nil {
			return rep, fmt.Errorf("skip-reason classifier: %w", err)
		}
	}
	return rep, nil
}

// domain returns the Go files the classifier reads, sorted. It fails on
// a domain that selected no Go file, because every later count would
// then be taken from a tree the run never opened.
func (c *skipClassifier) domain(ctx context.Context) ([]string, error) {
	readable, err := scope.ReadDomain(ctx, c.List)
	if err != nil {
		return nil, fmt.Errorf("skip-reason classifier: %w", err)
	}
	domain := make([]string, 0, len(readable))
	for _, target := range readable {
		if inSkipDomain(target) {
			domain = append(domain, target)
		}
	}
	if len(domain) == 0 {
		return nil, fmt.Errorf("skip-reason classifier: %d readable path(s) hold no Go file under %s",
			len(readable), strings.Join(skipDomainPrefixes, ", "))
	}
	return domain, nil
}

// inSkipDomain reports whether a readable path is a Go file in one of
// the trees the classifier governs.
func inSkipDomain(target string) bool {
	if !strings.HasSuffix(target, ".go") {
		return false
	}
	for _, prefix := range skipDomainPrefixes {
		if strings.HasPrefix(target, prefix) {
			return true
		}
	}
	return false
}

// scan classifies every file in the domain, returning how many skip call
// sites it inspected and the sites the predicate reports, in file order.
func (c *skipClassifier) scan(ctx context.Context, domain []string) (int, []skipSite, error) {
	inspected := 0
	var reported []skipSite
	for _, target := range domain {
		if err := ctx.Err(); err != nil {
			return 0, nil, fmt.Errorf("skip-reason classifier: %w", err)
		}
		data, err := c.Read(target)
		if err != nil {
			return 0, nil, fmt.Errorf("skip-reason classifier: read %s: %w", target, err)
		}
		sites, found, err := classifySkipsInFile(target, data)
		if err != nil {
			return 0, nil, fmt.Errorf("skip-reason classifier: %w", err)
		}
		inspected += sites
		reported = append(reported, found...)
	}
	return inspected, reported, nil
}

// compare measures each reported site against the register, fills in the
// report, and returns the entries the rewritten register holds.
//
// A site whose entry no longer matches is failed against the entry the
// register holds, and a site with no entry is failed as one the run
// would have to add. Neither adds anything. Both leave the register
// untouched, because Run writes only when the run reports no failure.
func (c *skipClassifier) compare(reported []skipSite, carried map[string]skipSite, rep *skipReport) []skipSite {
	kept := make([]skipSite, 0, len(carried))
	matched := map[string]bool{}
	for _, site := range reported {
		entry, ok := carried[site.key()]
		if !ok {
			rep.Failures = append(rep.Failures, skipFailure{Site: site, Absent: true})
			continue
		}
		if !sameSkipReason(entry, site) {
			registered := entry
			rep.Failures = append(rep.Failures, skipFailure{Site: site, Registered: &registered})
			continue
		}
		matched[site.key()] = true
		rep.Held++
		kept = append(kept, entry)
	}
	// An entry whose site carries a category now, whose reason changed,
	// or that left the tree is dropped.
	for _, key := range sortedSkipKeys(carried) {
		if !matched[key] {
			rep.Removed = append(rep.Removed, carried[key])
		}
	}
	sortSkipSites(rep.Removed)
	return kept
}

// sameSkipReason reports whether a register entry describes the site the
// run read. A reason the classifier cannot read matches only another
// such reason, so a non-literal site that becomes a free-text literal is
// reported rather than held.
func sameSkipReason(entry, site skipSite) bool {
	if entry.Call != site.Call {
		return false
	}
	if entry.Reason == nil || site.Reason == nil {
		return entry.Reason == nil && site.Reason == nil
	}
	return *entry.Reason == *site.Reason
}

// classifySkipsInFile parses one Go file and returns how many skip call
// sites it holds together with the sites the predicate reports.
//
// A file that does not parse is an error rather than a file with no
// site: a classifier that passed over what it could not read would go
// green on the file a rewrite broke.
func classifySkipsInFile(path string, src []byte) (int, []skipSite, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return 0, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	inspected := 0
	var reported []skipSite
	ordinals := map[string]int{}
	for _, decl := range file.Decls {
		fn := enclosingFuncName(decl)
		ast.Inspect(decl, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			form, ok := skipCallForm(call)
			if !ok {
				return true
			}
			inspected++
			ordinals[fn]++
			if form != skipCallPlain && form != skipCallFormatted {
				// A helper gating a test on an infrastructure
				// precondition states its reason in its own body, where
				// the classifier reads it as an ordinary call.
				return true
			}
			site := skipSite{
				Path:     path,
				Function: fn,
				Site:     ordinals[fn],
				Call:     form,
				Line:     fset.Position(call.Lparen).Line,
			}
			reason, literal, stated := skipReason(call)
			switch {
			case !stated:
				// A bare skip states no reason to classify.
				return true
			case !literal:
				reported = append(reported, site)
			case !openedWithSkipCategory(reason):
				held := reason
				site.Reason = &held
				reported = append(reported, site)
			}
			return true
		})
	}
	return inspected, reported, nil
}

// enclosingFuncName names the declaration holding a call, with the
// receiver type when it is a method. A call outside a function
// declaration, in a package-level initializer, is keyed under the empty
// name.
func enclosingFuncName(decl ast.Decl) string {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok {
		return ""
	}
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return receiverTypeName(fn.Recv.List[0].Type) + "." + fn.Name.Name
}

// receiverTypeName renders a method receiver's type.
func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return "*" + receiverTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	}
	return "?"
}

// skipCallForm reports the call form when the call is a skip call site.
// The decision is keyed on the selected name alone, whatever expression
// the handle is reached through: a test handle is spelled variously
// across the tree, as a bare identifier, as a field of a case or harness
// struct, or as an element of a slice. Holding a Skip method on some
// other type to the same predicate costs that site a category or a
// register entry, while reading only the bare-identifier spelling would
// leave every other spelling of the convention unenforced, so the wider
// match is the one that fails closed.
func skipCallForm(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	name := sel.Sel.Name
	if strings.HasPrefix(name, skipHelperPrefix) {
		return name, true
	}
	if name == skipCallPlain || name == skipCallFormatted {
		return name, true
	}
	return "", false
}

// skipReason reads the reason a skip call states. It reports the literal
// text, whether the first argument is readable as a string literal, and
// whether the call states a first argument at all.
func skipReason(call *ast.CallExpr) (string, bool, bool) {
	if len(call.Args) == 0 {
		return "", false, false
	}
	text, ok := constantStringExpr(call.Args[0])
	if !ok {
		return "", false, true
	}
	return text, true, true
}

// constantStringExpr reads an expression whose text is fixed at parse
// time, which is a string literal or a concatenation of string literals.
// A reason a source line is too long to hold is spelled as a
// concatenation across several lines, and its text is as readable as a
// single literal's, so the predicate applies to it rather than the
// non-literal branch. Reporting it as unreadable would both baseline
// reasons that already open with a category and stop pinning the reason
// text of the ones that do not.
func constantStringExpr(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return constantStringExpr(e.X)
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		return text, true
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, ok := constantStringExpr(e.X)
		if !ok {
			return "", false
		}
		right, ok := constantStringExpr(e.Y)
		if !ok {
			return "", false
		}
		return left + right, true
	}
	return "", false
}

// openedWithSkipCategory reports whether a reason opens with one of the
// categories. The categories come from cmd/lenny-test/skipreason, which
// the scaffold-marker reader in cmd/lenny-test reads as well, so one
// convention has one enumeration and widening a category cannot land in
// one reader alone.
func openedWithSkipCategory(reason string) bool {
	return skipreason.OpenedWithCategory(reason)
}

// sortedSkipKeys returns the register's keys in a stable order.
func sortedSkipKeys(carried map[string]skipSite) []string {
	keys := make([]string, 0, len(carried))
	for k := range carried {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sortSkipSites orders sites by file, then function, then ordinal.
func sortSkipSites(sites []skipSite) {
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].Path != sites[j].Path {
			return sites[i].Path < sites[j].Path
		}
		if sites[i].Function != sites[j].Function {
			return sites[i].Function < sites[j].Function
		}
		return sites[i].Site < sites[j].Site
	})
}

// skipRegisterDoc is the register's schema. Sites is a pointer so a
// document that declares no sites block is distinguishable from one that
// declares an empty list; the first is malformed and the second is a
// tree in which every reason carries a category.
type skipRegisterDoc struct {
	Kind    string      `yaml:"kind"`
	Version int         `yaml:"version"`
	Sites   *[]skipSite `yaml:"sites"`
}

// loadSkipRegister reads and validates the register. A missing,
// unreadable, or malformed register is an error rather than an empty
// set: a load that degraded to carrying nothing would fail every site in
// the tree, and one that degraded to carrying everything would exempt
// the growth the gate exists to stop.
func loadSkipRegister(read scope.FileReader, path string) (map[string]skipSite, error) {
	if read == nil {
		return nil, fmt.Errorf("read skip-reason register %s: no reader is configured", path)
	}
	data, err := read(path)
	if err != nil {
		return nil, fmt.Errorf("read skip-reason register %s: %w", path, err)
	}
	var doc skipRegisterDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse skip-reason register %s: %w", path, err)
	}
	if doc.Kind != skipRegisterKind {
		return nil, fmt.Errorf("skip-reason register %s: expected kind %q, got %q", path, skipRegisterKind, doc.Kind)
	}
	if doc.Version != skipRegisterVersion {
		return nil, fmt.Errorf("skip-reason register %s: expected version %d, got %d",
			path, skipRegisterVersion, doc.Version)
	}
	if doc.Sites == nil {
		return nil, fmt.Errorf("skip-reason register %s: carries no sites block", path)
	}
	carried := map[string]skipSite{}
	for _, entry := range *doc.Sites {
		if err := validSkipEntry(path, entry); err != nil {
			return nil, err
		}
		if _, ok := carried[entry.key()]; ok {
			return nil, fmt.Errorf("skip-reason register %s: %s in %s (site %d) is declared twice",
				path, entry.Path, entry.Function, entry.Site)
		}
		carried[entry.key()] = entry
	}
	return carried, nil
}

// validSkipEntry holds one entry to the schema.
func validSkipEntry(path string, entry skipSite) error {
	if strings.TrimSpace(entry.Path) == "" {
		return fmt.Errorf("skip-reason register %s: carries an entry with no path", path)
	}
	if entry.Site < 1 {
		return fmt.Errorf("skip-reason register %s: %s carries an entry with site %d, and a site is 1-based",
			path, entry.Path, entry.Site)
	}
	if entry.Call != skipCallPlain && entry.Call != skipCallFormatted {
		return fmt.Errorf("skip-reason register %s: %s in %s (site %d) carries call %q, want %q or %q",
			path, entry.Path, entry.Function, entry.Site, entry.Call, skipCallPlain, skipCallFormatted)
	}
	if entry.Reason != nil && openedWithSkipCategory(*entry.Reason) {
		return fmt.Errorf("skip-reason register %s: %s in %s (site %d) carries reason %q, which opens with a category "+
			"and needs no entry", path, entry.Path, entry.Function, entry.Site, *entry.Reason)
	}
	return nil
}

// writeSkipRegister writes the register holding exactly the entries
// given. It is the one writer of the file: the downward rewrite calls it
// with the entries that survived a run, and the seeding of the register
// calls it with the sites the classifier itself read, so the file is
// never hand-assembled from a population stated elsewhere.
func writeSkipRegister(write func(string, []byte) error, path string, sites []skipSite) error {
	if write == nil {
		return fmt.Errorf("rewrite skip-reason register %s: no writer is configured", path)
	}
	sortSkipSites(sites)
	entries := make([]skipSite, 0, len(sites))
	for i, s := range sites {
		if i > 0 && sites[i-1].key() == s.key() {
			return fmt.Errorf("rewrite skip-reason register %s: %s in %s (site %d) is written twice",
				path, s.Path, s.Function, s.Site)
		}
		entries = append(entries, s)
	}
	doc := skipRegisterDoc{Kind: skipRegisterKind, Version: skipRegisterVersion, Sites: &entries}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encode skip-reason register %s: %w", path, err)
	}
	var b strings.Builder
	b.WriteString(skipRegisterHeader)
	b.Write(body)
	if err := write(path, []byte(b.String())); err != nil {
		return fmt.Errorf("rewrite skip-reason register %s: %w", path, err)
	}
	return nil
}

// skipRegisterHeader explains the file to a reader who opens it without
// the classifier beside them.
const skipRegisterHeader = `# SPDX-License-Identifier: MIT
#
# Skip call sites whose reason states no category.
#
# Each entry is a t.Skip or t.Skipf call whose reason opened with none of
# the categories TESTING.md §17.9 names, or whose reason the classifier
# could not read because the argument is not a string literal, when the
# classifier landed. A site is keyed by its file, the function holding
# it, and its ordinal among that function's skip call sites, because the
# line a call sits on moves with any edit above it.
#
# The classifier rewrites this file downward on a run that reports no
# failure: an entry whose reason gains a category or whose site left the
# tree is dropped in the same run, and no run adds one, so a site that is
# not here fails tier 0 rather than being absorbed. A run that fails
# leaves the file byte-identical, so reverting the edit that failed it
# restores green. When every entry has been rewritten the file is empty
# and the gate is a flat prohibition.
`

// skipTree is a fixture tree the classifier runs over. It holds
// whichever Go files a case writes and the register at the path the gate
// reads.
type skipTree struct {
	t    *testing.T
	root string
}

// newSkipTree returns an empty fixture tree.
func newSkipTree(t *testing.T) *skipTree {
	t.Helper()
	return &skipTree{t: t, root: t.TempDir()}
}

// file writes a file into the tree.
func (tr *skipTree) file(target, content string) {
	tr.t.Helper()
	full := filepath.Join(tr.root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		tr.t.Fatalf("create the directory for %s: %v", target, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		tr.t.Fatalf("write %s: %v", target, err)
	}
}

// carrier writes a Go file holding the given statements inside one test
// function.
func (tr *skipTree) carrier(target string, statements ...string) {
	tr.t.Helper()
	tr.file(target, skipCarrierSource(skipCarrierFunc{Name: "TestCarrier", Statements: statements}))
}

// register writes the register holding the given entries.
func (tr *skipTree) register(sites ...skipSite) {
	tr.t.Helper()
	// The writer preserves the mode of an existing file and creates no
	// directory, so the register is placed before it is written.
	tr.file(skipRegisterPath, "")
	if err := writeSkipRegister(scope.DirWriter(tr.root), skipRegisterPath, sites); err != nil {
		tr.t.Fatalf("seed the fixture register: %v", err)
	}
}

// rawRegister writes the register verbatim, for a document the writer
// would not produce.
func (tr *skipTree) rawRegister(body string) {
	tr.t.Helper()
	tr.file(skipRegisterPath, body)
}

// removeRegister deletes the register from the tree.
func (tr *skipTree) removeRegister() {
	tr.t.Helper()
	if err := os.Remove(filepath.Join(tr.root, filepath.FromSlash(skipRegisterPath))); err != nil {
		tr.t.Fatalf("remove the fixture register: %v", err)
	}
}

// registerText returns the register as it stands in the tree.
func (tr *skipTree) registerText() string {
	tr.t.Helper()
	body, err := os.ReadFile(filepath.Join(tr.root, filepath.FromSlash(skipRegisterPath)))
	if err != nil {
		tr.t.Fatalf("read the fixture register: %v", err)
	}
	return string(body)
}

// run drives the classifier over the fixture tree.
func (tr *skipTree) run() (skipReport, error) {
	tr.t.Helper()
	c := newSkipClassifierOver(
		scope.DirLister(tr.root), scope.DirReader(tr.root), scope.DirWriter(tr.root),
	)
	return c.Run(context.Background())
}

// runOK drives the classifier and fails the test when the run could not
// inspect the tree.
func (tr *skipTree) runOK() skipReport {
	tr.t.Helper()
	rep, err := tr.run()
	if err != nil {
		tr.t.Fatalf("run the skip-reason classifier over the fixture tree: %v", err)
	}
	return rep
}

// skipCarrierFunc is one test function of a fixture carrier.
type skipCarrierFunc struct {
	Name       string
	Statements []string
}

// skipCarrierSource renders a Go test file holding the given functions.
// The classifier reads the syntax tree, so a case states the calls it
// exercises as source rather than as a fixture of its own.
func skipCarrierSource(funcs ...skipCarrierFunc) string {
	var b strings.Builder
	b.WriteString("package carrier\n\nimport \"testing\"\n")
	for _, fn := range funcs {
		b.WriteString("\nfunc " + fn.Name + "(t *testing.T) {\n")
		for _, s := range fn.Statements {
			b.WriteString("\t" + s + "\n")
		}
		b.WriteString("}\n")
	}
	return b.String()
}

// skipFailureTexts renders a report's failures for an assertion message.
func skipFailureTexts(rep skipReport) []string {
	out := make([]string, 0, len(rep.Failures))
	for _, f := range rep.Failures {
		out = append(out, f.String())
	}
	return out
}

// literalSkipReason returns a pointer to the reason, for an entry the
// classifier read as a string literal.
func literalSkipReason(reason string) *string { return &reason }

func TestSkipReasonClassifierCertifiesTheTree(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	rep, err := newSkipClassifier(root).Run(context.Background())
	if err != nil {
		t.Fatalf("run the skip-reason classifier over the tracked tree: %v", err)
	}
	if len(rep.Failures) > 0 {
		t.Errorf("%d skip call site(s) state a reason %s does not carry:\n  %s",
			len(rep.Failures), skipRegisterPath, strings.Join(skipFailureTexts(rep), "\n  "))
	}
	// A walk that read a fraction of the tree reports no failure, so the
	// file and site counts are asserted rather than taken as a clean
	// tree. The reported count and the register are not asserted to be
	// non-zero: both reach zero when the last reason is rewritten, which
	// is the end state the register drives toward.
	if rep.Files == 0 || rep.Sites == 0 {
		t.Errorf("the classifier read %d file(s) and %d skip call site(s); a report of no failure over an "+
			"unread tree certifies nothing", rep.Files, rep.Sites)
	}
	// Every entry the register carried is accounted for by the run,
	// either held or removed. The identity holds at zero, so it also
	// holds once the population empties.
	if rep.Held+len(rep.Removed) != rep.Carried {
		t.Errorf("the register carried %d entr(ies), of which %d held and %d were removed; "+
			"the run accounted for a different number than it loaded",
			rep.Carried, rep.Held, len(rep.Removed))
	}
	t.Logf("%d Go file(s) in the domain, %d skip call site(s) inspected, %d reported, %d carried, %d held, %d removed",
		rep.Files, rep.Sites, rep.Reported, rep.Carried, rep.Held, len(rep.Removed))
	for _, removed := range rep.Removed {
		t.Logf("removed %s in %s (site %d)", removed.Path, removed.Function, removed.Site)
	}
}

func TestSkipReasonClassifierAcceptsEveryCategoryInBothCallForms(t *testing.T) {
	t.Parallel()
	for _, category := range skipreason.Categories {
		for _, call := range []string{skipCallPlain, skipCallFormatted} {
			t.Run(category+"/"+call, func(t *testing.T) {
				t.Parallel()
				tr := newSkipTree(t)
				statement := fmt.Sprintf("t.%s(%q)", call, category+" the reason continues here")
				if call == skipCallFormatted {
					statement = fmt.Sprintf("t.%s(%q, 1)", call, category+" %d")
				}
				tr.carrier("tests/carrier/carrier_test.go", statement)
				tr.register()

				rep := tr.runOK()
				if rep.Sites != 1 {
					t.Fatalf("inspected %d skip call site(s), want the one the carrier holds", rep.Sites)
				}
				if rep.Reported != 0 || len(rep.Failures) != 0 {
					t.Errorf("a reason opening with %q was reported: %s",
						category, strings.Join(skipFailureTexts(rep), "; "))
				}
			})
		}
	}
}

func TestSkipReasonClassifierAcceptsAHelperCallAndABareSkip(t *testing.T) {
	t.Parallel()
	for name, statement := range map[string]string{
		"a helper gating on a precondition": "kind.SkipUnlessAvailable(t)",
		"a bare skip":                       "t.Skip()",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tr := newSkipTree(t)
			tr.carrier("tests/carrier/carrier_test.go", statement)
			tr.register()

			rep := tr.runOK()
			// The call is inspected and then accepted, rather than passed
			// over: a call the walk never reached would leave the run with
			// no site at all, which is the state Run refuses.
			if rep.Sites != 1 {
				t.Fatalf("inspected %d skip call site(s), want the one %q holds", rep.Sites, statement)
			}
			if rep.Reported != 0 || len(rep.Failures) != 0 {
				t.Errorf("%q was reported: %s", statement, strings.Join(skipFailureTexts(rep), "; "))
			}
		})
	}
}

func TestSkipReasonClassifierRejectsAFreeTextReasonInBothCallForms(t *testing.T) {
	t.Parallel()
	// The handle a skip is called on is spelled variously across the tree.
	// A reason reached through a struct field, a nested field, or a slice
	// element states the same thing as one called on a bare identifier, so
	// each spelling is inspected and reported rather than passed over.
	for _, tc := range []struct {
		name      string
		call      string
		statement string
	}{
		{"a bare handle", skipCallPlain, `t.Skip("docker is not running")`},
		{"a bare handle, formatted", skipCallFormatted, `t.Skipf("docker is not running: %v", 1)`},
		{"a handle held by a case", skipCallPlain, `tc.t.Skip("docker is not running")`},
		{"a handle held by a nested harness", skipCallFormatted, `env.harness.tb.Skipf("docker is not running: %v", 1)`},
		{"a handle held by a slice", skipCallPlain, `handles[i].Skip("docker is not running")`},
		{"a handle returned by a call", skipCallPlain, `harness(t).Skip("docker is not running")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := newSkipTree(t)
			tr.carrier("tests/carrier/carrier_test.go", tc.statement)
			tr.register()

			rep := tr.runOK()
			if len(rep.Failures) != 1 {
				t.Fatalf("reported %d failure(s) for a free-text reason, want 1: %s",
					len(rep.Failures), strings.Join(skipFailureTexts(rep), "; "))
			}
			got := rep.Failures[0]
			if !got.Absent || got.Site.Call != tc.call || got.Site.Path != "tests/carrier/carrier_test.go" {
				t.Errorf("the failure reads %+v, want the free-text reason in the %s call form", got.Site, tc.call)
			}
			// The message opens the call for a reader, so it names the
			// file and the line the call sits on.
			if rendered := got.String(); !strings.Contains(rendered, "tests/carrier/carrier_test.go:6") {
				t.Errorf("the rendered failure %q does not name the file and line of the call", rendered)
			}
		})
	}
}

func TestSkipReasonClassifierReportsANonLiteralReason(t *testing.T) {
	t.Parallel()
	tr := newSkipTree(t)
	tr.carrier("tests/carrier/carrier_test.go", `t.Skip(reasonFor(t))`)
	tr.register()

	rep := tr.runOK()
	if len(rep.Failures) != 1 {
		t.Fatalf("reported %d failure(s) for a reason built from an expression, want 1: %s",
			len(rep.Failures), strings.Join(skipFailureTexts(rep), "; "))
	}
	// A reason the classifier cannot read is reported against the
	// register rather than accepted. Accepting it would leave the hole
	// the shell block carried: any reason at all passes as long as it is
	// not written as a literal.
	got := rep.Failures[0]
	if got.Site.Reason != nil {
		t.Errorf("the failure carries reason %q, want a site with no readable reason", *got.Site.Reason)
	}
	if !strings.Contains(got.String(), "non-literal") {
		t.Errorf("the rendered failure %q does not state that the reason could not be read", got.String())
	}
}

func TestSkipReasonClassifierReadsAReasonSpelledAsAConcatenation(t *testing.T) {
	t.Parallel()
	// A reason too long for one source line is spelled as a concatenation
	// of literals. Its text is fixed at parse time, so the predicate reads
	// it: a concatenation opening with a category is accepted, and a
	// free-text one is reported with its text pinned rather than dropped
	// into the branch for reasons the classifier cannot read.
	t.Run("a category spelled across two literals is accepted", func(t *testing.T) {
		t.Parallel()
		tr := newSkipTree(t)
		tr.carrier("tests/carrier/carrier_test.go",
			`t.Skip("phase-gated: the hardening pass " +`,
			`"has not landed yet")`)
		tr.register()

		rep := tr.runOK()
		if rep.Sites != 1 {
			t.Fatalf("inspected %d skip call site(s), want the one the carrier holds", rep.Sites)
		}
		if rep.Reported != 0 || len(rep.Failures) != 0 {
			t.Errorf("a concatenated reason opening with a category was reported: %s",
				strings.Join(skipFailureTexts(rep), "; "))
		}
	})
	t.Run("free text spelled across two literals is reported with its text", func(t *testing.T) {
		t.Parallel()
		tr := newSkipTree(t)
		tr.carrier("tests/carrier/carrier_test.go",
			`t.Skip("docker is not " +`,
			`"running on the host")`)
		tr.register()

		rep := tr.runOK()
		if len(rep.Failures) != 1 {
			t.Fatalf("reported %d failure(s) for a concatenated free-text reason, want 1: %s",
				len(rep.Failures), strings.Join(skipFailureTexts(rep), "; "))
		}
		got := rep.Failures[0].Site
		if got.Reason == nil || *got.Reason != "docker is not running on the host" {
			t.Errorf("the failure carries %s, want the joined text of the two literals", got.reasonText())
		}
	})
}

func TestSkipReasonClassifierHoldsARegisteredSiteAndFailsTheSameReasonElsewhere(t *testing.T) {
	t.Parallel()
	tr := newSkipTree(t)
	tr.file("tests/carrier/carrier_test.go", skipCarrierSource(
		skipCarrierFunc{Name: "TestRegistered", Statements: []string{`t.Skip("docker is not running")`}},
		skipCarrierFunc{Name: "TestOther", Statements: []string{`t.Skip("docker is not running")`}},
	))
	tr.register(skipSite{
		Path:     "tests/carrier/carrier_test.go",
		Function: "TestRegistered",
		Site:     1,
		Call:     skipCallPlain,
		Reason:   literalSkipReason("docker is not running"),
	})

	rep := tr.runOK()
	if rep.Held != 1 {
		t.Errorf("held %d entr(ies), want the registered site", rep.Held)
	}
	if len(rep.Failures) != 1 {
		t.Fatalf("reported %d failure(s), want the same reason at the site the register does not carry: %s",
			len(rep.Failures), strings.Join(skipFailureTexts(rep), "; "))
	}
	if got := rep.Failures[0]; !got.Absent || got.Site.Function != "TestOther" {
		t.Errorf("the failure reads %+v, want the unregistered site", got.Site)
	}
}

func TestSkipReasonClassifierRemovesARewrittenReasonAndFailsItsReturn(t *testing.T) {
	t.Parallel()
	tr := newSkipTree(t)
	tr.carrier("tests/carrier/carrier_test.go", `t.Skip("docker is not running")`)
	entry := skipSite{
		Path:     "tests/carrier/carrier_test.go",
		Function: "TestCarrier",
		Site:     1,
		Call:     skipCallPlain,
		Reason:   literalSkipReason("docker is not running"),
	}
	tr.register(entry)

	// The reason is rewritten into a category, so the entry is dropped in
	// the same run.
	tr.carrier("tests/carrier/carrier_test.go", `t.Skip("blocked: no docker daemon on the host")`)
	rewritten := tr.runOK()
	if len(rewritten.Failures) != 0 {
		t.Fatalf("a rewritten reason was reported: %s", strings.Join(skipFailureTexts(rewritten), "; "))
	}
	if len(rewritten.Removed) != 1 || rewritten.Removed[0].Function != "TestCarrier" {
		t.Fatalf("the run removed %v, want the entry whose reason gained a category", rewritten.Removed)
	}
	if body := tr.registerText(); strings.Contains(body, "docker is not running") {
		t.Fatalf("the register was not rewritten downward:\n%s", body)
	}

	// The site returns to free text. Without the downward rewrite the
	// gate would stay green here.
	tr.carrier("tests/carrier/carrier_test.go", `t.Skip("docker is not running")`)
	returned := tr.runOK()
	if len(returned.Failures) != 1 || !returned.Failures[0].Absent {
		t.Fatalf("reported %v on the return to free text, want the site read as one the register does not carry",
			skipFailureTexts(returned))
	}
}

func TestSkipReasonClassifierFailsOnAReasonThatChangedAtARegisteredSite(t *testing.T) {
	t.Parallel()
	tr := newSkipTree(t)
	tr.carrier("tests/carrier/carrier_test.go", `t.Skip("podman is not running")`)
	tr.register(skipSite{
		Path:     "tests/carrier/carrier_test.go",
		Function: "TestCarrier",
		Site:     1,
		Call:     skipCallPlain,
		Reason:   literalSkipReason("docker is not running"),
	})

	rep := tr.runOK()
	if len(rep.Failures) != 1 {
		t.Fatalf("reported %d failure(s) for a changed reason, want 1: %s",
			len(rep.Failures), strings.Join(skipFailureTexts(rep), "; "))
	}
	got := rep.Failures[0]
	if got.Absent || got.Registered == nil || *got.Registered.Reason != "docker is not running" {
		t.Errorf("the failure reads %+v, want the site measured against the reason the register carries", got)
	}
	// A failing run writes nothing, so the entry it measured against
	// survives. A run that rewrote the register while red would report a
	// different failure on the next run over the same tree and would
	// leave a tracked file modified by a gate that failed.
	before := tr.registerText()
	if !strings.Contains(before, "docker is not running") {
		t.Fatalf("the failing run dropped the entry it measured the site against:\n%s", before)
	}

	// The same tree fails the same way again, naming the same entry.
	again := tr.runOK()
	if len(again.Failures) != 1 {
		t.Fatalf("the second run over the same tree reported %d failure(s), want the same one: %s",
			len(again.Failures), strings.Join(skipFailureTexts(again), "; "))
	}
	if repeated := again.Failures[0]; repeated.Absent || repeated.Registered == nil {
		t.Errorf("the second run reads %+v, want the same failure the first run reported", repeated)
	}
	if after := tr.registerText(); after != before {
		t.Errorf("a failing run rewrote the register:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// Reverting the edit that failed the run restores green, because the
	// baseline the run measured against is still the one it landed with.
	tr.carrier("tests/carrier/carrier_test.go", `t.Skip("docker is not running")`)
	reverted := tr.runOK()
	if len(reverted.Failures) != 0 {
		t.Errorf("reverting the reason left %s failing: %s",
			skipRegisterPath, strings.Join(skipFailureTexts(reverted), "; "))
	}
}

func TestSkipReasonClassifierFailsOnASiteTheRegisterDoesNotCarry(t *testing.T) {
	t.Parallel()
	tr := newSkipTree(t)
	tr.carrier("tests/carrier/carrier_test.go",
		`t.Skip("blocked: no docker daemon on the host")`,
		`t.Skip("docker is not running")`)
	tr.register()

	rep := tr.runOK()
	if len(rep.Failures) != 1 {
		t.Fatalf("reported %d failure(s) for a site the register does not carry, want 1: %s",
			len(rep.Failures), strings.Join(skipFailureTexts(rep), "; "))
	}
	// The accepted call above it is inspected, so the reported call is
	// the second site of the function rather than the first.
	if got := rep.Failures[0].Site; got.Site != 2 {
		t.Errorf("the failure names site %d, want the ordinal counted across every inspected call", got.Site)
	}
	if body := tr.registerText(); strings.Contains(body, "docker is not running") {
		t.Errorf("the run added the site to the register rather than failing it:\n%s", body)
	}
}

func TestSkipReasonClassifierLeavesTheRegisterUntouchedOnAFailingRun(t *testing.T) {
	t.Parallel()
	tr := newSkipTree(t)
	tr.file("tests/carrier/carrier_test.go", skipCarrierSource(
		skipCarrierFunc{Name: "TestRewritten", Statements: []string{`t.Skip("docker is not running")`}},
		skipCarrierFunc{Name: "TestOther", Statements: []string{`t.Skip("podman is not running")`}},
	))
	held := skipSite{
		Path:     "tests/carrier/carrier_test.go",
		Function: "TestRewritten",
		Site:     1,
		Call:     skipCallPlain,
		Reason:   literalSkipReason("docker is not running"),
	}
	tr.register(held)
	seeded := tr.registerText()

	// One entry would be dropped, because its reason gains a category,
	// and one site fails, because the register does not carry it. The
	// run writes nothing: a gate that modified a tracked file on the run
	// it failed would leave the tree dirty and would measure the next
	// run against a baseline no landed edit produced.
	tr.file("tests/carrier/carrier_test.go", skipCarrierSource(
		skipCarrierFunc{Name: "TestRewritten", Statements: []string{`t.Skip("blocked: no docker daemon on the host")`}},
		skipCarrierFunc{Name: "TestOther", Statements: []string{`t.Skip("podman is not running")`}},
	))
	rep := tr.runOK()
	if len(rep.Failures) != 1 {
		t.Fatalf("reported %d failure(s), want the site the register does not carry: %s",
			len(rep.Failures), strings.Join(skipFailureTexts(rep), "; "))
	}
	if body := tr.registerText(); body != seeded {
		t.Errorf("the failing run rewrote the register:\nbefore:\n%s\nafter:\n%s", seeded, body)
	}
}

func TestSkipReasonClassifierFailsOnAnUnloadableRegister(t *testing.T) {
	t.Parallel()
	entry := "  - path: tests/carrier/carrier_test.go\n    function: TestCarrier\n    site: 1\n" +
		"    call: Skip\n    reason: docker is not running\n"
	for _, tc := range []struct {
		name   string
		body   string
		remove bool
	}{
		{name: "the register does not parse", body: "kind: [skip-reason-baseline\n"},
		{name: "the register declares no sites block", body: "kind: skip-reason-baseline\nversion: 1\n"},
		{name: "the register declares another kind", body: "kind: other\nversion: 1\nsites: []\n"},
		{name: "the register declares another version", body: "kind: skip-reason-baseline\nversion: 2\nsites: []\n"},
		{
			name: "the register declares one site twice",
			body: "kind: skip-reason-baseline\nversion: 1\nsites:\n" + entry + entry,
		},
		{
			name: "the register carries a site with no path",
			body: "kind: skip-reason-baseline\nversion: 1\nsites:\n  - function: TestCarrier\n" +
				"    site: 1\n    call: Skip\n    reason: docker is not running\n",
		},
		{
			name: "the register carries an ordinal below one",
			body: "kind: skip-reason-baseline\nversion: 1\nsites:\n  - path: tests/carrier/carrier_test.go\n" +
				"    function: TestCarrier\n    site: 0\n    call: Skip\n    reason: docker is not running\n",
		},
		{
			name: "the register carries an unknown call form",
			body: "kind: skip-reason-baseline\nversion: 1\nsites:\n  - path: tests/carrier/carrier_test.go\n" +
				"    function: TestCarrier\n    site: 1\n    call: Fatal\n    reason: docker is not running\n",
		},
		{
			// A reason that opens with a category is accepted by the
			// predicate, so an entry for it would never be read again and
			// would sit in the register forever.
			name: "the register carries a reason that needs no entry",
			body: "kind: skip-reason-baseline\nversion: 1\nsites:\n  - path: tests/carrier/carrier_test.go\n" +
				"    function: TestCarrier\n    site: 1\n    call: Skip\n    reason: 'blocked: no docker daemon'\n",
		},
		{name: "the register is missing", body: "kind: skip-reason-baseline\nversion: 1\nsites: []\n", remove: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := newSkipTree(t)
			tr.carrier("tests/carrier/carrier_test.go", `t.Skip("docker is not running")`)
			tr.rawRegister(tc.body)
			if tc.remove {
				tr.removeRegister()
			}
			_, err := tr.run()
			if err == nil {
				t.Fatalf("the run certified the tree with an unloadable register")
			}
			if !strings.Contains(err.Error(), skipRegisterPath) {
				t.Errorf("the error %q does not name the register", err)
			}
		})
	}
}

func TestSkipReasonClassifierFailsOnAFileThatDoesNotParse(t *testing.T) {
	t.Parallel()
	tr := newSkipTree(t)
	tr.carrier("tests/carrier/carrier_test.go", `t.Skip("blocked: no docker daemon on the host")`)
	tr.file("tests/carrier/broken_test.go", "package carrier\n\nfunc TestBroken(t *testing.T) {\n")
	tr.register()

	_, err := tr.run()
	if err == nil {
		t.Fatalf("the run certified a tree holding a file it could not parse")
	}
	if !strings.Contains(err.Error(), "tests/carrier/broken_test.go") {
		t.Errorf("the error %q does not name the file that does not parse", err)
	}
}

func TestSkipReasonClassifierFailsWhenTheWalkSelectsZeroGoFiles(t *testing.T) {
	t.Parallel()
	tr := newSkipTree(t)
	// Every Go file in the tree is outside the trees the classifier
	// governs, so the domain holds no file to classify.
	tr.file("docs/example.go", skipCarrierSource(
		skipCarrierFunc{Name: "TestCarrier", Statements: []string{`t.Skip("docker is not running")`}},
	))
	tr.register()

	_, err := tr.run()
	if err == nil {
		t.Fatalf("the run certified a tree over which it classified no Go file")
	}
	if !strings.Contains(err.Error(), "skip-reason classifier") {
		t.Errorf("the error %q does not name the classifier", err)
	}
}

func TestSkipReasonClassifierFailsWhenNoSkipCallSiteIsInspected(t *testing.T) {
	t.Parallel()
	tr := newSkipTree(t)
	tr.carrier("tests/carrier/carrier_test.go", "t.Log(\"the test runs\")")
	tr.register()

	_, err := tr.run()
	if err == nil {
		t.Fatalf("the run certified a tree in which it inspected no skip call site")
	}
	if !strings.Contains(err.Error(), "skip-reason classifier") {
		t.Errorf("the error %q does not name the classifier", err)
	}
}
