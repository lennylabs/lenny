// SPDX-License-Identifier: MIT

import type { Authenticator } from './auth.js';
import { ApiError, decodeApiError } from './errors.js';
import { delayForAttempt, type RetryPolicy } from './retry.js';

/** The SDK identity sent in the User-Agent request header. */
const streamUserAgent = 'lenny-client-sdk-ts';

/**
 * StreamEvent is one decoded frame from the §15.1
 * `GET /v1/sessions/{id}/events` Server-Sent Events stream.
 */
export interface StreamEvent {
  /**
   * The monotonic per-session sequence the gateway assigns. It is the
   * cursor a reconnect resumes from: the SDK sends the last delivered
   * seq as the Last-Event-ID header. A frame with no `id:` field has
   * seq 0.
   */
  seq: number;

  /**
   * The SSE event type (the `event:` field), for example
   * message_delivered, response, or state_changed. Empty when the
   * frame carried no `event:` field.
   */
  type: string;

  /** The event payload, the JSON text carried in the `data:` field. */
  data: string;
}

/**
 * StreamOptions configures a {@link Client.streamEvents} call.
 */
export interface StreamOptions {
  /**
   * The sequence the stream resumes after. Zero (the default) starts
   * from the beginning of the retained backlog. A non-zero value sends
   * the §15.1 Last-Event-ID header on the initial request so the
   * gateway replays only events after that cursor.
   */
  lastEventId?: number;

  /**
   * An AbortSignal that stops the stream. When it fires, the async
   * iterator returned by streamEvents completes without throwing: an
   * abort is a caller-requested clean stop.
   */
  signal?: AbortSignal;
}

/**
 * The transport surface streamEvents needs. It is the subset of the
 * {@link Client} internals the stream loop reads, passed in so the
 * stream module does not reach into the Client's private fields.
 */
export interface StreamTransport {
  baseUrl: string;
  fetchImpl: typeof fetch;
  retry: RetryPolicy;
  rng: () => number;
  auth?: Authenticator;
  tenantId?: string;
}

/**
 * streamSessionEvents opens the §15.1 GET /v1/sessions/{id}/events
 * Server-Sent Events stream for a session and returns an async
 * iterable of decoded events.
 *
 * On a transport disconnect the iterable reconnects automatically,
 * sending the last delivered sequence as the Last-Event-ID header so
 * the gateway replays the retained backlog from that cursor. Any
 * replayed event whose seq is at or below the last delivered seq is
 * dropped, so the §15.1 reconnect contract holds: the backlog is
 * replayed, no event is skipped, and no event is delivered twice.
 * Reconnect attempts are spaced by the retry policy's backoff.
 *
 * The gateway holds the SSE connection open until the client
 * disconnects; it does not close a live stream on its own. The SDK
 * therefore treats any connection close, including a clean end of
 * body, as an unexpected disconnect and reconnects, the same
 * reconnect behavior the WHATWG EventSource model specifies. A
 * reconnect that delivers an event resets the attempt counter, so a
 * long-lived stream reconnects indefinitely; consecutive reconnects
 * that deliver no event are bounded by the retry policy's maxAttempts.
 *
 * The iterable ends when the signal in `opt` is aborted (a clean
 * caller-requested stop, no error thrown), when the gateway returns a
 * non-retryable HTTP status (the iterator throws the typed
 * {@link ApiError}), or when consecutive zero-progress reconnects
 * exhaust the retry budget. A retryable HTTP status (429, 5xx) on the
 * initial request or on a reconnect is retried like a transport
 * disconnect.
 */
export async function* streamSessionEvents(
  transport: StreamTransport,
  sessionID: string,
  opt: StreamOptions = {},
): AsyncGenerator<StreamEvent, void, void> {
  const path = `/v1/sessions/${encodeURIComponent(sessionID)}/events`;
  const policy = transport.retry;
  let lastSeq = opt.lastEventId && opt.lastEventId > 0 ? opt.lastEventId : 0;
  // failures counts consecutive reconnects that delivered no event. It
  // bounds reconnection against a permanently broken stream and spaces
  // attempts with the retry policy's backoff. A connection that
  // delivers an event resets it to zero.
  let failures = 0;

  for (;;) {
    if (opt.signal?.aborted) {
      return;
    }
    if (failures > 0) {
      try {
        await sleep(delayForAttempt(policy, failures, transport.rng), opt.signal);
      } catch {
        // The signal aborted during the backoff wait. A clean stop.
        return;
      }
    }

    let outcome: ConnectionOutcome;
    try {
      outcome = await openConnection(transport, path, lastSeq, opt.signal);
    } catch (err) {
      if (isAbort(err)) {
        // The caller aborted the request mid-connection. A clean stop.
        return;
      }
      // A transport-level failure before any response (connection
      // refused, DNS, a reset). Reconnect after a backoff.
      failures++;
      if (failures >= policy.maxAttempts) {
        throw err;
      }
      continue;
    }

    if (outcome.kind === 'error') {
      // A non-retryable HTTP status ends the stream with the typed
      // error; a retryable status is reconnected like a disconnect.
      if (!outcome.error.retryable) {
        throw outcome.error;
      }
      failures++;
      if (failures >= policy.maxAttempts) {
        throw outcome.error;
      }
      continue;
    }

    // outcome.kind === 'open': relay every frame the connection
    // delivered. A frame at or below the last delivered cursor is a
    // replayed backlog event and is dropped.
    let progressed = false;
    let connError: unknown;
    try {
      for await (const ev of readFrames(outcome.body, opt.signal)) {
        if (ev.seq !== 0 && ev.seq <= lastSeq) {
          continue;
        }
        yield ev;
        if (ev.seq > lastSeq) {
          lastSeq = ev.seq;
          progressed = true;
        }
      }
    } catch (err) {
      if (isAbort(err)) {
        // The caller aborted while reading the body. A clean stop.
        return;
      }
      // A read error mid-stream. Reconnect from the last delivered
      // cursor.
      connError = err;
    } finally {
      await cancelBody(outcome.body);
    }

    if (opt.signal?.aborted) {
      return;
    }
    // A transport disconnect, a clean end of body, or a read error.
    // Reconnect. A connection that delivered an event restarts the
    // attempt counter; otherwise the counter advances toward the
    // maxAttempts ceiling.
    if (progressed) {
      failures = 0;
      continue;
    }
    failures++;
    if (failures >= policy.maxAttempts) {
      // The stream made no progress across the full retry budget.
      // Surface the last read failure, or return cleanly when the
      // connection kept ending at a clean end of body.
      if (connError !== undefined) {
        throw connError;
      }
      return;
    }
  }
}

/** ConnectionOutcome is the result of opening one SSE connection. */
type ConnectionOutcome =
  | { kind: 'open'; body: ReadableStream<Uint8Array> }
  | { kind: 'error'; error: ApiError };

/**
 * openConnection issues one GET request for the SSE stream. It returns
 * the response body stream on a 2xx, or the typed {@link ApiError} on
 * a non-2xx. A transport-level failure rejects.
 */
async function openConnection(
  transport: StreamTransport,
  path: string,
  afterSeq: number,
  signal: AbortSignal | undefined,
): Promise<ConnectionOutcome> {
  const headers = new Headers();
  headers.set('Accept', 'text/event-stream');
  headers.set('User-Agent', streamUserAgent);
  if (afterSeq > 0) {
    // The §15.1 reconnect cursor. The gateway replays the retained
    // backlog with seq greater than this value before live delivery
    // resumes.
    headers.set('Last-Event-ID', String(afterSeq));
  }
  if (transport.tenantId) {
    headers.set('X-Lenny-Tenant-ID', transport.tenantId);
  }
  if (transport.auth) {
    await transport.auth.apply(headers);
  }

  const resp = await transport.fetchImpl(transport.baseUrl + path, {
    method: 'GET',
    headers,
    signal,
  });
  if (resp.status >= 200 && resp.status < 300) {
    if (!resp.body) {
      // A 2xx with no body is treated as an immediate clean end of
      // body: an empty stream the reconnect loop retries.
      return { kind: 'open', body: emptyStream() };
    }
    return { kind: 'open', body: resp.body };
  }
  const text = await resp.text();
  return { kind: 'error', error: decodeApiError(resp.status, text) };
}

/**
 * readFrames decodes a Server-Sent Events byte stream into
 * {@link StreamEvent} values. It accumulates the id, event, and data
 * fields of one frame and yields a StreamEvent when the blank line
 * that terminates the frame is read. The generator returns when the
 * body ends; a frame buffered without its terminating blank line is
 * discarded.
 */
async function* readFrames(
  body: ReadableStream<Uint8Array>,
  signal: AbortSignal | undefined,
): AsyncGenerator<StreamEvent, void, void> {
  const decoder = new TextDecoder();
  const reader = body.getReader();
  let buffer = '';
  const parser = new FrameParser();
  try {
    for (;;) {
      if (signal?.aborted) {
        throw abortError(signal);
      }
      const { value, done } = await reader.read();
      if (done) {
        // The body ended on a frame boundary. A partial frame still in
        // the parser is discarded; a clean stream ends here.
        return;
      }
      buffer += decoder.decode(value, { stream: true });
      let nl = buffer.indexOf('\n');
      while (nl >= 0) {
        // SSE lines are LF-terminated; tolerate a CRLF stream by
        // trimming a trailing CR.
        let line = buffer.slice(0, nl);
        if (line.endsWith('\r')) {
          line = line.slice(0, -1);
        }
        buffer = buffer.slice(nl + 1);
        const frame = parser.pushLine(line);
        if (frame) {
          yield frame;
        }
        nl = buffer.indexOf('\n');
      }
    }
  } finally {
    reader.releaseLock();
  }
}

/**
 * FrameParser accumulates the lines of one SSE frame and emits a
 * {@link StreamEvent} on the blank line that terminates the frame.
 */
class FrameParser {
  private seq = 0;
  private type = '';
  private data: string[] = [];
  private started = false;

  /**
   * pushLine feeds one SSE line. It returns the completed
   * {@link StreamEvent} when the line is the blank line that
   * terminates a frame, and undefined otherwise.
   */
  pushLine(line: string): StreamEvent | undefined {
    if (line === '') {
      // A blank line terminates the frame. Dispatch it only when a
      // recognized field was seen, so a blank line that follows only
      // comment lines or blank padding is skipped rather than
      // dispatched as an empty frame.
      if (!this.started) {
        return undefined;
      }
      const ev: StreamEvent = {
        seq: this.seq,
        type: this.type,
        data: this.data.join('\n'),
      };
      this.reset();
      return ev;
    }
    const { field, value } = splitLine(line);
    switch (field) {
      case 'id': {
        this.started = true;
        const n = Number.parseInt(value, 10);
        if (Number.isInteger(n) && n >= 0 && String(n) === value) {
          this.seq = n;
        }
        break;
      }
      case 'event':
        this.started = true;
        this.type = value;
        break;
      case 'data':
        this.started = true;
        // The SSE format joins multiple data lines with a newline.
        this.data.push(value);
        break;
      default:
        // A comment line (an empty field name, the line started with a
        // colon) or an unknown field name. The SSE format permits
        // both; neither contributes to the frame.
        break;
    }
    return undefined;
  }

  private reset(): void {
    this.seq = 0;
    this.type = '';
    this.data = [];
    this.started = false;
  }
}

/**
 * splitLine splits one SSE line into its field name and value. The SSE
 * format separates them with the first colon and strips a single
 * leading space from the value. A line with no colon is a field name
 * with an empty value.
 */
function splitLine(line: string): { field: string; value: string } {
  const idx = line.indexOf(':');
  if (idx < 0) {
    return { field: line, value: '' };
  }
  let value = line.slice(idx + 1);
  if (value.startsWith(' ')) {
    value = value.slice(1);
  }
  return { field: line.slice(0, idx), value };
}

/** emptyStream returns a ReadableStream that closes with no data. */
function emptyStream(): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    start(controller): void {
      controller.close();
    },
  });
}

/**
 * cancelBody cancels a response body stream so a dropped connection
 * does not leak the socket. A cancel that rejects is ignored: the
 * connection is already being discarded.
 */
async function cancelBody(body: ReadableStream<Uint8Array>): Promise<void> {
  try {
    await body.cancel();
  } catch {
    // The body is already errored or closed; nothing to release.
  }
}

/**
 * sleep waits ms milliseconds or until signal aborts, whichever is
 * first. It rejects with the abort reason when the signal fires.
 */
function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  if (ms <= 0) {
    return signal?.aborted ? Promise.reject(abortError(signal)) : Promise.resolve();
  }
  return new Promise<void>((resolve, reject) => {
    if (signal?.aborted) {
      reject(abortError(signal));
      return;
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    const onAbort = (): void => {
      clearTimeout(timer);
      reject(abortError(signal!));
    };
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}

/** isAbort reports whether err is an AbortError or a TimeoutError. */
function isAbort(err: unknown): boolean {
  return err instanceof Error && (err.name === 'AbortError' || err.name === 'TimeoutError');
}

/** abortError returns the signal's abort reason, or a generic error. */
function abortError(signal: AbortSignal): unknown {
  return signal.reason ?? new DOMException('The operation was aborted.', 'AbortError');
}
