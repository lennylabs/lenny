// SPDX-License-Identifier: MIT

/**
 * RetryPolicy governs how the {@link Client} retries retryable errors.
 * The {@link Client} fills unset fields from {@link defaultRetryPolicy}.
 */
export interface RetryPolicy {
  /**
   * The total number of attempts, including the first. A value of 1
   * disables retries. Values below 1 are clamped to 1.
   */
  maxAttempts: number;

  /**
   * The delay before the first retry, in milliseconds. Each subsequent
   * retry doubles the delay up to maxDelayMs.
   */
  baseDelayMs: number;

  /** The cap on the per-attempt delay after exponential growth. */
  maxDelayMs: number;

  /**
   * When true, multiplies each computed delay by a random factor in
   * [0.5, 1.0). Jitter spreads retries from many clients so a
   * recovering gateway is not hit by a synchronized burst.
   */
  jitter: boolean;
}

/**
 * The retry policy a {@link Client} uses when the caller supplies
 * none. It retries up to three times with exponential backoff and
 * jitter.
 */
export const defaultRetryPolicy: RetryPolicy = {
  maxAttempts: 3,
  baseDelayMs: 200,
  maxDelayMs: 5000,
  jitter: true,
};

/**
 * normalizeRetryPolicy returns the policy with unset fields filled
 * from {@link defaultRetryPolicy} and maxAttempts clamped to at least
 * 1.
 */
export function normalizeRetryPolicy(p: Partial<RetryPolicy>): RetryPolicy {
  const out: RetryPolicy = {
    maxAttempts:
      p.maxAttempts !== undefined && p.maxAttempts >= 1
        ? p.maxAttempts
        : defaultRetryPolicy.maxAttempts,
    baseDelayMs:
      p.baseDelayMs !== undefined && p.baseDelayMs > 0
        ? p.baseDelayMs
        : defaultRetryPolicy.baseDelayMs,
    maxDelayMs:
      p.maxDelayMs !== undefined && p.maxDelayMs > 0
        ? p.maxDelayMs
        : defaultRetryPolicy.maxDelayMs,
    jitter: p.jitter ?? defaultRetryPolicy.jitter,
  };
  return out;
}

/**
 * delayForAttempt returns the wait, in milliseconds, before retry
 * number `attempt` (attempt is 1-indexed: the wait before the first
 * retry is attempt 1). The delay grows as baseDelayMs * 2^(attempt-1),
 * capped at maxDelayMs, with optional jitter. rng supplies the jitter
 * randomness in [0, 1); it defaults to Math.random.
 */
export function delayForAttempt(
  p: RetryPolicy,
  attempt: number,
  rng: () => number = Math.random,
): number {
  if (attempt < 1) {
    attempt = 1;
  }
  let delay = p.baseDelayMs;
  for (let i = 1; i < attempt; i++) {
    delay *= 2;
    if (delay >= p.maxDelayMs) {
      delay = p.maxDelayMs;
      break;
    }
  }
  if (delay > p.maxDelayMs) {
    delay = p.maxDelayMs;
  }
  if (p.jitter) {
    const factor = 0.5 + rng() * 0.5;
    delay = delay * factor;
  }
  return delay;
}
