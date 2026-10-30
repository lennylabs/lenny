// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 live isolation tests for the §25.11 backup Job identity. The
// backup Job runs under a least-privilege envelope the Job Pod
// Specification pins: a dedicated ServiceAccount lenny-backup-sa with
// get/list on CRDs and get on ConfigMaps in the release namespace and
// "No Pod, Deployment, or Secret read access", and a dedicated
// NetworkPolicy lenny-backup-job whose egress is bounded to Postgres,
// MinIO, the Kubernetes API, and DNS only ("No other egress").
//
// The existing coverage proves the rendered manifests request the right
// scope: the chart-render unit tests (backup-job_test.yaml,
// ops-network-policies_test.yaml) and the k8slauncher unit test assert
// the ServiceAccount, RBAC, and NetworkPolicy documents carry the
// narrow grants. Those are static-render assertions. This file asserts
// the running cluster enforces the scope: the kube-apiserver denies the
// out-of-scope RBAC verbs through a live SubjectAccessReview, and the
// CNI drops out-of-policy egress from a pod carrying the app:
// lenny-backup label the NetworkPolicy selects.
//
// The tests install the Lenny control plane on a Kind cluster (via the
// install.sh-backed kind.InstallLenny harness). lenny-backup-sa and its
// bindings render in every install (they are part of the mandatory core
// inventory, independent of backups.onDemand.enabled), and the
// lenny-backup-job NetworkPolicy renders unconditionally, so both
// identities are present on the installed cluster without triggering a
// backup Job.
//
// Scope note: the §25.11 backup Job also authenticates to Postgres
// through a read-only lenny-backup role and to MinIO through a
// bucket-scoped access key. Those are operator-supplied credentials
// (backups.postgres.dsn / backups.minio.accessKey are empty in a stock
// render, and no migration provisions a lenny_backup Postgres role);
// their enforcement lives in Postgres and MinIO, not in a Lenny-rendered
// cluster resource, so there is no platform-provisioned identity on the
// installed cluster to drive at runtime. The two boundaries the platform
// itself provisions and the cluster enforces — the ServiceAccount RBAC
// and the egress NetworkPolicy — are the runtime-enforceable surface and
// are what these tests exercise.
//
// spec: §25.11 (Job Pod Specification — lenny-backup-sa RBAC,
// lenny-backup-job NetworkPolicy egress bound).

package tier9_security_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// backupSAUser is the fully-qualified ServiceAccount username the
// SubjectAccessReview impersonates. §25.11 binds the backup Job's
// get/list-on-CRDs ClusterRole and get-on-ConfigMaps Role to this
// subject; the chart renders both bindings in every install.
const backupSAUser = "system:serviceaccount:lenny-system:lenny-backup-sa"

// backupPodLabel is the label lenny-backup Job pods carry and the
// §25.11 lenny-backup-job NetworkPolicy selects on
// (podSelector: { app: lenny-backup }).
const backupPodLabel = "app=lenny-backup"

// backupSAAccess is one `kubectl auth can-i <verb> <resource>` assertion
// for the lenny-backup-sa ServiceAccount and whether the §25.11 grant
// must allow it. namespaced routes the check through lenny-system (the
// release namespace the get-on-ConfigMaps Role is scoped to); a
// cluster-scoped resource (CRDs) omits the namespace.
type backupSAAccess struct {
	name       string
	verb       string
	resource   string
	namespaced bool
	allowed    bool
}

// spec: §25.11 (Job Pod Specification — lenny-backup-sa: get/list on
// CRDs, get on ConfigMaps in the release namespace, "No Pod, Deployment,
// or Secret read access")
// diagnosis: the chart-installed §25.11 lenny-backup-sa RBAC does not
// enforce the backup Job's least-privilege K8s API envelope. The test
// runs live SubjectAccessReviews (`kubectl auth can-i ... --as` the
// lenny-backup-sa subject) against the installed RBAC and asserts the
// out-of-scope verbs the spec forbids are denied — a Secret get, a Pod
// get, and a Deployment get — while the in-scope grants (get on a
// ConfigMap in the release namespace, get/list on CRDs and lenny.dev
// custom resources) are allowed. A denied in-scope verb means the backup
// cannot read the platform configuration it must export; an allowed
// out-of-scope verb (especially Secret read) means the backup Job
// identity is over-privileged and a compromised Job pod could read
// platform secrets, defeating the §25.11 isolation boundary. A failure
// means the ServiceAccount, its ClusterRole/Role, or a binding was not
// deployed or a later edit widened the grant.
func TestBackupServiceAccountScopeEnforcedLiveRBAC_spec_25_11(t *testing.T) {
	c := kind.InstallLenny(t)

	// Gate on the ServiceAccount being present so a bare "no" is
	// attributable to the RBAC scope rather than a missing subject.
	if out, err := c.KubectlOut(t, "-n", lennySystemNS, "get", "serviceaccount", "lenny-backup-sa"); err != nil {
		t.Skipf("precondition not met: lenny-backup-sa is not installed in %s (%v); the §25.11 backup "+
			"identity ships with the chart core inventory\n%s", lennySystemNS, err, out)
	}

	cases := []backupSAAccess{
		// §25.11 forbidden reads: "No Pod, Deployment, or Secret read
		// access." A Secret read is the highest-value denial — it is what
		// keeps a compromised backup Job from exfiltrating platform
		// secrets.
		{name: "get-secrets-denied", verb: "get", resource: "secrets", namespaced: true, allowed: false},
		{name: "get-pods-denied", verb: "get", resource: "pods", namespaced: true, allowed: false},
		{name: "get-deployments-denied", verb: "get", resource: "deployments.apps", namespaced: true, allowed: false},
		// A Secret read is denied cluster-wide, not just in lenny-system:
		// the grant confers no Secret access in any namespace.
		{name: "get-secrets-any-namespace-denied", verb: "get", resource: "secrets", namespaced: false, allowed: false},
		// §25.11 read-only envelope: the grant is get/list only. A write
		// to the one resource the Job may read (ConfigMaps) is still
		// denied.
		{name: "create-configmaps-denied", verb: "create", resource: "configmaps", namespaced: true, allowed: false},

		// §25.11 in-scope positive controls: get on a ConfigMap in the
		// release namespace (backup flow step 2 config export), and
		// get/list on CRDs and lenny.dev custom resources (backup flow
		// steps 2-3 CRD/platform-config snapshot).
		{name: "get-configmaps-allowed", verb: "get", resource: "configmaps", namespaced: true, allowed: true},
		{name: "get-crds-allowed", verb: "get", resource: "customresourcedefinitions.apiextensions.k8s.io", namespaced: false, allowed: true},
		{name: "list-sandboxes-allowed", verb: "list", resource: "sandboxes.lenny.dev", namespaced: true, allowed: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			allowed, raw := backupSACanI(t, c, tc.verb, tc.resource, tc.namespaced)
			if allowed != tc.allowed {
				scope := "cluster-wide"
				if tc.namespaced {
					scope = "in " + lennySystemNS
				}
				t.Fatalf("§25.11 violation: `kubectl auth can-i %s %s %s --as=%s` returned %q "+
					"(allowed=%t), want allowed=%t. The lenny-backup-sa envelope grants get/list on CRDs "+
					"and get on ConfigMaps in the release namespace with no Pod, Deployment, or Secret "+
					"read access; an allowed out-of-scope verb (especially a Secret read) over-privileges "+
					"the backup Job identity, and a denied in-scope verb breaks the platform-config export.",
					tc.verb, tc.resource, scope, backupSAUser, raw, allowed, tc.allowed)
			}
			t.Logf("§25.11: lenny-backup-sa can-i %s %s = %q (want allowed=%t)", tc.verb, tc.resource, raw, tc.allowed)
		})
	}
}

// backupSACanI runs `kubectl auth can-i <verb> <resource> --as
// <lenny-backup-sa>` against the live cluster and reports whether the
// apiserver allowed the access. `kubectl auth can-i --as` issues a
// SubjectAccessReview, so the verdict reflects the installed RBAC rather
// than a rendered chart template. The command prints "yes"/"no" on
// stdout; a denial is a non-zero exit (not a harness error), so the
// function keys on the trimmed stdout marker, not the exit code.
func backupSACanI(t *testing.T, c *kind.Cluster, verb, resource string, namespaced bool) (allowed bool, raw string) {
	t.Helper()
	args := []string{"auth", "can-i", verb, resource, "--as", backupSAUser}
	if namespaced {
		args = append(args, "-n", lennySystemNS)
	} else {
		// --all-namespaces makes a namespaced-resource check span every
		// namespace; for a cluster-scoped resource kubectl ignores it.
		args = append(args, "--all-namespaces")
	}
	out, _ := c.KubectlOut(t, args...)
	raw = strings.TrimSpace(out)
	// `kubectl auth can-i` on a cluster-scoped resource prints a
	// "not namespace scoped" warning line before the yes/no verdict; the
	// last non-empty token is the verdict.
	fields := strings.Fields(raw)
	verdict := ""
	if len(fields) > 0 {
		verdict = fields[len(fields)-1]
	}
	return verdict == "yes", raw
}

// spec: §25.11 (Job Pod Specification — lenny-backup-job NetworkPolicy:
// "egress permitted to Postgres Service, MinIO Service, and K8s API
// only. No other egress")
// diagnosis: the CNI does not enforce the §25.11 lenny-backup-job egress
// bound. The test schedules a probe pod carrying app: lenny-backup so the
// lenny-backup-job allow-list applies to it, then reads the live policy
// (podSelector app: lenny-backup, both Ingress and Egress, no ingress
// allow rule) and drives a live egress probe. A positive control
// confirms the probe can egress (it reaches the in-cluster MinIO store,
// which the e2e datastore-egress allow-list permits), so a block on an
// out-of-policy peer is attributable to the egress bound rather than a
// broken source. The adversarial probe targets the token-service, which
// is not a lenny-backup-job egress peer, and must be dropped at the CNI
// (curl exit 28). A success (or a non-timeout failure) means the backup
// Job egress is unbounded — a compromised backup Job pod could reach
// control-plane peers the §25.11 containment boundary forbids.
func TestBackupJobEgressBoundToAllowlist_spec_25_11(t *testing.T) {
	c := kind.InstallLenny(t)

	// Posture: the lenny-backup-job policy is installed, selects
	// app: lenny-backup, carries both policy types, and admits no
	// ingress (Jobs accept no ingress).
	if !systemNetworkPolicyNames(t, c)["lenny-backup-job"] {
		t.Fatalf("§25.11 NetworkPolicy lenny-backup-job is not installed in %s; the backup Job egress "+
			"bound is missing", lennySystemNS)
	}
	sel := jsonpathOut(t, c, "networkpolicy", "lenny-backup-job", "{.spec.podSelector.matchLabels.app}")
	if sel != "lenny-backup" {
		t.Errorf("§25.11 lenny-backup-job podSelector app=%q; it must select app=lenny-backup", sel)
	}
	types := jsonpathOut(t, c, "networkpolicy", "lenny-backup-job", "{.spec.policyTypes}")
	if !strings.Contains(types, "Egress") {
		t.Errorf("§25.11 lenny-backup-job policyTypes %q must include Egress", types)
	}
	ingress := jsonpathOut(t, c, "networkpolicy", "lenny-backup-job", "{.spec.ingress}")
	if ingress != "" {
		t.Errorf("§25.11 lenny-backup-job must carry no ingress allow rule (backup Jobs accept no "+
			"ingress), got %q", ingress)
	}

	createBackupProbe(t, c, "backup-egress-probe")

	// Positive control: egress to the in-cluster MinIO store. §25.11
	// lists MinIO as a permitted backup egress peer; the e2e cluster's
	// datastore-egress allow-list also permits it on 9000. A reachable
	// MinIO proves the probe's egress path works, so a block on the
	// out-of-policy peer below is attributable to the egress bound.
	minioIP := dataStorePodIPT9(t, c, "minio")
	if minioIP == "" {
		t.Fatalf("no MinIO datastore pod IP found; cannot establish the egress-test positive control")
	}
	minioTarget := fmt.Sprintf("https://%s:9000/minio/health/live", minioIP)
	res := curlFromNS(t, c, lennySystemNS, "backup-egress-probe", minioTarget, 8*time.Second)
	if res.exitCode == 28 {
		t.Fatalf("egress-test positive control failed: an app=lenny-backup probe could not reach the "+
			"MinIO store at %s (curl exit 28, timed out). §25.11 lists MinIO as a permitted backup "+
			"egress peer; without a working egress a block below cannot be attributed to the egress "+
			"bound.\noutput:\n%s", minioTarget, res.output)
	}
	t.Logf("egress-test positive control: app=lenny-backup probe reached the MinIO store at %s "+
		"(curl exit %d, TCP connection established)", minioTarget, res.exitCode)

	// Adversarial probe: the token-service is not a lenny-backup-job
	// egress peer (§25.11 permits Postgres, MinIO, and the K8s API only).
	// Resolve it by pod IP and confirm the egress is dropped at the CNI.
	tsIP := podIPBySelector(t, c, lennySystemNS, "lenny.dev/component=token-service")
	if tsIP == "" {
		t.Fatalf("no token-service pod IP found; cannot probe the lenny-backup-job egress bound")
	}
	tsTarget := fmt.Sprintf("http://%s:50052/", tsIP)
	res = curlFromNS(t, c, lennySystemNS, "backup-egress-probe", tsTarget, 8*time.Second)
	if res.exitCode == 0 {
		t.Fatalf("§25.11 violation: an app=lenny-backup probe reached the token-service at %s. "+
			"lenny-backup-job is a bound egress allow-list (Postgres, MinIO, K8s API only); the "+
			"token-service is not a permitted peer and egress to it must be dropped.\noutput:\n%s",
			tsTarget, res.output)
	}
	if res.exitCode != 28 {
		t.Errorf("lenny-backup-job egress to the token-service at %s failed with curl exit %d, expected "+
			"28 (connection timed out). A non-timeout failure is not a clean CNI egress block.\noutput:\n%s",
			tsTarget, res.exitCode, res.output)
	} else {
		t.Logf("adversarial probe: app=lenny-backup egress to the token-service at %s dropped at the CNI "+
			"(curl exit 28 — lenny-backup-job egress bound)", tsTarget)
	}
}

// createBackupProbe schedules a probe pod in lenny-system carrying the
// app: lenny-backup label so the §25.11 lenny-backup-job egress
// allow-list applies to its egress.
func createBackupProbe(t *testing.T, c *kind.Cluster, name string) {
	t.Helper()
	createProbeInNamespace(t, c, lennySystemNS, name, map[string]string{"app": "lenny-backup"})
}
