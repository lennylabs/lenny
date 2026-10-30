// SPDX-License-Identifier: MIT

/**
 * The §15.1 error category. Every gateway error envelope carries one
 * of these four values.
 */
export type ErrorCategory = 'TRANSIENT' | 'PERMANENT' | 'POLICY' | 'UPSTREAM';

/**
 * The §15.1 error envelope `{code, category, message, retryable,
 * docs_url, details}`. The gateway returns this body on every non-2xx
 * REST response.
 */
export interface ErrorEnvelope {
  code: string;
  category?: ErrorCategory;
  message: string;
  retryable?: boolean;
  docs_url?: string;
  details?: Record<string, unknown>;
}

/**
 * Constructor fields for an {@link ApiError}.
 */
export interface ApiErrorInit {
  code: string;
  category?: ErrorCategory;
  message: string;
  retryable: boolean;
  docsUrl?: string;
  details?: Record<string, unknown>;
  httpStatus: number;
}

/**
 * ApiError is the typed form of the §15.1 error envelope. The gateway
 * returns this body on every non-2xx REST response. ApiError extends
 * the built-in Error, so a caller may either `instanceof`-check it or
 * read the structured fields.
 */
export class ApiError extends Error {
  /**
   * The machine-readable §15.1 error code (for example
   * VALIDATION_ERROR, RESOURCE_NOT_FOUND, RATE_LIMITED).
   */
  readonly code: string;

  /** The §15.1 error category, when the gateway supplies one. */
  readonly category?: ErrorCategory;

  /**
   * Whether the client should retry the request. The {@link Client}
   * retries an error only when this field is true.
   */
  readonly retryable: boolean;

  /**
   * The documentation URL for this error code, when the gateway
   * supplies one. The §15.1 envelope spells the field `docs_url`.
   */
  readonly docsUrl?: string;

  /**
   * Error-specific context. The structure varies by code;
   * INVALID_STATE_TRANSITION, for example, carries currentState and
   * allowedStates.
   */
  readonly details?: Record<string, unknown>;

  /**
   * The HTTP status code the gateway returned alongside the envelope.
   * It is populated by the {@link Client} and is not part of the wire
   * envelope.
   */
  readonly httpStatus: number;

  constructor(init: ApiErrorInit) {
    const summary = init.code
      ? `lenny: ${init.code}${init.category ? ` (${init.category})` : ''}: ${init.message}`
      : `lenny: gateway returned HTTP ${init.httpStatus} with no error envelope`;
    super(summary);
    this.name = 'ApiError';
    this.code = init.code;
    this.category = init.category;
    this.retryable = init.retryable;
    this.docsUrl = init.docsUrl;
    this.details = init.details;
    this.httpStatus = init.httpStatus;
    // Restore the prototype chain so `instanceof ApiError` holds after
    // transpilation to ES5-class semantics.
    Object.setPrototypeOf(this, ApiError.prototype);
  }
}

/**
 * The HTTP statuses that warrant a retry when the response carried no
 * parseable §15.1 error envelope. The retryable categories map onto
 * 429, 500, 502, 503, and 504.
 */
const retryableStatuses = new Set([429, 500, 502, 503, 504]);

/**
 * retryableStatus reports whether an HTTP status warrants a retry when
 * the response carried no parseable error envelope.
 */
export function retryableStatus(status: number): boolean {
  return retryableStatuses.has(status);
}

/**
 * isRetryable reports whether err is a retryable {@link ApiError}. A
 * non-ApiError (a transport failure, an abort) is not retryable
 * through this predicate; the {@link Client} retry loop treats
 * transport failures separately.
 */
export function isRetryable(err: unknown): boolean {
  return err instanceof ApiError && err.retryable;
}

/**
 * asApiError narrows err to an {@link ApiError}, returning the error
 * when err is an ApiError and undefined otherwise.
 */
export function asApiError(err: unknown): ApiError | undefined {
  return err instanceof ApiError ? err : undefined;
}

/**
 * decodeApiError parses a non-2xx response body into an
 * {@link ApiError}. When the body does not carry a §15.1 envelope, the
 * error code is left empty and retryable is inferred from the HTTP
 * status.
 */
export function decodeApiError(status: number, body: string): ApiError {
  let envelope: { error?: ErrorEnvelope } | undefined;
  try {
    envelope = JSON.parse(body) as { error?: ErrorEnvelope };
  } catch {
    envelope = undefined;
  }
  const e = envelope?.error;
  if (e && e.code) {
    return new ApiError({
      code: e.code,
      category: e.category,
      message: e.message ?? '',
      retryable: e.retryable ?? false,
      docsUrl: e.docs_url,
      details: e.details,
      httpStatus: status,
    });
  }
  return new ApiError({
    code: '',
    message: body.trim(),
    retryable: retryableStatus(status),
    httpStatus: status,
  });
}
