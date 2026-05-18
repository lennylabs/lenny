// SPDX-License-Identifier: MIT

// Package migrations embeds the numbered Postgres schema migrations so
// the lenny-migrate runner and any embedded startup runner can apply
// them without a checkout of the source tree. The .sql files remain
// the source of truth; this file only exposes them as an embed.FS.
package migrations

import "embed"

// FS holds every numbered up/down migration (NNNN_*.up.sql and
// NNNN_*.down.sql). It is consumed through the golang-migrate iofs
// source driver.
//
//go:embed *.sql
var FS embed.FS
