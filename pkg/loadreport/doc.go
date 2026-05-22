// SPDX-License-Identifier: MIT

// Package loadreport generates the tier-12 HTML report. Given a Run
// manifest and the associated metric snapshots, Render produces a
// single self-contained HTML document with Plotly.js charts loaded
// from CDN and all data inlined as JSON in the page.
//
// The report is uploaded to object storage by lenny-loadctl as
// runs/<run-id>/report.html. It opens directly from object storage
// with no server.
//
// TESTING.md §12.12 and §24.1 (Wave 6 report generator).
package loadreport
