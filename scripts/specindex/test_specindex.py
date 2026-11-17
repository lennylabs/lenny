#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Tests for scripts/specindex/specindex.py. Run: python3 -m unittest discover -s scripts/specindex"""
import io
import os
import shutil
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import specindex as S  # noqa: E402

REGISTRY = """# Conventions

Text.

## Code registry

| Code | Type | Path | Summary |
| :-- | :-- | :-- | :-- |
| `README` | conventions | [`README.md`](README.md) | Root map. |
| `CAPS` | index | [`capabilities/index.md`](capabilities/index.md) | Lists the areas. |
| `RTPOOL` | index | [`capabilities/runtimes-and-pools/index.md`](capabilities/runtimes-and-pools/index.md) | Lists the pool capabilities. |
| `POOL` | capability | [`capabilities/runtimes-and-pools/warm-pools.md`](capabilities/runtimes-and-pools/warm-pools.md) | Keeps pods warm. |
| `CLAIM` | capability | [`capabilities/runtimes-and-pools/pod-claims.md`](capabilities/runtimes-and-pools/pod-claims.md) | Claims a pod \\| for a session. |
"""


class Fixture(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.spec = os.path.join(self.root, "spec")
        os.makedirs(os.path.join(self.spec, "capabilities", "runtimes-and-pools"))
        self.write("conventions.md", REGISTRY)
        self.write("capabilities/runtimes-and-pools/warm-pools.md", "---\ncode: POOL\n---\n\n# Warm pools\n")
        self.write("capabilities/runtimes-and-pools/pod-claims.md", "# Pod claims\n")

    def tearDown(self):
        shutil.rmtree(self.root)

    def write(self, rel, text):
        full = os.path.join(self.spec, rel)
        os.makedirs(os.path.dirname(full), exist_ok=True)
        with open(full, "w", encoding="utf-8") as fh:
            fh.write(text)

    def read(self, rel):
        with open(os.path.join(self.spec, rel), encoding="utf-8") as fh:
            return fh.read()

    def run_tool(self, *args):
        out, err = io.StringIO(), io.StringIO()
        with redirect_stdout(out), redirect_stderr(err):
            code = S.main(["--root", self.root] + list(args))
        return code, out.getvalue(), err.getvalue()


class GenerateTest(Fixture):
    def test_writes_front_matter_summary_and_table(self):
        code, _, err = self.run_tool()
        self.assertEqual(code, 0, err)
        page = self.read("capabilities/runtimes-and-pools/index.md")
        self.assertTrue(page.startswith("---\ncode: RTPOOL\ntype: index\narea: runtimes-and-pools\nsummary: >-\n  Lists the pool capabilities.\n---\n"))
        self.assertIn("\n# Runtimes and pools\n\nLists the pool capabilities.\n\n## Pages\n", page)
        self.assertIn("| [Pod claims](pod-claims.md) | `CLAIM` | Claims a pod \\| for a session. |", page)
        self.assertIn("| [Warm pools](warm-pools.md) | `POOL` | Keeps pods warm. |", page)
        self.assertNotIn("status:", page)

    def test_parent_index_lists_subfolder_index(self):
        self.assertEqual(self.run_tool()[0], 0)
        top = self.read("capabilities/index.md")
        self.assertIn("| [Runtimes and pools](runtimes-and-pools/index.md) | `RTPOOL` | Lists the pool capabilities. |", top)
        self.assertNotIn("area:", top)

    def test_keeps_title_and_content_today_and_marks_placeholder(self):
        self.write("capabilities/runtimes-and-pools/index.md",
                   "# Runtimes, pools\n\n## Content today\n\n- [§5 Runtime](../../05_x.md)\n")
        self.assertEqual(self.run_tool()[0], 0)
        page = self.read("capabilities/runtimes-and-pools/index.md")
        self.assertIn("status: placeholder\n---", page)
        self.assertIn("\n# Runtimes, pools\n", page)
        self.assertTrue(page.endswith("## Content today\n\n- [§5 Runtime](../../05_x.md)\n"))
        self.assertLess(page.index("## Pages"), page.index("## Content today"))

    def test_second_run_is_a_fixed_point(self):
        self.assertEqual(self.run_tool()[0], 0)
        first = self.read("capabilities/runtimes-and-pools/index.md")
        code, out, _ = self.run_tool()
        self.assertEqual(code, 0)
        self.assertIn("wrote 0 of 2", out)
        self.assertEqual(first, self.read("capabilities/runtimes-and-pools/index.md"))


class CheckTest(Fixture):
    def test_check_reports_missing_and_stale_pages_and_writes_nothing(self):
        code, _, err = self.run_tool("--check")
        self.assertEqual(code, 1)
        self.assertIn("spec/capabilities/index.md", err)
        self.assertFalse(os.path.exists(os.path.join(self.spec, "capabilities", "index.md")))
        self.assertEqual(self.run_tool()[0], 0)
        self.assertEqual(self.run_tool("--check")[0], 0)
        self.write("capabilities/runtimes-and-pools/pod-claims.md", "# Pod claim\n")
        code, _, err = self.run_tool("--check")
        self.assertEqual(code, 1)
        self.assertIn("spec/capabilities/runtimes-and-pools/index.md", err)


class RegistryErrorTest(Fixture):
    def test_folder_without_index_row_is_refused(self):
        self.write("conventions.md", REGISTRY + "| `GW` | component | [`components/gateway/x.md`](components/gateway/x.md) | X. |\n")
        code, _, err = self.run_tool()
        self.assertEqual(code, 2)
        self.assertIn("components, components/gateway", err)

    def test_duplicate_code_is_refused(self):
        self.write("conventions.md", REGISTRY + "| `POOL` | capability | [`capabilities/x.md`](capabilities/x.md) | X. |\n")
        code, _, err = self.run_tool("--check")
        self.assertEqual(code, 2)
        self.assertIn("POOL is registered twice", err)

    def test_malformed_row_is_refused(self):
        self.write("conventions.md", REGISTRY + "| `pool` | capability | capabilities/x.md | X. |\n")
        code, _, err = self.run_tool()
        self.assertEqual(code, 2)
        self.assertIn("malformed", err)

    def test_missing_registry_section_and_empty_registry_are_refused(self):
        self.write("conventions.md", "# Conventions\n")
        self.assertIn("no '## Code registry'", self.run_tool()[2])
        self.write("conventions.md", "## Code registry\n\n| Code | Type | Path | Summary |\n| :-- | :-- | :-- | :-- |\n")
        self.assertIn("has no row", self.run_tool()[2])
        os.remove(os.path.join(self.spec, "conventions.md"))
        self.assertEqual(self.run_tool()[0], 2)

    def test_registry_ends_at_next_section(self):
        self.write("conventions.md", REGISTRY + "\n## Later\n\n| `ZZ` | odd row |\n")
        self.assertEqual(self.run_tool()[0], 0)


if __name__ == "__main__":
    unittest.main()
