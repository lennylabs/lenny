// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency checks for the pod-to-object-store checkpoint
// path. The §13.1 Pod Security control table now records that the agent pod
// reaches object storage for checkpoint chunks against a gateway-minted
// presigned capability (single key, single method, exact Content-Length on a
// PUT, short expiry) rather than an object-store credential, and that this is
// the one exception to gateway-mediated file delivery. The reader-facing
// mirrors of that table (getting-started/architecture.md,
// operator-guide/security.md) and the concepts.md "Gateway-mediated file
// delivery" prose must agree; none of them may still assert that pods have no
// object-store path.
//
// The §16.1 workspace-seal metric now carries an `outcome="permanent"` arm for
// a seal that failed on a permanent adapter error and returned immediately
// without retrying, and the `WorkspaceSealStuck` alert fires on the
// `outcome="timeout"` arm alone. The metrics.md row and the
// workspace-seal-stuck.md runbook must reflect both.
//
// These checks read repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 13.1 (Credentials / File delivery / Checkpoint transfer rows),
// 16.1 (lenny_workspace_seal_duration_seconds outcome enum + timeout-scoped
// WorkspaceSealStuck alert).

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// spec: 13.1
// diagnosis: the reader-facing §13.1 control-table mirrors or the concepts.md
//
//	file-delivery prose disagree with the amended §13.1 on the pod-to-object-
//	store checkpoint path. The spec grants the agent pod a gateway-minted
//	presigned capability (single key, single method, short expiry) to PUT and
//	GET checkpoint chunks, the one exception to gateway-mediated file delivery.
//	A failure here means a page still asserts the pre-fix posture — pods have
//	no object-store path at all (architecture.md's "Access MinIO, Postgres, or
//	Redis directly" in the Pods CANNOT column, or concepts.md's "no direct
//	object store access from pods") — leaving an operator or reviewer with a
//	control-table mirror that contradicts the shipped presigned-capability
//	grant.
func TestCheckpointPresignedCapabilityMirroredIn131(t *testing.T) {
	root := repoRoot(t)

	// architecture.md §13.1 mirror: the Pod security settings table.
	arch := readDocPage(t, filepath.Join(root, "docs", "getting-started", "architecture.md"))
	podSec := section(arch, "Pod security settings")
	if podSec == "" {
		t.Fatal("architecture.md: 'Pod security settings' section not found (renamed or removed?)")
	}
	archCred := requireLine(t, podSec, "| Credentials |")
	requireAllContain(t, "architecture.md §13.1 Credentials row", archCred, []string{
		"gateway-minted presigned capability",
		"never as an object-store credential",
	})
	archDelivery := requireLine(t, podSec, "| File delivery |")
	requireAllContain(t, "architecture.md §13.1 File delivery row", archDelivery, []string{
		"Checkpoint chunk objects are the one exception",
		"validates the tenant prefix",
	})

	// architecture.md "What pods can and cannot do": the object-store row moves
	// to the Pods CAN column; the pre-fix blanket denial must be gone.
	canTable := section(arch, "What pods can and cannot do")
	if canTable == "" {
		t.Fatal("architecture.md: 'What pods can and cannot do' section not found (renamed or removed?)")
	}
	if !strings.Contains(canTable, "checkpoint chunk objects to object storage against a gateway-minted presigned capability") {
		t.Error("architecture.md 'What pods can and cannot do' table does not list the checkpoint-chunk object-store path in the Pods CAN column; the amended §13.1 grants the pod a presigned capability")
	}
	if strings.Contains(canTable, "Access MinIO, Postgres, or Redis directly") {
		t.Error("architecture.md 'What pods can and cannot do' table still denies all object-store access (\"Access MinIO, Postgres, or Redis directly\" in Pods CANNOT); the amended §13.1 gives the pod a presigned checkpoint-chunk path")
	}

	// security.md §13.1 mirror: the Pod Security Controls / Security Context table.
	sec := readDocPage(t, filepath.Join(root, "docs", "operator-guide", "security.md"))
	secCtx := section(sec, "Security Context")
	if secCtx == "" {
		t.Fatal("security.md: 'Security Context' section not found (renamed or removed?)")
	}
	secCred := requireLine(t, secCtx, "| Credentials |")
	requireAllContain(t, "security.md §13.1 Credentials row", secCred, []string{
		"gateway-minted presigned capability",
		"never as an object-store credential",
	})
	secDelivery := requireLine(t, secCtx, "| File delivery |")
	requireAllContain(t, "security.md §13.1 File delivery row", secDelivery, []string{
		"Checkpoint chunk objects are the one exception",
		"validates the tenant prefix",
	})

	// concepts.md prose: state the exception, drop the blanket denial.
	concepts := readDocPage(t, filepath.Join(root, "docs", "getting-started", "concepts.md"))
	delivery := section(concepts, "Gateway-mediated file delivery")
	if delivery == "" {
		t.Fatal("concepts.md: 'Gateway-mediated file delivery' section not found (renamed or removed?)")
	}
	requireAllContain(t, "concepts.md gateway-mediated file delivery prose", delivery, []string{
		"Checkpoint chunk objects are the one exception",
		"the pod holds no object-store credential",
	})
	requireNoneContain(t, "concepts.md gateway-mediated file delivery prose", delivery, []string{
		"no direct object store access from pods",
	})
}

// spec: 16.1
// diagnosis: the metrics.md workspace-seal row or the workspace-seal-stuck.md
//
//	runbook disagree with the amended §16.1 on the seal-outcome enum. The spec
//	added an `outcome="permanent"` arm for a seal that failed on a permanent
//	adapter error and returned immediately without retrying, and scoped the
//	WorkspaceSealStuck alert to the `outcome="timeout"` arm alone. A failure
//	here means the operator reference still enumerates only success/timeout, or
//	the runbook does not tell the responder that a permanent-outcome seal fails
//	fast and does not fire this timeout-scoped alert, leaving them to chase a
//	non-existent retry loop for a seal that already returned.
func TestWorkspaceSealPermanentOutcomeMirroredIn161(t *testing.T) {
	root := repoRoot(t)

	// metrics.md: the seal histogram row carries the permanent outcome and the
	// timeout-scoped alert.
	metrics := readDocPage(t, filepath.Join(root, "docs", "reference", "metrics.md"))
	sealRow := lineContaining(metrics, "`lenny_workspace_seal_duration_seconds`")
	if sealRow == "" {
		t.Fatal("metrics.md has no lenny_workspace_seal_duration_seconds row")
	}
	requireAllContain(t, "metrics.md workspace-seal row", sealRow, []string{
		"`success`, `timeout`, `permanent`",
		"failed on a permanent adapter error and returned immediately without retrying",
		"`outcome=\"timeout\"` arm alone",
	})

	// workspace-seal-stuck.md: the runbook states the timeout-scoped alert and
	// the fail-fast permanent outcome.
	runbook := readDocPage(t, filepath.Join(root, "docs", "runbooks", "workspace-seal-stuck.md"))
	requireAllContain(t, "workspace-seal-stuck.md runbook", runbook, []string{
		"`outcome=\"timeout\"` arm",
		"`outcome=\"permanent\"`",
		"returns immediately without retrying",
	})
}
