// SPDX-License-Identifier: MIT

/**
 * The official TypeScript/JavaScript client SDK for the Lenny
 * gateway. It wraps the §15.1 REST session API and the §15.6 SDK
 * contract surface so application developers do not re-implement the
 * wire protocol from the spec.
 *
 * A {@link Client} is constructed with a gateway base URL and an
 * authentication credential. Its methods cover the session lifecycle
 * (createSession, getSession, listSessions, deleteSession, finalize,
 * start, interrupt, terminate, resume), parse the §15.1 error
 * envelope into the typed {@link ApiError}, support per-request
 * idempotency keys, and retry retryable errors with exponential
 * backoff and jitter.
 *
 * {@link Client.streamEvents} consumes the §15.1
 * GET /v1/sessions/{id}/events Server-Sent Events stream as an async
 * iterable, reconnecting with the Last-Event-ID cursor on a
 * disconnect.
 *
 * {@link Client.mcp} returns an {@link MCPClient} that drives the §15.2
 * gateway MCP API over JSON-RPC 2.0: the initialize handshake,
 * tools/list tool discovery, and tools/call invocation of the platform
 * tools (lenny/create_session, lenny/send_message, and the others).
 *
 * The {@link Verifier} verifies §14 webhook delivery signatures with
 * the same HMAC-SHA256 scheme the gateway signs them.
 *
 * @packageDocumentation
 */

export { Client, newClient } from './client.js';
export type { ClientOptions, RequestOptions } from './client.js';

export {
  bearerToken,
  apiKey,
  refreshingToken,
} from './auth.js';
export type { Authenticator, TokenSource } from './auth.js';

export {
  ApiError,
  asApiError,
  decodeApiError,
  isRetryable,
  retryableStatus,
} from './errors.js';
export type { ApiErrorInit, ErrorCategory, ErrorEnvelope } from './errors.js';

export {
  defaultRetryPolicy,
  delayForAttempt,
  normalizeRetryPolicy,
} from './retry.js';
export type { RetryPolicy } from './retry.js';

export { streamSessionEvents } from './stream.js';
export type { StreamEvent, StreamOptions, StreamTransport } from './stream.js';

export { MCPClient, MCPError, asMCPError } from './mcp.js';
export type {
  InitializeResult,
  MCPContent,
  MCPCreateSessionResult,
  MCPServerInfo,
  MCPTool,
  MCPToolResult,
  MCPTransport,
} from './mcp.js';

export type {
  CreateSessionRequest,
  CreateSessionResult,
  DeliveryMode,
  DeliveryReceipt,
  InteractionResolution,
  IsolationLevel,
  ListOptions,
  MessagePayload,
  MessagePart,
  SendMessagesRequest,
  SendMessagesResponse,
  Session,
  SessionPage,
  State,
  TranscriptEntry,
  TranscriptOptions,
  TranscriptResponse,
} from './types.js';

export {
  Verifier,
  WebhookError,
  newVerifier,
  replayWindowSeconds,
  sign,
  signatureHeader,
  verifierWithSecret,
} from './webhook.js';
export type { Secret, VerifierOptions, WebhookErrorReason } from './webhook.js';
