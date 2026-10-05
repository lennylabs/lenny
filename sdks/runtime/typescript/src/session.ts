// SPDX-License-Identifier: MIT

// This module holds the per-session state of one runtime process. One
// runtime process serves every session the pod holds, one after another on
// a recycling pool and side by side on a concurrent pool. The adapter opens
// each session with session_start and ends it with session_end on
// CH-MSGSOCK, and the SDK keeps one SessionRecord per session_start it acts
// on. A record is keyed by the session's sessionId in the routing table
// while the session is live, and by the startId of the session_start that
// created it from its session_end until its context is released. Each
// record has its own promise chain, so one session's handler never holds up
// another session or the frame loop.
//
// spec: §4.7.10 (runtime process lifetime), §28.5.3 (CH-MSGSOCK, Inbound:
// session_start, Inbound: session_end, Outbound: session_started, Session
// errors), §15.7 (Handler).

import { readFile } from "node:fs/promises";
import type {
  CredentialBundle,
  ExperimentContext,
  LLMConfig,
  TerminationReason,
} from "./types.js";

// SessionStart is the decoded session_start frame.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start field table).
export interface SessionStart {
  sessionId: string;
  startId: string;
  credentialsPath: string;
  experimentContext?: ExperimentContext;
  tracingContext?: Record<string, string>;
  llm?: LLMConfig;
}

// decodeSessionStart reads the session_start fields the SDK uses. A member
// that is absent or null leaves its field undefined.
export function decodeSessionStart(raw: Record<string, unknown>): SessionStart {
  const str = (v: unknown): string => (typeof v === "string" ? v : "");
  const obj = (v: unknown): Record<string, unknown> | undefined =>
    v !== null && typeof v === "object" && !Array.isArray(v)
      ? (v as Record<string, unknown>)
      : undefined;
  const exp = obj(raw.experimentContext);
  const llm = obj(raw.llm);
  const tracing = obj(raw.tracingContext);
  const headers = llm ? obj(llm.headers) : undefined;
  return {
    sessionId: str(raw.sessionId),
    startId: str(raw.startId),
    credentialsPath: str(raw.credentialsPath),
    experimentContext: exp
      ? {
          experimentId: str(exp.experimentId),
          variantId: str(exp.variantId),
          inherited: exp.inherited === true,
        }
      : undefined,
    tracingContext: tracing ? stringMap(tracing) : undefined,
    llm: llm
      ? {
          deliveryMode: str(llm.deliveryMode),
          ...(typeof llm.dialect === "string" ? { dialect: llm.dialect } : {}),
          ...(typeof llm.apiKeyEnv === "string" ? { apiKeyEnv: llm.apiKeyEnv } : {}),
          ...(headers ? { headers: stringMap(headers) } : {}),
        }
      : undefined,
  };
}

// stringMap converts an object's values to strings.
function stringMap(o: Record<string, unknown>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(o)) {
    out[k] = String(v);
  }
  return out;
}

// SessionRecord is one session's context on this runtime process. The
// frame loop and the CH-RUNTIMEOPS loop run on the one event loop, so a
// field set synchronously by one is observed by the next callback of the
// other without a lock.
export class SessionRecord {
  readonly id: string;
  readonly startId: string;
  // tail is the session's promise chain: its creation, each message, and
  // its release run on it one after another.
  tail: Promise<void>;
  sequence = 0;
  credentials?: CredentialBundle;
  // createDone is set once the creation finished, after the record's own
  // session_started was written.
  createDone = false;
  // createError is the credential-read or onCreate failure, if any. It is
  // final once set, before session_started is written.
  createError?: Error;
  // endRead is set when the frame loop reads the session's session_end.
  endRead = false;
  // closeReason is the reason EOF or shutdown closed the session with.
  closeReason?: TerminationReason;
  // pendingAcks holds the startIds of duplicate session_start frames read
  // while the creation was still running.
  pendingAcks: string[] = [];
  // ended is the stdout drop mark: once the frame loop reads the session's
  // session_end, no response or tool_call for the record is written.
  ended = false;
  // released settles once the record's context is released. A later
  // record for the same sessionId runs its onCreate only after it.
  readonly released: Promise<void>;
  private resolveReleased!: () => void;

  constructor(
    readonly start: SessionStart,
    prev: SessionRecord | undefined,
  ) {
    this.id = start.sessionId;
    this.startId = start.startId;
    this.released = new Promise<void>((resolve) => {
      this.resolveReleased = resolve;
    });
    this.tail = prev ? prev.released : Promise.resolve();
  }

  // markReleased settles released.
  markReleased(): void {
    this.resolveReleased();
  }

  // terminationReason is the reason Handler.onTerminate receives.
  terminationReason(): TerminationReason {
    if (this.endRead) {
      return { reason: "session_end", deadlineMs: 0 };
    }
    return this.closeReason ?? { reason: "stdin_closed", deadlineMs: 0 };
  }
}

// SessionTable is the routing table from sessionId to the live record,
// plus the bookkeeping for records a session_end removed whose context is
// not yet released.
export class SessionTable {
  // live maps a sessionId to the record of the latest session_start that
  // created one for it.
  private readonly live = new Map<string, SessionRecord>();
  // tail maps a sessionId to its most recent unreleased record, live or
  // removed, which a new record for the same sessionId waits on.
  private readonly tail = new Map<string, SessionRecord>();
  // removed maps a startId to a record a session_end removed from live
  // until the record's context is released.
  private readonly removed = new Map<string, SessionRecord>();
  // ended holds each sessionId whose latest start a session_end ended and
  // that no later session_start reopened. It outlives the release, so a
  // message the adapter had in flight when it wrote the session_end is
  // dropped rather than answered with a RUNTIME_ERROR response a later
  // start of the same session could read as its own. The number of entries
  // is bounded by the pool's per-pod session limit.
  private readonly ended = new Set<string>();

  // lookup returns the live record for sessionId.
  lookup(sessionId: string): SessionRecord | undefined {
    return this.live.get(sessionId);
  }

  // endedSince reports whether a session_end ended sessionId's latest start
  // and no session_start has reopened the session since.
  endedSince(sessionId: string): boolean {
    return this.ended.has(sessionId);
  }

  // open installs a record for start unless the session is already held.
  // held reports a duplicate under the session_start rule 3.
  open(start: SessionStart): { rec: SessionRecord; held: boolean } {
    const cur = this.live.get(start.sessionId);
    if (cur) {
      return { rec: cur, held: true };
    }
    const rec = new SessionRecord(start, this.tail.get(start.sessionId));
    this.live.set(rec.id, rec);
    this.tail.set(rec.id, rec);
    this.ended.delete(rec.id);
    return { rec, held: false };
  }

  // remove takes the live record for sessionId out of the routing table
  // and tracks it under its startId until its release.
  remove(sessionId: string): SessionRecord | undefined {
    const rec = this.live.get(sessionId);
    if (!rec) {
      return undefined;
    }
    this.live.delete(sessionId);
    this.removed.set(rec.startId, rec);
    this.ended.add(sessionId);
    return rec;
  }

  // forget drops a released record from every index that still names it.
  forget(rec: SessionRecord): void {
    if (this.live.get(rec.id) === rec) {
      this.live.delete(rec.id);
    }
    if (this.tail.get(rec.id) === rec) {
      this.tail.delete(rec.id);
    }
    if (this.removed.get(rec.startId) === rec) {
      this.removed.delete(rec.startId);
    }
  }

  // liveRecords returns a snapshot of the live records.
  liveRecords(): SessionRecord[] {
    return [...this.live.values()];
  }
}

// CredentialFileError reports a credential file a frame named that could
// not be read or parsed.
export class CredentialFileError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "CredentialFileError";
  }
}

// loadCredentialBundle reads and parses the credential file at path. An
// empty path loads no bundle: the adapter names the file only when it
// provisioned one. A named file that cannot be read or parsed rejects with
// CredentialFileError, which fails the session's creation.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start credentialsPath),
// §4.7.11 (item 4).
export async function loadCredentialBundle(
  path: string,
): Promise<CredentialBundle | undefined> {
  if (path === "") {
    return undefined;
  }
  let data: string;
  try {
    data = await readFile(path, "utf8");
  } catch (err) {
    throw new CredentialFileError(
      `read credential file ${path}: ${(err as Error).message}`,
    );
  }
  let raw: unknown;
  try {
    raw = JSON.parse(data);
  } catch (err) {
    throw new CredentialFileError(
      `malformed credential file ${path}: ${(err as Error).message}`,
    );
  }
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) {
    throw new CredentialFileError(
      `malformed credential file ${path}: not a JSON object`,
    );
  }
  const providers = (raw as { providers?: unknown }).providers;
  return {
    providers: Array.isArray(providers)
      ? (providers.filter(
          (p) => p !== null && typeof p === "object",
        ) as CredentialBundle["providers"])
      : [],
  };
}

// sessionErrorResponse is the RUNTIME_ERROR response the SDK writes for a
// message it cannot dispatch to a session.
//
// spec: §28.5.3 (CH-MSGSOCK, Session errors).
export function sessionErrorResponse(
  sessionId: string,
  message: string,
): Record<string, unknown> {
  return {
    type: "response",
    output: [],
    error: { code: "RUNTIME_ERROR", message },
    sessionId,
  };
}
