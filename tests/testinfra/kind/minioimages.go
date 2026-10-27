// SPDX-License-Identifier: MIT

package kind

// The S3 server and client images the e2e data-store fixtures run. The
// community minio/minio and minio/mc images are no longer distributed, so
// the fixtures run the maintained forks pgsty/silo and pgsty/mc, each
// pinned by release tag and by the digest of its multi-arch index.
// install.sh pulls both on the host, loads them onto the Kind nodes, and
// registers each digest-pinned name, so a pod that names one of these
// references with imagePullPolicy IfNotPresent resolves it without a pull.
// The same references appear in install.sh, tests/testinfra/k8s/datastores.yaml,
// datastores-ha-minio.yaml, the compose files, and the chart defaults;
// TestMinIOImagePinsAgree holds them equal.
const (
	// MinIOServerImage is the S3 server image.
	MinIOServerImage = "pgsty/silo:RELEASE.2026-10-26T00-00-00Z@sha256:7bb2aff6b89efe1cb1303c10fc21d579447434ed06649ac9ec05a5e2dbf30ad7"
	// MinIOClientImage is the mc client image. It carries /bin/sh, which the
	// one-shot bucket pods run their mc scripts under.
	MinIOClientImage = "pgsty/mc:RELEASE.2026-10-26T00-00-00Z@sha256:ce62c9053c69ce12a527a322345b5ad9f44b5f8a7b8189f82e516f2e3f30f1d8"
)
