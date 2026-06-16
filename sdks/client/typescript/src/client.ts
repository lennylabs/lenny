// SPDX-License-Identifier: MIT

import type { Authenticator } from './auth.js';
import { decodeApiError } from './errors.js';
import { MCPClient } from './mcp.js';
import {
  defaultRetryPolicy,
  delayForAttempt,
  normalizeRetryPolicy,
  type RetryPolicy,
} from './retry.js';
import {
  streamSessionEvents,
  type StreamEvent,
  type StreamOptions,
} from './stream.js';
import type {
  CreateSessionRequest,
  CreateSessionResult,
  InteractionResolution,
  ListOptions,
  SendMessagesRequest,
  SendMessagesResponse,
  Session,
  SessionPage,
  TranscriptOptions,
  TranscriptResponse,
} from './types.js';

/** The SDK identity sent in the User-Agent request header. */
const userAgent = 'lenny-client-sdk-ts';

/** The default per-request timeout, in milliseconds. */
const defaultTimeoutMs = 30_000;

/**
 * Options for constructing a {@link Client}.
 */
export interface ClientOptions {
  /**
   * The authentication credential the client attaches to every
   * request. See {@link bearerToken} and {@link apiKey}.
   */
  auth?: Authenticator;

  /**
   * The development X-Lenny-Tenant-ID header. The minimal gateway
   * honors this header to scope requests to a tenant when no OIDC
   * principal is present. Production deployments derive the tenant
   * from the authenticated principal and ignore the header.
   */
  tenantId?: string;

  /**
   * Overrides {@link defaultRetryPolicy}. Unset fields of the supplied
   * policy are filled from {@link defaultRetryPolicy}.
   */
  retryPolicy?: Partial<RetryPolicy>;

  /**
   * The per-request timeout, in milliseconds. A request that exceeds
   * it is aborted. Defaults to 30000.
   */
  timeoutMs?: number;

  /**
   * The fetch implementation. Defaults to the global `fetch`. Tests
   * inject a stub; production leaves this unset on a runtime that
   * provides a global fetch.
   */
  fetch?: typeof fetch;

  /**
   * The jitter randomness source, a function returning a value in
   * [0, 1). Defaults to Math.random. Tests inject a deterministic
   * source.
   */
  rng?: () => number;
}

/**
 * Per-call options threaded through a single request.
 */
export interface RequestOptions {
  /**
   * Attaches an Idempotency-Key header to the request. The §11.5
   * gateway middleware replays the cached response when the same key
   * is presented again with the same body within the key's TTL. Use it
   * on createSession so a retried create does not produce a second
   * session.
   */
  idempotencyKey?: string;

  /**
   * An AbortSignal that aborts the request. The SDK aborts the request
   * when this signal fires or when the per-request timeout elapses,
   * whichever is first.
   */
  signal?: AbortSignal;
}

/**
 * Client is the §15.1 REST gateway client. Construct it with
 * {@link newClient}.
 */
export class Client {
  private readonly baseUrl: string;
  private readonly auth?: Authenticator;
  private readonly tenantId?: string;
  private readonly retry: RetryPolicy;
  private readonly timeoutMs: number;
  private readonly fetchImpl: typeof fetch;
  private readonly rng: () => number;

  constructor(baseUrl: string, opts: ClientOptions = {}) {
    if (!baseUrl) {
      throw new Error('lenny: base URL is required');
    }
    let parsed: URL;
    try {
      parsed = new URL(baseUrl);
    } catch {
      throw new Error(`lenny: base URL ${JSON.stringify(baseUrl)} is invalid`);
    }
    if (!parsed.protocol || !parsed.host) {
      throw new Error(`lenny: base URL ${JSON.stringify(baseUrl)} must be absolute`);
    }
    const fetchImpl = opts.fetch ?? globalThis.fetch;
    if (typeof fetchImpl !== 'function') {
      throw new Error('lenny: no fetch implementation; pass ClientOptions.fetch');
    }
    this.baseUrl = baseUrl.replace(/\/+$/, '');
    this.auth = opts.auth;
    this.tenantId = opts.tenantId;
    this.retry = opts.retryPolicy
      ? normalizeRetryPolicy(opts.retryPolicy)
      : { ...defaultRetryPolicy };
    this.timeoutMs = opts.timeoutMs && opts.timeoutMs > 0 ? opts.timeoutMs : defaultTimeoutMs;
    this.fetchImpl = fetchImpl;
    this.rng = opts.rng ?? Math.random;
  }

  /**
   * createSession calls POST /v1/sessions. The request is retried on a
   * retryable error per the client's retry policy. Pass an
   * idempotencyKey so a retry is collapsed by the gateway's
   * idempotency middleware rather than producing a duplicate session.
   */
  async createSession(
    req: CreateSessionRequest,
    opts: RequestOptions = {},
  ): Promise<CreateSessionResult> {
    return this.do<CreateSessionResult>('POST', '/v1/sessions', req, opts);
  }

  /** getSession calls GET /v1/sessions/{id}. */
  async getSession(id: string, opts: RequestOptions = {}): Promise<Session> {
    return this.do<Session>('GET', `/v1/sessions/${encodeURIComponent(id)}`, undefined, opts);
  }

  /**
   * listSessions calls GET /v1/sessions, applying the filters in opt.
   * It returns one page; iterate with the returned nextCursor or use
   * {@link iterateSessions} for an all-pages helper.
   */
  async listSessions(opt: ListOptions = {}, opts: RequestOptions = {}): Promise<SessionPage> {
    const q = new URLSearchParams();
    if (opt.state) {
      q.set('state', opt.state);
    }
    if (opt.runtime) {
      q.set('runtime', opt.runtime);
    }
    if (opt.cursor) {
      q.set('cursor', opt.cursor);
    }
    if (opt.limit !== undefined && opt.limit > 0) {
      q.set('limit', String(opt.limit));
    }
    const encoded = q.toString();
    const path = encoded ? `/v1/sessions?${encoded}` : '/v1/sessions';
    // §15.1 lines 1228-1253 canonical cursor-paginated envelope:
    // {items, cursor, hasMore, total?}. The list helper surfaces items
    // as page.sessions and cursor as page.nextCursor so the SDK's
    // pagination surface stays stable regardless of the wire field
    // names. This matches the Go SDK's ListSessions decode.
    const raw = await this.do<{
      items?: Session[];
      cursor?: string;
      hasMore?: boolean;
    }>('GET', path, undefined, opts);
    return {
      sessions: raw.items ?? [],
      nextCursor: raw.cursor ?? '',
      hasMore: raw.hasMore ?? false,
    };
  }

  /**
   * iterateSessions walks every page of a GET /v1/sessions listing,
   * yielding one session at a time. It is the cursor-iteration helper
   * for the §15.6 pagination-cursors contract. Iteration stops when
   * the listing is exhausted; a page error rejects the iterator.
   */
  async *iterateSessions(
    opt: ListOptions = {},
    opts: RequestOptions = {},
  ): AsyncGenerator<Session, void, void> {
    let cursor = opt.cursor;
    for (;;) {
      const page = await this.listSessions({ ...opt, cursor }, opts);
      for (const s of page.sessions) {
        yield s;
      }
      if (!page.hasMore || !page.nextCursor) {
        return;
      }
      cursor = page.nextCursor;
    }
  }

  /**
   * streamEvents opens the §15.1 GET /v1/sessions/{id}/events
   * Server-Sent Events stream for a session and returns an async
   * iterable of decoded events. Consume it with `for await`.
   *
   * On a transport disconnect the iterable reconnects automatically,
   * sending the last delivered sequence as the Last-Event-ID header so
   * the gateway replays the retained backlog from that cursor. A
   * replayed event at or below the last delivered cursor is dropped,
   * so the §15.1 reconnect contract holds with no gap and no
   * duplicate. Reconnect attempts are spaced by the client's retry
   * policy backoff.
   *
   * The iterable ends when the AbortSignal in opt is aborted (a clean
   * caller-requested stop), when the gateway returns a non-retryable
   * HTTP status (the iterator throws the typed {@link ApiError}), or
   * when consecutive zero-progress reconnects exhaust the retry
   * budget. A retryable HTTP status (429, 5xx) is retried like a
   * transport disconnect.
   */
  streamEvents(id: string, opt: StreamOptions = {}): AsyncGenerator<StreamEvent, void, void> {
    return streamSessionEvents(
      {
        baseUrl: this.baseUrl,
        fetchImpl: this.fetchImpl,
        retry: this.retry,
        rng: this.rng,
        auth: this.auth,
        tenantId: this.tenantId,
      },
      id,
      opt,
    );
  }

  /**
   * mcp returns an {@link MCPClient} that drives the §15.2 gateway MCP
   * API over JSON-RPC 2.0. The MCP client targets the gateway's /mcp
   * endpoint and reuses this client's base URL, fetch implementation,
   * authentication credential, and development tenant header.
   */
  mcp(): MCPClient {
    return new MCPClient({
      baseUrl: this.baseUrl,
      fetchImpl: this.fetchImpl,
      auth: this.auth,
      tenantId: this.tenantId,
    });
  }

  /**
   * deleteSession calls DELETE /v1/sessions/{id}. The §15.1 contract
   * moves a non-terminal session to the cancelled state and returns
   * the updated envelope.
   */
  async deleteSession(id: string, opts: RequestOptions = {}): Promise<Session> {
    return this.transition('DELETE', id, '', opts);
  }

  /**
   * finalize calls POST /v1/sessions/{id}/finalize, transitioning a
   * created session to ready.
   */
  async finalize(id: string, opts: RequestOptions = {}): Promise<Session> {
    return this.transition('POST', id, 'finalize', opts);
  }

  /**
   * start calls POST /v1/sessions/{id}/start, transitioning a ready
   * session to running.
   */
  async start(id: string, opts: RequestOptions = {}): Promise<Session> {
    return this.transition('POST', id, 'start', opts);
  }

  /**
   * interrupt calls POST /v1/sessions/{id}/interrupt, transitioning a
   * running session to suspended.
   */
  async interrupt(id: string, opts: RequestOptions = {}): Promise<Session> {
    return this.transition('POST', id, 'interrupt', opts);
  }

  /**
   * terminate calls POST /v1/sessions/{id}/terminate, transitioning a
   * non-terminal session to completed.
   */
  async terminate(id: string, opts: RequestOptions = {}): Promise<Session> {
    return this.transition('POST', id, 'terminate', opts);
  }

  /**
   * resume calls POST /v1/sessions/{id}/resume, transitioning a
   * session awaiting client action back to running.
   */
  async resume(id: string, opts: RequestOptions = {}): Promise<Session> {
    return this.transition('POST', id, 'resume', opts);
  }

  /**
   * sendMessages calls POST /v1/sessions/{id}/messages with the
   * supplied batch and returns the §15.4 delivery receipt plus the
   * executor's synchronous output. Each payload may carry `inReplyTo`,
   * `delivery`, and `slotId`; see {@link MessagePayload}.
   *
   * spec: §15.1 messages endpoint; §15.4 lines 1725-1737
   * delivery_receipt; §7.2 line 345.
   */
  async sendMessages(
    id: string,
    req: SendMessagesRequest,
    opts: RequestOptions = {},
  ): Promise<SendMessagesResponse> {
    if (!req.messages || req.messages.length === 0) {
      throw new Error('lenny: sendMessages requires at least one message');
    }
    return this.do<SendMessagesResponse>(
      'POST',
      `/v1/sessions/${encodeURIComponent(id)}/messages`,
      req,
      opts,
    );
  }

  /**
   * getTranscript calls GET /v1/sessions/{id}/transcript with optional
   * afterSeq / limit filters. spec: §15.1.
   */
  async getTranscript(
    id: string,
    opt: TranscriptOptions = {},
    opts: RequestOptions = {},
  ): Promise<TranscriptResponse> {
    const q = new URLSearchParams();
    if (opt.afterSeq !== undefined && opt.afterSeq > 0) {
      q.set('afterSeq', String(opt.afterSeq));
    }
    if (opt.limit !== undefined && opt.limit > 0) {
      q.set('limit', String(opt.limit));
    }
    const encoded = q.toString();
    let path = `/v1/sessions/${encodeURIComponent(id)}/transcript`;
    if (encoded) {
      path += `?${encoded}`;
    }
    return this.do<TranscriptResponse>('GET', path, undefined, opts);
  }

  /**
   * approveToolUse calls
   * POST /v1/sessions/{id}/tool-use/{toolCallId}/approve to resolve a
   * pending tool-use interaction the agent is blocked on.
   *
   * spec: §7.2 table line 124; §15.1.
   */
  async approveToolUse(
    id: string,
    toolCallId: string,
    opts: RequestOptions = {},
  ): Promise<InteractionResolution> {
    return this.do<InteractionResolution>(
      'POST',
      `/v1/sessions/${encodeURIComponent(id)}/tool-use/${encodeURIComponent(toolCallId)}/approve`,
      {},
      opts,
    );
  }

  /**
   * denyToolUse calls
   * POST /v1/sessions/{id}/tool-use/{toolCallId}/deny with an optional
   * human-readable reason recorded in the audit row.
   *
   * spec: §7.2 table line 125; §15.1.
   */
  async denyToolUse(
    id: string,
    toolCallId: string,
    reason = '',
    opts: RequestOptions = {},
  ): Promise<InteractionResolution> {
    const body = reason ? { reason } : {};
    return this.do<InteractionResolution>(
      'POST',
      `/v1/sessions/${encodeURIComponent(id)}/tool-use/${encodeURIComponent(toolCallId)}/deny`,
      body,
      opts,
    );
  }

  /**
   * respondElicitation calls
   * POST /v1/sessions/{id}/elicitations/{elicitationId}/respond with
   * the supplied response value. The runtime receives the value and
   * unblocks the pending `lenny/request_elicitation` call.
   *
   * spec: §7.2 table line 126; §9.2; §15.1.
   */
  async respondElicitation(
    id: string,
    elicitationId: string,
    response: unknown,
    opts: RequestOptions = {},
  ): Promise<InteractionResolution> {
    return this.do<InteractionResolution>(
      'POST',
      `/v1/sessions/${encodeURIComponent(id)}/elicitations/${encodeURIComponent(elicitationId)}/respond`,
      { response },
      opts,
    );
  }

  /**
   * dismissElicitation calls
   * POST /v1/sessions/{id}/elicitations/{elicitationId}/dismiss to
   * cancel a pending elicitation request.
   *
   * spec: §7.2 table line 127; §9.2; §15.1.
   */
  async dismissElicitation(
    id: string,
    elicitationId: string,
    opts: RequestOptions = {},
  ): Promise<InteractionResolution> {
    return this.do<InteractionResolution>(
      'POST',
      `/v1/sessions/${encodeURIComponent(id)}/elicitations/${encodeURIComponent(elicitationId)}/dismiss`,
      {},
      opts,
    );
  }

  /**
   * transition issues a no-body state-mutating call. action is the
   * trailing path segment (finalize, start, ...); an empty action
   * targets the bare /v1/sessions/{id} path for DELETE.
   */
  private async transition(
    method: string,
    id: string,
    action: string,
    opts: RequestOptions,
  ): Promise<Session> {
    let path = `/v1/sessions/${encodeURIComponent(id)}`;
    if (action) {
      path += `/${action}`;
    }
    return this.do<Session>(method, path, undefined, opts);
  }

  /**
   * do executes one REST call with retry. It serializes body to JSON
   * (when defined), sends the request, retries retryable failures per
   * the client's retry policy, and parses a 2xx response. A non-2xx
   * response is parsed into and thrown as an {@link ApiError}.
   */
  private async do<T>(
    method: string,
    path: string,
    body: unknown,
    opts: RequestOptions,
  ): Promise<T> {
    const bodyText = body !== undefined ? JSON.stringify(body) : undefined;
    const policy = this.retry;
    let lastErr: unknown;
    for (let attempt = 1; attempt <= policy.maxAttempts; attempt++) {
      if (attempt > 1) {
        await this.sleep(delayForAttempt(policy, attempt - 1, this.rng), opts.signal);
      }
      let status: number;
      let respBody: string;
      try {
        ({ status, body: respBody } = await this.roundTrip(method, path, bodyText, opts));
      } catch (err) {
        // An abort is a caller-driven cancellation; surface it rather
        // than retrying. Any other transport-level failure (connection
        // refused, DNS) is retried like a TRANSIENT error.
        if (isAbortError(err)) {
          throw err;
        }
        lastErr = err;
        continue;
      }
      if (status >= 200 && status < 300) {
        if (!respBody) {
          return undefined as T;
        }
        return JSON.parse(respBody) as T;
      }
      const apiErr = decodeApiError(status, respBody);
      lastErr = apiErr;
      if (apiErr.retryable && attempt < policy.maxAttempts) {
        continue;
      }
      throw apiErr;
    }
    throw lastErr;
  }

  /**
   * roundTrip performs a single HTTP request and returns the status
   * code and response body text. It does not interpret the body.
   */
  private async roundTrip(
    method: string,
    path: string,
    bodyText: string | undefined,
    opts: RequestOptions,
  ): Promise<{ status: number; body: string }> {
    const headers = new Headers();
    headers.set('Accept', 'application/json');
    headers.set('User-Agent', userAgent);
    if (bodyText !== undefined) {
      headers.set('Content-Type', 'application/json');
    }
    if (opts.idempotencyKey) {
      headers.set('Idempotency-Key', opts.idempotencyKey);
    }
    if (this.tenantId) {
      headers.set('X-Lenny-Tenant-ID', this.tenantId);
    }
    if (this.auth) {
      await this.auth.apply(headers);
    }

    const timeout = new AbortController();
    const timer = setTimeout(() => timeout.abort(), this.timeoutMs);
    const signal = mergeSignals(opts.signal, timeout.signal);
    try {
      const resp = await this.fetchImpl(this.baseUrl + path, {
        method,
        headers,
        body: bodyText,
        signal,
      });
      const text = await resp.text();
      return { status: resp.status, body: text };
    } finally {
      clearTimeout(timer);
    }
  }

  /**
   * sleep waits ms milliseconds or until signal aborts, whichever is
   * first. It rejects with the abort reason when the signal fires.
   */
  private sleep(ms: number, signal?: AbortSignal): Promise<void> {
    if (ms <= 0) {
      if (signal?.aborted) {
        return Promise.reject(abortReason(signal));
      }
      return Promise.resolve();
    }
    return new Promise<void>((resolve, reject) => {
      if (signal?.aborted) {
        reject(abortReason(signal));
        return;
      }
      const timer = setTimeout(() => {
        signal?.removeEventListener('abort', onAbort);
        resolve();
      }, ms);
      const onAbort = (): void => {
        clearTimeout(timer);
        reject(abortReason(signal!));
      };
      signal?.addEventListener('abort', onAbort, { once: true });
    });
  }
}

/**
 * newClient returns a {@link Client} bound to baseUrl. baseUrl is the
 * gateway origin (for example https://gateway.acme.com); the /v1 path
 * prefix is appended by the SDK. It throws when baseUrl is empty or
 * does not parse as an absolute URL.
 */
export function newClient(baseUrl: string, opts: ClientOptions = {}): Client {
  return new Client(baseUrl, opts);
}

/** isAbortError reports whether err is an AbortError. */
function isAbortError(err: unknown): boolean {
  return (
    err instanceof Error && (err.name === 'AbortError' || err.name === 'TimeoutError')
  );
}

/** abortReason returns the signal's abort reason, or a generic error. */
function abortReason(signal: AbortSignal): unknown {
  return signal.reason ?? new DOMException('The operation was aborted.', 'AbortError');
}

/**
 * mergeSignals returns a single AbortSignal that fires when either
 * input fires. When the runtime provides AbortSignal.any it is used;
 * otherwise a manual relay is wired up.
 */
function mergeSignals(a: AbortSignal | undefined, b: AbortSignal): AbortSignal {
  if (!a) {
    return b;
  }
  const anyFn = (AbortSignal as { any?: (signals: AbortSignal[]) => AbortSignal }).any;
  if (typeof anyFn === 'function') {
    return anyFn([a, b]);
  }
  const merged = new AbortController();
  const relay = (src: AbortSignal): void => {
    if (!merged.signal.aborted) {
      merged.abort(src.reason);
    }
  };
  if (a.aborted) {
    relay(a);
  } else {
    a.addEventListener('abort', () => relay(a), { once: true });
  }
  if (b.aborted) {
    relay(b);
  } else {
    b.addEventListener('abort', () => relay(b), { once: true });
  }
  return merged.signal;
}
