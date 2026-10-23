// SPDX-License-Identifier: MIT

package tier0_static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// Every gateway and lenny-ops Redis store runs on the go-redis cluster client
// when the chart's redis.provider is cluster, and a single-node test client
// accepts operations a cluster rejects. Two defects follow from that gap. A
// script, a MULTI block, or a multi-key command whose keys span hash slots
// passes every single-node test and fails with CROSSSLOT on a cluster
// (F-12.4.28). A SCAN issued on the cluster client reaches one arbitrary
// master, so it returns a partial result with no error (F-12.4.29).
//
// This check closes both gaps for code that has not been written yet. Every
// package whose non-test code issues a script, a transaction, a WATCH, or a
// multi-key command must install the slotguard hook (or build a clustertest
// cluster, which installs it) in its own tests, so a cross-slot operation
// fails in tier 1. Every SCAN must go through redisconn.ScanKeys or
// redisconn.ScanDelete, which route it to the owning master or to every
// master on a cluster client.

// redisClusterRoots are the trees whose non-test Go files the check reads.
var redisClusterRoots = []string{"pkg", "cmd", "sdks"}

// redisScanHome is the package that implements node-routed SCAN; its own
// SCAN calls are the sanctioned ones.
const redisScanHome = "pkg/redisconn"

// redisSlotGuardExempt lists packages that issue multi-key operations but
// whose tests cannot install the slot hook yet, each with the finding that
// tracks the fix. An entry is removed when that finding closes.
var redisSlotGuardExempt = map[string]string{
	// The lenny-ops remediation-lock scripts and transaction span
	// ops:lock:<scope> and ops:lockidx:<id>, which hash to different slots.
	// Renaming them under one hash tag needs a §25.4 change, which P6 of
	// gateway-runtime-comms-remediation.md §10.2 owns (F-12.4.28 class B/C).
	"pkg/ops/coordination/redisstore": "F-12.4.28 class B/C (P6)",
}

// multiKeyMethods are go-redis methods that always issue a script, a
// transaction, or a command over several keys.
var multiKeyMethods = map[string]bool{
	"TxPipeline": true, "TxPipelined": true, "Watch": true,
	"MGet": true, "MSet": true, "MSetNX": true,
	"Eval": true, "EvalSha": true, "EvalRO": true, "EvalShaRO": true,
	"FCall": true, "FCallRO": true, "NewScript": true,
	"SUnion": true, "SInter": true, "SDiff": true,
	"SUnionStore": true, "SInterStore": true, "SDiffStore": true,
	"Rename": true, "RenameNX": true, "SMove": true, "RPopLPush": true,
	"LMove": true, "BLMove": true, "Copy": true,
}

// variadicKeyMethods issue a multi-key command when called with a spread
// slice (keys...).
var variadicKeyMethods = map[string]bool{
	"Del": true, "Unlink": true, "Exists": true, "Touch": true,
}

// redisUsage records what one package's non-test code does with Redis.
type redisUsage struct {
	multiKey []string // file:line of each multi-key operation
	scans    []string // file:line of each direct SCAN
}

// spec: §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3; the
// `t:{tenant_id}:` prefix hashes a tenant's keys to one slot).
func TestRedisStoresInstallSlotGuard_spec_12_4(t *testing.T) {
	root := schematest.RepoRoot(t)
	usage := scanRedisUsage(t, root)
	if len(usage) == 0 {
		t.Fatal("found no package issuing multi-key Redis operations; the scan is broken")
	}
	var missing []string
	for _, pkg := range sortedRedisPackages(usage) {
		u := usage[pkg]
		if len(u.multiKey) == 0 {
			continue
		}
		if _, ok := redisSlotGuardExempt[pkg]; ok {
			continue
		}
		if !testsInstallSlotGuard(t, filepath.Join(root, pkg)) {
			missing = append(missing, pkg+" (first site "+u.multiKey[0]+")")
		}
	}
	if len(missing) > 0 {
		t.Errorf("these packages issue scripts, transactions, or multi-key Redis commands, but no test in the package installs slotguard.Install or builds clustertest.New, so a cross-slot operation would pass tier 1 and fail on Redis Cluster:\n  %s",
			strings.Join(missing, "\n  "))
	}
	for pkg := range redisSlotGuardExempt {
		if u, ok := usage[pkg]; !ok || len(u.multiKey) == 0 {
			t.Errorf("exemption for %s is stale: the package no longer issues a multi-key operation; remove the entry", pkg)
		}
	}
}

// spec: §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3).
func TestRedisScanRoutesByNode_spec_12_4(t *testing.T) {
	root := schematest.RepoRoot(t)
	usage := scanRedisUsage(t, root)
	var direct []string
	for _, pkg := range sortedRedisPackages(usage) {
		if pkg == redisScanHome {
			continue
		}
		direct = append(direct, usage[pkg].scans...)
	}
	if len(direct) > 0 {
		t.Errorf("these sites issue a Redis SCAN directly; a cluster client sends it to one master and returns a partial result. Use redisconn.ScanKeys or redisconn.ScanDelete:\n  %s",
			strings.Join(direct, "\n  "))
	}
	if u := usage[redisScanHome]; u == nil || len(u.scans) == 0 {
		t.Errorf("%s issues no SCAN; the routed SCAN helper moved and this check must follow it", redisScanHome)
	}
}

// scanRedisUsage parses every non-test Go file under the roots that
// imports go-redis and records its multi-key operations and direct SCANs,
// keyed by package directory relative to root.
func scanRedisUsage(t *testing.T, root string) map[string]*redisUsage {
	t.Helper()
	out := map[string]*redisUsage{}
	fset := token.NewFileSet()
	for _, r := range redisClusterRoots {
		err := filepath.WalkDir(filepath.Join(root, r), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			if !importsGoRedis(f) {
				return nil
			}
			rel, _ := filepath.Rel(root, filepath.Dir(path))
			rel = filepath.ToSlash(rel)
			recordRedisCalls(fset, root, f, rel, out)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", r, err)
		}
	}
	return out
}

func importsGoRedis(f *ast.File) bool {
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == "github.com/redis/go-redis/v9" {
			return true
		}
	}
	return false
}

func recordRedisCalls(fset *token.FileSet, root string, f *ast.File, pkg string, out map[string]*redisUsage) {
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pos := fset.Position(call.Pos())
		rel, _ := filepath.Rel(root, pos.Filename)
		site := filepath.ToSlash(rel) + ":" + strconv.Itoa(pos.Line)
		u := out[pkg]
		if u == nil {
			u = &redisUsage{}
			out[pkg] = u
		}
		name := sel.Sel.Name
		switch {
		case multiKeyMethods[name]:
			u.multiKey = append(u.multiKey, site)
		case variadicKeyMethods[name] && call.Ellipsis.IsValid():
			u.multiKey = append(u.multiKey, site)
		case name == "Scan" && isRedisScanCall(call):
			u.scans = append(u.scans, site)
		}
		return true
	})
}

// isRedisScanCall reports whether call has the go-redis SCAN signature
// Scan(ctx, cursor, match, count), as opposed to a database row Scan whose
// arguments are pointers.
func isRedisScanCall(call *ast.CallExpr) bool {
	if len(call.Args) != 4 {
		return false
	}
	for _, a := range call.Args {
		if u, ok := a.(*ast.UnaryExpr); ok && u.Op == token.AND {
			return false
		}
	}
	return true
}

// testsInstallSlotGuard reports whether any _test.go file directly in dir
// installs the slot hook or builds an in-process cluster.
func testsInstallSlotGuard(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		s := string(b)
		if strings.Contains(s, "slotguard.Install(") || strings.Contains(s, "clustertest.New(") {
			return true
		}
	}
	return false
}

func sortedRedisPackages(m map[string]*redisUsage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// spec: §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3). The
// scanner itself is pinned on a fixture: a bare SCAN, a script, a
// transaction, and a spread DEL are recorded, while a database row Scan and
// a single-key DEL are not.
func TestRedisUsageScannerRecognizesSites_spec_12_4(t *testing.T) {
	const src = `package fixture

import "github.com/redis/go-redis/v9"

var script = redis.NewScript("return 1")

func f(ctx context.Context, c redis.UniversalClient, rows interface{ Scan(...any) error }, keys []string) {
	c.Scan(ctx, 0, "cb:*", 100)
	c.TxPipelined(ctx, nil)
	c.Del(ctx, keys...)
	c.Del(ctx, "one")
	var a, b, d, e int
	_ = rows.Scan(&a, &b, &d, &e)
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "/repo/pkg/fixture/f.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if !importsGoRedis(f) {
		t.Fatal("fixture: go-redis import not recognized")
	}
	out := map[string]*redisUsage{}
	recordRedisCalls(fset, "/repo", f, "pkg/fixture", out)
	u := out["pkg/fixture"]
	if u == nil || len(u.scans) != 1 || len(u.multiKey) != 3 {
		t.Fatalf("recorded %+v, want 1 SCAN and 3 multi-key sites (NewScript, TxPipelined, spread Del)", u)
	}
}
