// SPDX-License-Identifier: MIT

/**
 * Verification of §14 Lenny webhook signatures. A webhook delivery
 * carries an X-Lenny-Signature header of the form
 * `t=<unix_seconds>,v1=<hex_signature>`, where the signature is an
 * HMAC-SHA256 over `<unix_seconds>.<raw_body>`. A receiver verifies
 * the HMAC with the callbackSecret it supplied at session creation and
 * rejects a delivery timestamp outside a five-minute replay window.
 */

import { createHmac, timingSafeEqual } from 'node:crypto';

/** The §14 HTTP header that carries the webhook signature. */
export const signatureHeader = 'X-Lenny-Signature';

/** The §14 maximum delivery-timestamp skew, in seconds. */
export const replayWindowSeconds = 5 * 60;

/**
 * The reason a webhook signature verification failed. A
 * {@link WebhookError} carries one of these values.
 */
export type WebhookErrorReason =
  | 'malformed_signature'
  | 'missing_signature'
  | 'replay_window'
  | 'signature_mismatch';

/**
 * WebhookError is the typed error a {@link Verifier} throws when a
 * signature does not verify.
 */
export class WebhookError extends Error {
  /** The classification of the verification failure. */
  readonly reason: WebhookErrorReason;

  constructor(reason: WebhookErrorReason, message: string) {
    super(message);
    this.name = 'WebhookError';
    this.reason = reason;
    Object.setPrototypeOf(this, WebhookError.prototype);
  }
}

/** A signing secret accepted as input, as a string or raw bytes. */
export type Secret = string | Uint8Array;

/** Options for a {@link Verifier}. */
export interface VerifierOptions {
  /**
   * Overrides the time source used for the replay-window check, as a
   * function returning the current time in Unix seconds. Tests inject
   * a fixed clock; production leaves this unset.
   */
  now?: () => number;
}

function toBuffer(s: Secret): Buffer {
  return typeof s === 'string' ? Buffer.from(s, 'utf8') : Buffer.from(s);
}

/**
 * computeHmac returns the hex-encoded HMAC-SHA256 over the §14 signing
 * input `<unix>.<body>`.
 */
function computeHmac(secret: Buffer, unix: string, body: Buffer): string {
  const mac = createHmac('sha256', secret);
  mac.update(unix, 'utf8');
  mac.update('.', 'utf8');
  mac.update(body);
  return mac.digest('hex');
}

/**
 * parseHeader extracts the t and v1 components of an X-Lenny-Signature
 * header. It throws a {@link WebhookError} when the header does not
 * parse.
 */
function parseHeader(header: string): { unix: string; v1: string } {
  let unix = '';
  let v1 = '';
  for (const part of header.split(',')) {
    const eq = part.indexOf('=');
    if (eq < 0) {
      throw new WebhookError('malformed_signature', 'webhook: X-Lenny-Signature header is malformed');
    }
    const key = part.slice(0, eq).trim();
    const value = part.slice(eq + 1);
    if (key === 't') {
      unix = value;
    } else if (key === 'v1') {
      v1 = value;
    }
  }
  if (unix === '' || v1 === '') {
    throw new WebhookError('malformed_signature', 'webhook: X-Lenny-Signature header is malformed');
  }
  return { unix, v1 };
}

/**
 * Sign produces the §14 X-Lenny-Signature header value for body,
 * signed with secret at delivery time tUnix (Unix seconds). It is the
 * inverse of {@link Verifier.verify} and is provided so tests and
 * gateway-side code can produce a known-good header.
 */
export function sign(secret: Secret, body: string | Uint8Array, tUnix: number): string {
  const unix = String(Math.floor(tUnix));
  const bodyBuf = typeof body === 'string' ? Buffer.from(body, 'utf8') : Buffer.from(body);
  return `t=${unix},v1=${computeHmac(toBuffer(secret), unix, bodyBuf)}`;
}

/**
 * Verifier checks webhook delivery signatures against one or more
 * secrets. Holding more than one secret lets a receiver verify
 * deliveries during a secret-rotation overlap window. Construct it
 * with {@link newVerifier} or {@link verifierWithSecret}.
 */
export class Verifier {
  private readonly secrets: Buffer[];
  private readonly now: () => number;

  constructor(secrets: Secret[], opts: VerifierOptions = {}) {
    if (secrets.length === 0) {
      throw new Error('webhook: at least one signing secret is required');
    }
    this.secrets = secrets.map(toBuffer);
    this.now = opts.now ?? (() => Math.floor(Date.now() / 1000));
  }

  /**
   * verify checks that signatureHeaderValue is a valid §14 signature
   * over body as of the Verifier's clock. signatureHeaderValue is the
   * raw value of the X-Lenny-Signature header. verify returns when the
   * signature is valid and throws a {@link WebhookError} otherwise:
   * `malformed_signature` when the header does not parse,
   * `replay_window` when the timestamp is stale, and
   * `signature_mismatch` when the HMAC matches no configured secret.
   * The HMAC comparison is constant-time.
   */
  verify(body: string | Uint8Array, signatureHeaderValue: string): void {
    if (signatureHeaderValue === '') {
      throw new WebhookError(
        'missing_signature',
        'webhook: request is missing the X-Lenny-Signature header',
      );
    }
    const { unix, v1 } = parseHeader(signatureHeaderValue);
    const ts = Number(unix);
    if (!Number.isInteger(ts)) {
      throw new WebhookError('malformed_signature', 'webhook: X-Lenny-Signature header is malformed');
    }
    let skew = this.now() - ts;
    if (skew < 0) {
      skew = -skew;
    }
    if (skew > replayWindowSeconds) {
      throw new WebhookError(
        'replay_window',
        'webhook: signature timestamp is outside the replay window',
      );
    }
    const bodyBuf = typeof body === 'string' ? Buffer.from(body, 'utf8') : Buffer.from(body);
    const v1Buf = Buffer.from(v1, 'utf8');
    for (const secret of this.secrets) {
      const expected = Buffer.from(computeHmac(secret, unix, bodyBuf), 'utf8');
      if (expected.length === v1Buf.length && timingSafeEqual(expected, v1Buf)) {
        return;
      }
    }
    throw new WebhookError('signature_mismatch', 'webhook: signature does not match');
  }
}

/**
 * newVerifier returns a {@link Verifier} that accepts a delivery
 * signed with any of the supplied secrets. Pass the callbackSecret
 * supplied at session creation; pass both the old and new secret
 * during a rotation overlap. It throws when no secret is supplied.
 */
export function newVerifier(secrets: Secret[], opts: VerifierOptions = {}): Verifier {
  return new Verifier(secrets, opts);
}

/** verifierWithSecret is the single-secret convenience constructor. */
export function verifierWithSecret(secret: Secret, opts: VerifierOptions = {}): Verifier {
  return new Verifier([secret], opts);
}
