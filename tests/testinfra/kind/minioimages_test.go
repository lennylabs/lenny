// SPDX-License-Identifier: MIT

package kind

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// withdrawnMinIOImage matches an image reference to a withdrawn community
// MinIO image or to the retired source-built lenny-minio image. It leaves
// the lenny-minio Service, Secret, and endpoint names alone.
var withdrawnMinIOImage = regexp.MustCompile(`(?m)(image:|--image=|repository:)\s*"?(lenny-minio[:@]|minio/minio|minio/mc|quay\.io/minio)`)

// TestMinIOImagePinsAgree holds every carrier of the S3 server and client
// image references to the pinned pgsty/silo and pgsty/mc references, and
// rejects an image reference to the withdrawn community images or to the
// retired source-built lenny-minio image. A carrier that drifts from the
// others deploys an image install.sh never loaded, and the pod stalls in
// ImagePullBackOff on the air-gapped Kind nodes; a carrier that names a
// withdrawn image cannot be pulled on a fresh host.
//
// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind), 17.9.5 (Self-Managed Backends)
func TestMinIOImagePinsAgree(t *testing.T) {
	silo := strings.SplitN(MinIOServerImage, "@", 2)
	siloRepo, siloTag, _ := strings.Cut(silo[0], ":")

	cases := []struct {
		path string
		want []string
	}{
		{"tests/testinfra/kind/install.sh", []string{
			`MINIO_SERVER_IMAGE="` + MinIOServerImage + `"`,
			`MINIO_CLIENT_IMAGE="` + MinIOClientImage + `"`,
		}},
		{"tests/testinfra/k8s/datastores.yaml", []string{
			"image: " + MinIOServerImage,
			"image: " + MinIOClientImage,
		}},
		{"tests/testinfra/kind/datastores-ha-minio.yaml", []string{
			"image: " + MinIOServerImage,
			"image: " + MinIOClientImage,
		}},
		{"tests/testinfra/containers/minio.go", []string{
			`defaultMinIOImage = "` + MinIOServerImage + `"`,
		}},
		{"docker-compose.yml", []string{
			"image: " + MinIOServerImage,
			"image: " + MinIOClientImage,
		}},
		{"compose/default.yml", []string{
			"image: " + MinIOServerImage,
		}},
		{"charts/lenny/values.yaml", []string{
			"repository: " + siloRepo,
			`tag: "` + siloTag + `"`,
			`digest: "` + silo[1] + `"`,
			`image: "` + MinIOClientImage + `"`,
		}},
		{"charts/lenny/templates/minio-bucket-lifecycle-job.yaml", []string{
			`default "` + MinIOClientImage + `"`,
		}},
		{"tests/testinfra/kind/e2e-values.yaml", nil},
		{"tests/tier5_e2e_kind/restore_test_cronjob_dispatch_test.go", []string{
			"image: ` + kind.MinIOClientImage + `",
		}},
		{"tests/tier8_chaos/store_failure_test.go", []string{
			`"--image="+kind.MinIOClientImage`,
		}},
	}

	root := filepath.Join("..", "..", "..")
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tc.path)))
			if err != nil {
				t.Fatalf("read %s: %v", tc.path, err)
			}
			body := string(b)
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("%s does not carry %q", tc.path, w)
				}
			}
			if m := withdrawnMinIOImage.FindString(body); m != "" {
				t.Errorf("%s names a withdrawn or retired MinIO image: %q", tc.path, m)
			}
		})
	}
}

// TestMinIOImageWithdrawnPatternMatches pins the withdrawn-image matcher
// TestMinIOImagePinsAgree applies, so a matcher that silently stops
// matching cannot pass a regressed carrier.
//
// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
func TestMinIOImageWithdrawnPatternMatches(t *testing.T) {
	for _, line := range []string{
		"image: lenny-minio:e2e",
		`image: "minio/mc:latest"`,
		"image: minio/minio:RELEASE.2025-04-22T22-12-26Z",
		"repository: quay.io/minio/minio",
		`"--image=lenny-minio:e2e"`,
	} {
		if !withdrawnMinIOImage.MatchString(line) {
			t.Errorf("matcher misses %q", line)
		}
	}
	for _, line := range []string{
		"image: " + MinIOServerImage,
		"image: " + MinIOClientImage,
		"endpoint: lenny-minio:9443",
		"name: lenny-minio",
	} {
		if withdrawnMinIOImage.MatchString(line) {
			t.Errorf("matcher flags %q", line)
		}
	}
}
