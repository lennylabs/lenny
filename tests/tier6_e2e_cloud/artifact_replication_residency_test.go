// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 behavioral test for §25.11 ArtifactStore cross-region
// replication residency. It exercises the provider-native replication
// path (S3 / GCS / Azure Blob) and the runtime residency preflight's
// jurisdiction-tag probe against real, tagged destination buckets —
// the surface that tier-1/tier-2 exercise only over a FakeDriver and
// that no provider validates end-to-end.
//
// The deployment under test must be pre-configured (by the per-provider
// cloud harness) with two source regions:
//
//   - a same-jurisdiction region whose
//     minio.regions.<region>.artifactBackup.target points at a
//     destination bucket carrying an lenny.dev/jurisdiction-region tag
//     equal to the source region's dataResidencyRegion, and
//   - a cross-jurisdiction region whose destination bucket carries a
//     mismatched (or missing) lenny.dev/jurisdiction-region tag.
//
// The bucket names, jurisdiction tags, and the two region identifiers
// are provisioned per provider and surfaced through the LENNY_ARTIFACT_REPL_*
// env vars below; the test skips (non-blocking, matching the managed_rds
// convention) when they are unset.

package tier6_e2e_cloud_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// artifactReplParams carries the two pre-configured source regions and
// the same-region destination's expected jurisdiction tag. The cloud
// harness (scripts/cloud/<provider>/up.sh) provisions the destination
// buckets and tags and exports these; the test skips when the
// same-region identifier is empty.
type artifactReplParams struct {
	sameRegion             string
	sameRegionJurisdiction string
	crossRegion            string
}

// requireArtifactReplication reads the LENNY_ARTIFACT_REPL_* env vars
// and returns the region identifiers, or a zero value with ok=false
// when the same-region identifier is unset (the default when the cloud
// harness has not provisioned the tagged destination buckets).
func requireArtifactReplication(t *testing.T) (artifactReplParams, bool) {
	t.Helper()
	same := strings.TrimSpace(os.Getenv("LENNY_ARTIFACT_REPL_SAME_REGION"))
	if same == "" {
		t.Log("requireArtifactReplication: LENNY_ARTIFACT_REPL_SAME_REGION is empty; " +
			"provision a same-jurisdiction and a cross-jurisdiction destination bucket " +
			"(each tagged lenny.dev/jurisdiction-region) via scripts/cloud/<provider>/up.sh " +
			"and export LENNY_ARTIFACT_REPL_SAME_REGION, LENNY_ARTIFACT_REPL_SAME_REGION_JURISDICTION, " +
			"and LENNY_ARTIFACT_REPL_CROSS_REGION")
		return artifactReplParams{}, false
	}
	jur := strings.TrimSpace(os.Getenv("LENNY_ARTIFACT_REPL_SAME_REGION_JURISDICTION"))
	if jur == "" {
		// The residency invariant is that the destination's advertised
		// jurisdiction equals the source region's dataResidencyRegion,
		// which defaults to the region identifier itself.
		jur = same
	}
	return artifactReplParams{
		sameRegion:             same,
		sameRegionJurisdiction: jur,
		crossRegion:            strings.TrimSpace(os.Getenv("LENNY_ARTIFACT_REPL_CROSS_REGION")),
	}, true
}

// spec: §25.11 (ArtifactStore Backup — Runtime residency preflight, lines
// 4077-4079; status endpoint line 3903; resume endpoint line 3902;
// ARTIFACT_REPLICATION_REGION_UNRESOLVABLE line 4341).
// diagnosis: this is the only end-to-end exercise of the §25.11
// provider-native ArtifactStore replication residency control against a
// real cloud destination bucket. The runtime preflight issues a
// jurisdiction-tag probe (s3:GetBucketTagging or provider equivalent)
// against the destination and compares the returned
// lenny.dev/jurisdiction-region tag to the source region's
// dataResidencyRegion. A same-jurisdiction destination must leave
// replication `active` with the matching destination jurisdiction tag;
// a cross-jurisdiction destination must be suspended
// (`suspended_residency_violation`) and any admin query or resume must
// surface ARTIFACT_REPLICATION_REGION_UNRESOLVABLE. A failure here means
// the provider-native probe, the tag comparison, or the fail-closed
// suspension does not hold on real S3/GCS/Azure, which the FakeDriver
// tier-1/tier-2 coverage cannot detect (deployment-combination gap
// across GKE/EKS/AKS).
func TestCloudArtifactReplicationResidency(t *testing.T) {
	_ = requireCloud(t)
	params, ok := requireArtifactReplication(t)
	if !ok {
		return
	}
	cli := kube(t)
	requireGatewayInstalled(t, cli)

	_, baseURL, stop := portForwardGatewayCloud(t)
	defer stop()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	adminHeaders := func(req *http.Request) {
		req.Header.Set("X-Lenny-Tenant-ID", "platform")
		req.Header.Set("X-Lenny-Roles", "platform-admin")
		req.Header.Set("X-Lenny-User-ID", "alice")
	}
	get := func(path string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, baseURL+path, nil)
		adminHeaders(req)
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	post := func(path string, body any) (int, []byte) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		adminHeaders(req)
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}

	type replState struct {
		Region                     string `json:"region"`
		Status                     string `json:"status"`
		DestinationBucket          string `json:"destinationBucket"`
		DestinationJurisdictionTag string `json:"destinationJurisdictionTag"`
	}
	type errEnvelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}

	// 1. Same-jurisdiction destination: the runtime preflight's
	// jurisdiction-tag probe succeeds, so replication stays `active`
	// and the status carries the destination's advertised jurisdiction
	// tag (equal to the source region's dataResidencyRegion). §25.11
	// lines 4077-4079.
	status, body := get("/v1/admin/artifact-replication/" + params.sameRegion + "/status")
	if status != http.StatusOK {
		t.Fatalf("§25.11: GET status for same-jurisdiction region %q returned %d body %s; "+
			"expected 200 with an active replication state",
			params.sameRegion, status, body)
	}
	var same replState
	if err := json.Unmarshal(body, &same); err != nil {
		t.Fatalf("decode same-region status %q: %v", body, err)
	}
	if same.Status != "active" {
		t.Errorf("§25.11: same-jurisdiction region %q replication status = %q, want %q "+
			"(a same-jurisdiction destination must not be suspended)",
			params.sameRegion, same.Status, "active")
	}
	if same.DestinationJurisdictionTag != params.sameRegionJurisdiction {
		t.Errorf("§25.11: same-jurisdiction region %q destinationJurisdictionTag = %q, want %q "+
			"(the status must surface the destination bucket's lenny.dev/jurisdiction-region tag "+
			"read by the preflight probe)",
			params.sameRegion, same.DestinationJurisdictionTag, params.sameRegionJurisdiction)
	}

	// 2. Cross-jurisdiction destination: the cross-region prohibition
	// (§25.11 line 4075) means the preflight tag comparison fails and
	// replication is suspended with suspended_residency_violation
	// (§25.11 line 4077). Only run when a cross-region fixture is
	// provisioned.
	if params.crossRegion == "" {
		t.Log("§25.11: LENNY_ARTIFACT_REPL_CROSS_REGION unset; skipping the cross-jurisdiction " +
			"suspension assertion (provision a mismatched-tag destination bucket to exercise it)")
		return
	}
	status, body = get("/v1/admin/artifact-replication/" + params.crossRegion + "/status")
	if status != http.StatusOK {
		t.Fatalf("§25.11: GET status for cross-jurisdiction region %q returned %d body %s; "+
			"expected 200 with a suspended state",
			params.crossRegion, status, body)
	}
	var cross replState
	if err := json.Unmarshal(body, &cross); err != nil {
		t.Fatalf("decode cross-region status %q: %v", body, err)
	}
	if cross.Status != "suspended_residency_violation" {
		t.Errorf("§25.11: cross-jurisdiction region %q replication status = %q, want %q "+
			"(a destination whose lenny.dev/jurisdiction-region tag does not match the source "+
			"region must be fail-closed suspended)",
			params.crossRegion, cross.Status, "suspended_residency_violation")
	}

	// 3. Resume re-runs the preflight synchronously; because the
	// jurisdiction mismatch persists, the resume is rejected with the
	// PERMANENT 422 ARTIFACT_REPLICATION_REGION_UNRESOLVABLE and
	// replication stays suspended (§25.11 lines 3902, 4341).
	status, body = post("/v1/admin/artifact-replication/"+params.crossRegion+"/resume",
		map[string]any{"justification": "tier-6 residency preflight verification"})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("§25.11: resume of cross-jurisdiction region %q returned %d body %s, want 422 "+
			"(a persisting jurisdiction mismatch must not auto-resume)",
			params.crossRegion, status, body)
	}
	var env errEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode resume error %q: %v", body, err)
	}
	if env.Error.Code != "ARTIFACT_REPLICATION_REGION_UNRESOLVABLE" {
		t.Errorf("§25.11: resume of cross-jurisdiction region %q returned error code %q, want %q",
			params.crossRegion, env.Error.Code, "ARTIFACT_REPLICATION_REGION_UNRESOLVABLE")
	}

	// Replication must remain suspended after the rejected resume.
	status, body = get("/v1/admin/artifact-replication/" + params.crossRegion + "/status")
	if status == http.StatusOK {
		var after replState
		if err := json.Unmarshal(body, &after); err == nil && after.Status == "active" {
			t.Errorf("§25.11: cross-jurisdiction region %q became active after a rejected resume; "+
				"a residency mismatch is a hard compliance fault with no silent resume",
				params.crossRegion)
		}
	}
}
