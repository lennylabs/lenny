// SPDX-License-Identifier: MIT

// Package scenkit collects the shared helpers tier-7a scenarios use.
// Each helper exists because a pattern was duplicated verbatim across
// scenarios; centralising the pattern reduces line count, prevents the
// "forgot the drain" / "forgot the ctx-cancel tolerance" class of bug
// from re-occurring, and gives scenario authors one canonical idiom.
//
// Surface:
//
//   - HTTPClient()             — the canonical connection-pooled client
//     used by every HTTP-driven scenario.
//   - DoJSON()                 — full HTTP request → drain → close →
//     ctx-cancel tolerance, returning
//     (status, body, error).
//   - Counters                 — named atomic counters with Inc / Get
//     / EmitTo helpers.
//   - InProcMixin              — embeddable struct providing the
//     inproc.Env Setup / Teardown lifecycle.
//
// TESTING.md §12.7.a (Wave 7 follow-up: scenario authoring kit).
package scenkit
