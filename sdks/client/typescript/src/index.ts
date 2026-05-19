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
 * The {@link Verifier} verifies §14 webhook delivery signatures with
 * the same HMAC-SHA256 scheme the gateway signs them.
 *
 * The streaming and MCP-client surfaces named in §15.6 are not yet
 * implemented in this SDK.
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

export type {
  CreateSessionRequest,
  CreateSessionResult,
  IsolationLevel,
  ListOptions,
  Session,
  SessionPage,
  State,
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
