# SPDX-License-Identifier: MIT

"""Unit tests for the section 7.1 ``sessionIsolationLevel`` decode.

The tests drive :meth:`lenny.IsolationLevel.from_wire` and
:meth:`lenny.CreateSessionResult.from_wire` against the wire envelope to
cover the ``conversationContinuity`` field across the ``session`` and
``service`` execution modes, plus the omitted-field default.

The tests use only the Python standard library (:mod:`unittest`),
matching the SDK's standard-library-only policy. Run them with
``python3 -m unittest discover -s tests`` from ``sdks/client/python``.
"""

from __future__ import annotations

import unittest

import lenny


class ConversationContinuityDecodeTest(unittest.TestCase):
    """The section 7.1 ``conversationContinuity`` field decode."""

    def test_session_mode_preserves_platform_continuity(self) -> None:
        # spec: section 7.1 sessionIsolationLevel -- session mode reports
        # platform conversation continuity alongside the session
        # executionMode value.
        level = lenny.IsolationLevel.from_wire(
            {
                "executionMode": "session",
                "isolationProfile": "gvisor",
                "podReuse": False,
                "residualStateWarning": False,
                "conversationContinuity": "platform",
            }
        )
        self.assertEqual(level.execution_mode, "session")
        self.assertEqual(level.conversation_continuity, "platform")

    def test_service_mode_declares_no_continuity(self) -> None:
        # spec: section 7.1 sessionIsolationLevel -- service mode reports
        # no conversation continuity alongside the service executionMode
        # value.
        result = lenny.CreateSessionResult.from_wire(
            {
                "id": "sess_2",
                "sessionIsolationLevel": {
                    "executionMode": "service",
                    "isolationProfile": "runc",
                    "podReuse": True,
                    "scrubPolicy": "none",
                    "residualStateWarning": True,
                    "conversationContinuity": "none",
                },
            }
        )
        self.assertEqual(result.isolation_level.execution_mode, "service")
        self.assertEqual(result.isolation_level.conversation_continuity, "none")

    def test_omitted_continuity_defaults_to_empty(self) -> None:
        # spec: section 7.1 sessionIsolationLevel -- an omitted
        # conversationContinuity decodes to the empty string, matching
        # the optional decode of every other isolation field.
        level = lenny.IsolationLevel.from_wire({"executionMode": "session"})
        self.assertEqual(level.conversation_continuity, "")


if __name__ == "__main__":
    unittest.main()
