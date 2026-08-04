// SPDX-License-Identifier: MIT

// Package identifier holds the specshift identifier pass, which rewrites
// a retired channel spelling to the canonical one across code, schemas,
// SDKs, charts, and documentation, moves the carrier whose own file name
// carries a retired spelling, and moves the keys of the path-keyed
// test-infrastructure registers the move invalidates.
//
// Two inputs drive the pass, and they answer different questions. The
// naming table in the communication-channels section of the
// specification states, per channel and per carrier, the spelling being
// retired and the spelling that replaces it. The sense register states,
// per occurrence, which channel that occurrence denotes, or that the
// occurrence is not a channel at all.
//
// The register is per occurrence because a retired spelling is
// two-valued in both directions. One spelling denotes two channels: the
// same Go token is declared for one channel in one file of a package and
// for the other channel in another file of the same package, so a
// channel-keyed register cannot say which of them an occurrence means. A
// spelling the table maps to exactly one channel still occurs where the
// text is not that channel, such as a command-line file argument that
// happens to carry the socket token, so a channel-count trigger would
// substitute there blind and rename a file after a mechanism the
// surrounding text never mentions.
//
// A site with no entry aborts the run with the tree left byte-identical,
// naming the file and the line, rather than taking a default. The
// substitution reads as canonical to every gate downstream: the
// identifier-resolution gate reads the forward relation, so it does not
// observe one spelling resolved to the wrong identifier, and no gate
// reads meaning. The reviewer resolves each site into the register
// before the substitution runs.
//
// Which naming-table row a resolved site takes is decided first by the
// form of the site, and the form decides unconditionally where it
// applies. A site standing in the method component of a gRPC full-method
// literal takes the proto RPC row, because that literal carries the RPC
// name the service definition declares and the Go method of the same
// channel is spelled differently. A site standing in the name of the
// file itself takes the path row, because the file-name stem is the path
// carrier. The form selects a row at those two positions and rules
// nothing out anywhere else, so ordinary carrier text stays open to every
// row of the channel, the proto RPC one included: the primary carrier of
// that row is the `rpc <Name>` declaration of the service definition,
// which is ordinary text. A channel that states a spelling in more
// carriers than the form selects between is resolved by the carrier the
// register entry names, a carrier the form has already ruled out fails
// rather than overriding it, and a site the two rules leave ambiguous
// aborts rather than taking the first row.
//
// The pass writes on three channels, all planned before anything is
// written. It substitutes at each resolved site; it moves a file whose
// own name carries a retired spelling, driven by an entry of the same
// register; and it rewrites the keys the move invalidates in the
// path-keyed registers. The third channel is separate because two of
// those registers are outside every pass's site-rewrite domain, a citation
// gate not being able to read its own baseline as tree content, while a
// rename still has to move their keys in the same run. Without it the
// line-citation ratchet fires on a rename that changed no citation, and
// every baselined non-resolving citation under the old path reappears as
// a resolver failure.
//
// The walk, the read exclusion, the identifier class's own register
// exclusion, and the per-pass write exclusion all come from the scope
// package rather than from a list here, so the pass writes against the
// same statement of the domain the identifier-resolution gate reads.
//
// This is migration tooling rather than a platform behavior, so it
// carries no spec citation of its own.
package identifier

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/pass"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// Rewriter is the identifier pass. Its tree dependencies are injected so
// a test drives it over a fixture tree.
type Rewriter struct {
	list scope.Lister
	read scope.FileReader

	// registerPath and senses are the driving register: the per-site
	// senses, keyed by file and by position within that file.
	registerPath string
	senses       map[string]map[int]Entry

	// table is the naming table the specification states. It is read out
	// of the tree the pass runs over, because the pass rewrites that
	// tree and a second copy of the table would let the spelling written
	// and the spelling stated disagree.
	table *Table

	// moves are the file moves the run performs, keyed by the old path,
	// and symbols are the token renames the substitutions produce, keyed
	// by the old token. Both are computed once, before any file is
	// written, because the registers a rename invalidates are rekeyed by
	// whichever channel reaches them first and a set filled during the
	// walk would depend on the order the walk took.
	moves   map[string]string
	symbols map[string]string

	// confine is the part of the write domain this run may write, nil
	// when the run covers the whole of it. It filters the two reads the
	// walk does not cover: the claimed-entry check, which holds the
	// whole register to the tree, and the rename planning, which runs
	// over the whole tracked tree before the walk begins.
	confine *pass.Confinement

	// deferred names the register entries the confinement put outside
	// this run, sorted by file path and, within a file, by entry
	// position (the file-name entry first, then the occurrences in
	// order). The register's own listing order is not recoverable: the
	// loader keys the entries by file and by position, so the order the
	// checks read them in is the sorted one they report. They are the
	// complementary run's to check, and the run reports them so an
	// entry no run covers is visible rather than silently unconsumed.
	deferred []string

	// prepared records that the one-per-run preparation has been
	// attempted, and prepErr the outcome it reached. The outcome is
	// memoized whether it succeeded or failed, because the preparation
	// walks and parses the whole tracked tree and reports the run's
	// whole hand-correction population: re-entering it on the next file
	// the harness hands the pass would re-walk the tree once per file in
	// the write domain and report each abort once per file.
	prepared bool
	prepErr  error
}

// New returns the identifier pass over the tree the lister and reader
// cover.
func New(list scope.Lister, read scope.FileReader) *Rewriter {
	return &Rewriter{list: list, read: read}
}

// Pass names the write domain the pass runs in.
func (r *Rewriter) Pass() scope.Pass { return scope.Identifier }

// Confine states the part of the write domain this run writes, which
// the claimed-entry check and the rename planning are held to.
func (r *Rewriter) Confine(c *pass.Confinement) { r.confine = c }

// Deferred returns the register entries this run left to the
// complementary one, sorted by file path and, within a file, by entry
// position.
func (r *Rewriter) Deferred() []string {
	return append([]string(nil), r.deferred...)
}

// DeferredFiles returns the distinct files those entries are keyed to,
// in path order. It is derived from the register rather than from the
// entry descriptions, which name an entry's position beside the file.
func (r *Rewriter) DeferredFiles() []string {
	return r.confine.Exclude(sortedKeys(r.senses))
}

// LoadRegister reads and validates the per-site senses that drive the
// pass. A missing or malformed register fails rather than loading as an
// empty one: a run with no senses would rewrite no file and report the
// zero work of a completed migration.
func (r *Rewriter) LoadRegister(path string) error {
	senses, err := loadSenses(path)
	if err != nil {
		return err
	}
	r.registerPath, r.senses = path, senses
	return nil
}

// Rewrite substitutes the canonical spelling at every resolved
// retired-spelling site one file carries.
func (r *Rewriter) Rewrite(ctx context.Context, target string, content []byte) ([]byte, error) {
	if err := r.prepare(ctx); err != nil {
		return nil, err
	}
	// The two citation baselines are outside the read domain the passes
	// and the gates share, because a citation gate cannot read its own
	// baseline as tree content, so the key rewrite is the only channel
	// that reaches them. Every other path-keyed register is an ordinary
	// member of that domain and takes the site rewrite alongside its key
	// rewrite; the spans the key rewrite owns are held out of the site
	// enumeration by contentSites, so no key is written twice.
	if scope.KeyWritable(target) && !scope.Readable(target) {
		return content, nil
	}
	text := string(content)
	sites := r.contentSites(target, text)
	if len(sites) == 0 {
		return content, nil
	}
	edits, err := r.plan(target, sites)
	if err != nil {
		return nil, err
	}
	return []byte(splice(text, edits)), nil
}

// MoveTo returns the path a carrier takes after the pass, which is its
// own name with the canonical spelling written at the retired one.
//
// A file name carrying a retired spelling with no entry aborts the run,
// as an occurrence in the content does and for the same reason: the
// stem the file is named after is a channel reference or it is not, and
// nothing in the name says which.
func (r *Rewriter) MoveTo(ctx context.Context, target string) (string, error) {
	if err := r.prepare(ctx); err != nil {
		return "", err
	}
	return r.moves[target], nil
}

// RewriteKeys moves the keys of one path-keyed register, and refuses a
// register that still names a path or a symbol the run retired.
func (r *Rewriter) RewriteKeys(ctx context.Context, target string, content []byte) ([]byte, error) {
	if err := r.prepare(ctx); err != nil {
		return nil, err
	}
	after := rekey(target, content, r.moves, r.symbols)
	if stale := staleKeys(after, r.moves, r.symbols); len(stale) > 0 {
		return nil, fmt.Errorf("%s still names %d retired key(s) after the rekey, which the run cannot move because they are written inside a wider value: %s",
			target, len(stale), strings.Join(stale, "; "))
	}
	return after, nil
}

// prepare reads the naming table, computes the file moves and symbol
// renames the run performs, and holds the register to the tree. It runs
// once per run, on whichever channel reaches the pass first, because the
// key rewrite of a register outside the site-rewrite domain needs the
// moves the site walk would otherwise be the only producer of.
//
// Its outcome is memoized in both dispositions. A failure re-computed
// per file would re-walk and re-parse the whole tracked tree once per
// file in the write domain and report the same population of aborts
// once per file, and one run states that population once.
func (r *Rewriter) prepare(ctx context.Context) error {
	if r.prepared {
		return r.prepErr
	}
	r.prepared = true
	r.prepErr = r.prepareOnce(ctx)
	return r.prepErr
}

// prepareOnce is the body of the preparation, run once per pass.
//
// The renames are planned before the register is held to the tree,
// because the site enumeration of a path-keyed register holds out the
// spans the key rewrite owns and so is defined only once the moves are
// known.
func (r *Rewriter) prepareOnce(ctx context.Context) error {
	if r.senses == nil {
		return fmt.Errorf("the identifier pass ran with no register loaded")
	}
	table, err := LoadTable(ctx, r.list, r.read)
	if err != nil {
		return err
	}
	if err := r.checkChannels(table); err != nil {
		return err
	}
	tracked, err := trackedPaths(ctx, r.list)
	if err != nil {
		return err
	}
	r.table = table
	moves, symbols, err := r.planRenames(ctx, tracked)
	if err != nil {
		return err
	}
	r.moves, r.symbols = moves, symbols
	return r.checkClaimed(ctx, tracked)
}

// checkChannels holds every entry to the channels the naming table
// names. A channel the table does not carry states no spelling, so the
// site it resolves has no substitution and the run would abort there
// with the operator sent to the register rather than to the table.
func (r *Rewriter) checkChannels(table *Table) error {
	var unknown []string
	for _, target := range sortedKeys(r.senses) {
		for _, at := range sortedPositions(r.senses[target]) {
			entry := r.senses[target][at]
			if entry.Channel == "" || table.Declares(entry.Channel) {
				continue
			}
			unknown = append(unknown, fmt.Sprintf("%s names %s", entry.where(), entry.Channel))
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("%s names %d channel(s) the naming table does not carry: %s",
			r.registerPath, len(unknown), strings.Join(unknown, "; "))
	}
	return nil
}

// contentSites returns the retired-spelling sites one file's content
// carries, which is what the register's occurrence numbers are assigned
// against.
//
// A spelling standing in a row of the naming table itself is not one of
// them. The row is where the spelling is retired, so the cell names the
// spelling rather than referring to the channel, and demanding a
// register entry for it would key the enumeration of a specification
// file to the size of the table inside it.
//
// A spelling standing in a key the key rewrite already moves is not one
// of them either. The key channel is authoritative over those spans: it
// writes the path the run moved the file to, whereas the site rule would
// write a carrier spelling over part of that path, and a register entry
// demanded for every such key would state a sense the move already
// carries. Every other occurrence a path-keyed register writes, a
// section title or a wildcard glob key among them, is an ordinary site
// and is resolved through the register like any other.
func (r *Rewriter) contentSites(target, content string) []site {
	all := findSites(content, r.table.Retired())
	keys := keySpans(target, content, r.moves, r.symbols)
	out := make([]site, 0, len(all))
	for _, s := range all {
		if r.table.mentioned(target, s.start) || withinSpan(keys, s.start) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// withinSpan reports whether an offset stands inside one of the spans.
func withinSpan(spans [][2]int, at int) bool {
	for _, span := range spans {
		if at >= span[0] && at < span[1] {
			return true
		}
	}
	return false
}

// pathSites returns the retired-spelling sites a file's own name
// carries, each marked with the form that selects the path row.
func (r *Rewriter) pathSites(target string) []site {
	sites := findSites(target, r.table.Retired())
	for i := range sites {
		sites[i].fileName = true
	}
	return sites
}

// plan resolves every site of one file against the register.
//
// Every unresolved site is collected before the plan fails, so one run
// names the whole hand-correction population rather than its first
// member. Nothing is written until the plan succeeds, so reporting them
// all still leaves the tree byte-identical.
func (r *Rewriter) plan(target string, sites []site) ([]edit, error) {
	entries := r.senses[target]
	edits := make([]edit, 0, len(sites))
	var aborts []*pass.Abort
	for i, s := range sites {
		occurrence := i + 1
		entry, ok := entries[occurrence]
		if !ok {
			aborts = append(aborts, &pass.Abort{
				Path: target,
				Line: s.line,
				Reason: fmt.Sprintf("occurrence %d of the retired spelling %s has no entry in %s, so what it denotes is unresolved",
					occurrence, s.retired, r.registerPath),
			})
			continue
		}
		// A site the register records as no channel stands as it is
		// written. It is the whole reason the trigger is per occurrence:
		// the spelling maps to one channel and this text is not it.
		if entry.NotAChannel {
			continue
		}
		row, err := r.rowFor(s, entry)
		if err != nil {
			aborts = append(aborts, &pass.Abort{Path: target, Line: s.line, Reason: err.Error()})
			continue
		}
		edits = append(edits, edit{start: s.start, end: s.end, text: row.Canonical})
	}
	if err := pass.Aborted(aborts); err != nil {
		return nil, err
	}
	return edits, nil
}

// rowFor selects the naming-table row a resolved site takes.
//
// The form of the site decides first, and it decides unconditionally
// where it applies. A gRPC full-method literal carries the RPC name of
// the service definition, so it takes the proto RPC row; a file name
// carries the path stem, so it takes the path row; every other site
// stays open to every row the channel states for the spelling. The
// register entry's carrier then resolves what the form leaves open, and
// a carrier the form has already ruled out fails rather
// than overriding it: an entry naming the Go type row at a full-method
// literal would write a method the service definition does not declare,
// and the run would exit clean because no gate downstream reads which
// spelling the site meant. A site neither rule resolves to one row
// aborts rather than taking the first, for the same reason.
func (r *Rewriter) rowFor(s site, entry Entry) (Row, error) {
	rows := r.table.rowsFor(s.retired, entry.Channel)
	if len(rows) == 0 {
		return Row{}, fmt.Errorf("the naming table states no spelling of %s for %s, so the site has no substitution",
			s.retired, entry.Channel)
	}
	form, rows := formRows(s, rows)
	if len(rows) == 0 {
		return Row{}, fmt.Errorf("the site is %s, and the naming table states no such spelling of %s for %s",
			form, s.retired, entry.Channel)
	}
	if entry.Carrier != "" {
		narrowed := withCarrier(rows, entry.Carrier)
		if len(narrowed) == 0 {
			return Row{}, fmt.Errorf("%s names the carrier %s, and the site is %s, which the naming table states no %s spelling of %s for %s at",
				r.registerPath, entry.Carrier, form, entry.Carrier, s.retired, entry.Channel)
		}
		rows = narrowed
	}
	if len(rows) != 1 {
		return Row{}, fmt.Errorf("the naming table states %d spellings of %s for %s (%s), and neither the site nor %s selects one",
			len(rows), s.retired, entry.Channel, carriersOf(rows), r.registerPath)
	}
	return rows[0], nil
}

// formRows applies the form rule of a site to a channel's rows, and
// names the form for a failure message.
//
// A file name is the carrier of the path stem, so it takes the path row
// alone: resolving it from whichever other row happened to be unique
// would move a carrier to a name the table states for no path. A
// full-method literal takes the proto RPC row for the same reason.
//
// The two rules select; they do not subtract. Ordinary carrier text
// keeps every row the channel states for the spelling, because the proto
// RPC row's own primary carrier is the `rpc <Name>` declaration of the
// service definition, which is ordinary text: subtracting that row
// everywhere outside a full-method literal would leave the declaration
// with no route to the spelling the naming table states for it. What the
// form leaves open the register entry's carrier resolves, and a site it
// leaves ambiguous aborts.
func formRows(s site, rows []Row) (string, []Row) {
	switch {
	case s.fileName:
		return "a file name", withCarrier(rows, CarrierPath)
	case s.grpcMethod:
		return "the method component of a gRPC full-method literal", withCarrier(rows, CarrierProtoRPC)
	default:
		return "ordinary carrier text", rows
	}
}

// withCarrier keeps the rows of one carrier.
func withCarrier(rows []Row, c Carrier) []Row {
	var out []Row
	for _, row := range rows {
		if row.Carrier == c {
			out = append(out, row)
		}
	}
	return out
}

// carriersOf renders the carriers of a row set for a failure message.
func carriersOf(rows []Row) string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, string(row.Carrier))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// planRenames computes the file moves and the symbol renames the run
// performs.
//
// Both are computed before any file is rewritten. A move is what the
// path-keyed registers are rekeyed against, and a register outside the
// site-rewrite domain is reached by a channel that never walks the file
// that moved, so a set filled during the walk would rekey against
// whichever moves the walk had reached by then.
//
// A tracked file inside the write domain whose own name carries a
// retired spelling and that the register does not record aborts the run,
// so a carrier is never left named after a channel that no longer exists
// and never moved on a guess.
//
// The file name is the one site class a pass reads outside the walk, so
// it takes the confinement here rather than through the filtered
// domain. A path the confinement does not cover is skipped whole: this
// run neither moves it, nor records its symbols, nor aborts on it, and
// the complementary run, which is the run that performs the move, takes
// all three. Skipping the move with the abort is what keeps the key
// channel honest, because the moves are what a path-keyed register is
// rekeyed against and a key moved for a carrier that stands still would
// name a path no run ever creates.
func (r *Rewriter) planRenames(ctx context.Context, tracked map[string]bool) (map[string]string, map[string]string, error) {
	moves := map[string]string{}
	symbols := map[string]string{}
	var aborts []*pass.Abort
	for _, target := range sortedTracked(tracked) {
		if err := ctx.Err(); err != nil {
			return nil, nil, fmt.Errorf("plan the identifier renames: %w", err)
		}
		writable, err := scope.Writable(scope.Identifier, target, r.read)
		if err != nil {
			return nil, nil, err
		}
		if !writable {
			continue
		}
		if !r.confine.Covers(target) {
			continue
		}
		move, abort := r.moveOf(target)
		if abort != nil {
			aborts = append(aborts, abort)
			continue
		}
		moved := target
		if move != "" {
			moves[target] = move
			moved = move
		}
		if err := r.recordSymbols(target, moved, symbols); err != nil {
			return nil, nil, err
		}
	}
	if err := pass.Aborted(aborts); err != nil {
		return nil, nil, err
	}
	return moves, symbols, nil
}

// moveOf returns the path a carrier moves to, the empty string when its
// name carries no retired spelling or the register records the name as
// no channel, and an abort when the name carries one the register does
// not record.
func (r *Rewriter) moveOf(target string) (string, *pass.Abort) {
	sites := r.pathSites(target)
	if len(sites) == 0 {
		return "", nil
	}
	entry, ok := r.senses[target][0]
	if !ok {
		return "", &pass.Abort{
			Path: target,
			Reason: fmt.Sprintf("the file name carries the retired spelling %s and has no file-name entry in %s, so what it denotes is unresolved",
				sites[0].retired, r.registerPath),
		}
	}
	if entry.NotAChannel {
		return "", nil
	}
	edits := make([]edit, 0, len(sites))
	for _, s := range sites {
		row, err := r.rowFor(s, entry)
		if err != nil {
			return "", &pass.Abort{Path: target, Reason: err.Error()}
		}
		edits = append(edits, edit{start: s.start, end: s.end, text: row.Canonical})
	}
	return splice(target, edits), nil
}

// recordSymbols records the symbol references one Go carrier's
// substitutions retire, keyed by the reference the spec map writes,
// which is `<declaring file>::<symbol>`.
//
// The reference is keyed by symbol rather than by path, and a tier-0
// check resolves each one against its declaring file, so a renamed
// symbol left standing there fails that check with the run that renamed
// it having exited clean. The record is keyed by the whole reference
// rather than by the token alone, because the same token is declared for
// one channel in one file of a package and for the other channel in
// another file of it, which is the collision the sense register exists
// for, and a token-keyed record would rewrite each reference to
// whichever of the two the walk reached last.
func (r *Rewriter) recordSymbols(target, moved string, symbols map[string]string) error {
	if filepath.Ext(target) != ".go" || len(r.senses[target]) == 0 {
		return nil
	}
	content, err := r.read(target)
	if err != nil {
		return fmt.Errorf("read %s for the identifier symbol rename: %w", target, err)
	}
	text := string(content)
	declared, err := declaredNameSpans(target, text)
	if err != nil {
		return err
	}
	for i, s := range r.contentSites(target, text) {
		entry, ok := r.senses[target][i+1]
		if !ok || entry.NotAChannel {
			continue
		}
		span, declares := declarationAround(declared, s.start, s.end)
		if !declares {
			continue
		}
		row, err := r.rowFor(s, entry)
		if err != nil {
			// The site is reported as an abort by the plan of the file
			// itself, which names its line.
			continue
		}
		before := text[span[0]:span[1]]
		after := text[span[0]:s.start] + row.Canonical + text[s.end:span[1]]
		if before != after {
			symbols[target+"::"+before] = moved + "::" + after
		}
	}
	return nil
}

// checkClaimed reports every register entry no site in the tree claims,
// which is an entry keyed to a file the tree does not carry, to a file
// outside the pass's write domain, to an occurrence number above the
// count of sites its file carries, or to a file name that carries no
// retired spelling.
//
// The register drives the pass in one direction and is checked in both.
// An entry the walk never reaches is written nowhere and, without this
// check, the run exits zero having reported the completed migration it
// never performed. An off-by-one enumeration and a misspelled path are
// the two ways an entry lands in that position.
//
// An entry whose file the run's confinement does not cover is deferred
// rather than checked, because this run neither reaches its site nor
// writes its file, and the complementary run checks it. The filter is
// the confinement rather than the set of files this run planned sites
// for: an entry whose file the confinement covers and whose sites a
// prior run over the same confinement already consumed still fails,
// which is what makes a replayed run loud rather than a silent no-op.
func (r *Rewriter) checkClaimed(ctx context.Context, tracked map[string]bool) error {
	var unclaimed []string
	r.deferred = nil
	for _, target := range sortedKeys(r.senses) {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("check the identifier sense register against the tree: %w", err)
		}
		if !r.confine.Covers(target) {
			for _, at := range sortedPositions(r.senses[target]) {
				r.deferred = append(r.deferred, r.senses[target][at].where())
			}
			continue
		}
		sites, reason, err := r.claimableSites(target, tracked)
		if err != nil {
			return err
		}
		for _, at := range sortedPositions(r.senses[target]) {
			entry := r.senses[target][at]
			switch {
			case reason != "":
				unclaimed = append(unclaimed, fmt.Sprintf("%s (%s)", entry.where(), reason))
			case entry.Path && len(r.pathSites(target)) == 0:
				unclaimed = append(unclaimed, fmt.Sprintf("%s (the file name carries no retired spelling)", entry.where()))
			case !entry.Path && at > sites:
				unclaimed = append(unclaimed, fmt.Sprintf("%s (the file carries %d retired-spelling site(s))", entry.where(), sites))
			}
		}
	}
	if len(unclaimed) > 0 {
		return fmt.Errorf("%s carries %d entr(ies) no site in the tree claims, so the run would report a substitution it never performed: %s",
			r.registerPath, len(unclaimed), strings.Join(unclaimed, "; "))
	}
	return nil
}

// claimableSites returns how many retired-spelling sites one file's
// content carries, or the reason no entry keyed to it can be claimed.
func (r *Rewriter) claimableSites(target string, tracked map[string]bool) (int, string, error) {
	if !tracked[target] {
		return 0, "the tree carries no such file", nil
	}
	writable, err := scope.Writable(scope.Identifier, target, r.read)
	if err != nil {
		return 0, "", fmt.Errorf("check %s against the identifier sense register: %w", target, err)
	}
	if !writable {
		return 0, "the file is outside the pass's write domain", nil
	}
	content, err := r.read(target)
	if err != nil {
		return 0, "", fmt.Errorf("read %s for the identifier sense register check: %w", target, err)
	}
	return len(r.contentSites(target, string(content))), "", nil
}

// trackedPaths returns the tracked tree as a set, so the register check
// asks about a path without walking the list per entry.
func trackedPaths(ctx context.Context, list scope.Lister) (map[string]bool, error) {
	if list == nil {
		return nil, fmt.Errorf("check the identifier sense register against the tree: a lister is required")
	}
	paths, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("check the identifier sense register against the tree: %w", err)
	}
	tracked := make(map[string]bool, len(paths))
	for _, p := range paths {
		tracked[p] = true
	}
	return tracked, nil
}

// sortedTracked returns the tracked paths in order, so a run reports the
// same failures in the same order.
func sortedTracked(tracked map[string]bool) []string {
	out := make([]string, 0, len(tracked))
	for p := range tracked {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
