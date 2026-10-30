# SPDX-License-Identifier: MIT

"""Retry policy for the Lenny client SDK.

A :class:`RetryPolicy` governs how the client retries retryable errors.
The client fills unset fields from :data:`DEFAULT_RETRY_POLICY`.
"""

from __future__ import annotations

import random
from dataclasses import dataclass
from typing import Optional


@dataclass
class RetryPolicy:
    """Governs how the client retries retryable errors.

    Attributes:
        max_attempts: Total number of attempts, including the first. A
            value of 1 disables retries. Values below 1 are clamped.
        base_delay: Delay in seconds before the first retry. Each
            subsequent retry doubles the delay up to ``max_delay``.
        max_delay: Cap in seconds on the per-attempt delay after
            exponential growth.
        jitter: When true, multiplies each computed delay by a random
            factor in ``[0.5, 1.0)``. Jitter spreads retries from many
            clients so a recovering gateway is not hit by a
            synchronized burst.
    """

    max_attempts: int = 3
    base_delay: float = 0.2
    max_delay: float = 5.0
    jitter: bool = True

    def normalized(self) -> "RetryPolicy":
        """Return the policy with unset fields filled from defaults."""
        max_attempts = self.max_attempts
        if max_attempts < 1:
            max_attempts = 1
        base_delay = self.base_delay if self.base_delay > 0 else DEFAULT_RETRY_POLICY.base_delay
        max_delay = self.max_delay if self.max_delay > 0 else DEFAULT_RETRY_POLICY.max_delay
        return RetryPolicy(
            max_attempts=max_attempts,
            base_delay=base_delay,
            max_delay=max_delay,
            jitter=self.jitter,
        )

    def delay_for_attempt(
        self, attempt: int, rng: Optional[random.Random] = None
    ) -> float:
        """Return the wait in seconds before retry number ``attempt``.

        ``attempt`` is 1-indexed: the wait before the first retry is
        attempt 1. The delay grows as ``base_delay * 2 ** (attempt - 1)``,
        capped at ``max_delay``, with optional jitter.
        """
        if attempt < 1:
            attempt = 1
        delay = self.base_delay
        for _ in range(1, attempt):
            delay *= 2
            if delay >= self.max_delay:
                delay = self.max_delay
                break
        if delay > self.max_delay:
            delay = self.max_delay
        if self.jitter:
            factor = rng.random() if rng is not None else random.random()
            delay = delay * (0.5 + factor * 0.5)
        return delay


#: Retry policy a client uses when the caller supplies none. It retries
#: up to three times with exponential backoff and jitter.
DEFAULT_RETRY_POLICY = RetryPolicy(
    max_attempts=3,
    base_delay=0.2,
    max_delay=5.0,
    jitter=True,
)
