// SPDX-License-Identifier: MIT

// Package runtimeexec holds the message model a session's executor
// exchanges with a runtime, the runtime's §28.5.3 JSONL message and
// response frames with their ingestion, and the subprocess executor that
// drives a runtime binary over its stdin and stdout.
//
// The gateway's executors (pkg/gateway/core/session/executor) alias the
// message model and encode and ingest the frames through this package, and
// the adapter binary (cmd/lenny-adapter) builds its developer-loop runtime
// from the subprocess executor, so the adapter imports no gateway package,
// which the gateway layout gate refuses outside pkg/gateway and
// cmd/lenny-gateway.
//
// spec: RTPMSG, RTPPART
package runtimeexec
