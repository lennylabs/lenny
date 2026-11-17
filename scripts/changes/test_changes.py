#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Tests for scripts/changes/changes.py. Run: python3 -m unittest discover -s scripts/changes"""
import io
import os
import shutil
import sys
import tempfile
import unittest
from contextlib import redirect_stdout, redirect_stderr

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import changes as C  # noqa: E402


def step(sid, title, status='Planned', deps=None, aliases=None, closes=None, lanes='specification, code'):
    out = [f'### {sid} {title}', f'- status: {status}']
    if aliases:
        out.append(f'- aliases: [{", ".join(aliases)}]')
    out.append(f'- lanes: [{lanes}]')
    if deps:
        out.append(f'- depends-on: [{", ".join(deps)}]')
    if closes:
        out.append(f'- closes: [{", ".join(closes)}]')
    return '\n'.join(out) + '\n\nDescription.\n'


class Fixture(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.tree = C.Tree(self.root)
        os.makedirs(os.path.join(self.root, 'changes', 'programs'))
        os.makedirs(os.path.join(self.root, 'proposals', '0099-old'))
        C.write(os.path.join(self.root, 'changes', 'programs', 'p.md'),
                '---\nprogram: p\nmembers: [0100, 0101, 0105]\nmilestones:\n  - id: M1\n    title: First\n'
                '    steps: [0100.S1, 0105]\ncurrent: M1\n---\n\n# Program p\n')
        self.change('0100-alpha', 'Alpha', 'Decided', [step('0100.S1', 'One', aliases=['AL-1']),
                                                       step('0100.S2', 'Two', deps=['0100.S1'])])
        self.change('0101-beta', 'Beta', 'Draft', [step('0101.S1', 'Uno', deps=['0100.S2 (part)'])],
                    open_decisions='1. **A question.** Options.\n')

    def tearDown(self):
        shutil.rmtree(self.root)

    def change(self, d, title, status, steps, open_decisions='None.\n'):
        p = os.path.join(self.root, 'changes', d)
        os.makedirs(p, exist_ok=True)
        C.write(os.path.join(p, 'change.md'), f'# {title}\n\nStatus: `{status}`, revision 1, 2026-01-01.\n\n## Problem\n\nText.\n')
        C.write(os.path.join(p, 'decisions.md'), f'# Decisions\n\n## Open decisions\n\n{open_decisions}\n## Decisions\n')
        C.write(os.path.join(p, 'steps.md'), '# Steps\n\n## Steps\n\n' + '\n'.join(steps))

    def run_cli(self, *args):
        out, err = io.StringIO(), io.StringIO()
        with redirect_stdout(out), redirect_stderr(err):
            rc = C.main(['--root', self.root, '--today', '2026-02-03', *args])
        return rc, out.getvalue(), err.getvalue()

    def problems(self):
        changes, steps, errors = C.load(self.tree)
        return C.check(changes, steps, errors), C.warnings(steps)


class CheckTest(Fixture):
    def test_valid_tree_has_no_problems(self):
        errors, warnings = self.problems()
        self.assertEqual(errors, [])
        self.assertEqual(warnings, [])
        rc, out, _ = self.run_cli('check')
        self.assertEqual(rc, 0)
        self.assertIn('3 steps in 2 changes; 0 problems', out)

    def test_unknown_statuses_lanes_and_fields_are_problems(self):
        self.change('0102-gamma', 'Gamma', 'Started', [step('0102.S1', 'X', status='Done', lanes='code, poetry')])
        p = os.path.join(self.root, 'changes', '0102-gamma', 'steps.md')
        C.write(p, C.read(p).replace('- lanes:', '- owner: me\n- lanes:'))
        errors, _ = self.problems()
        text = '\n'.join(errors)
        self.assertIn("status 'Started' unknown", text)
        self.assertIn("0102.S1: status 'Done' unknown", text)
        self.assertIn("lane 'poetry' unknown", text)
        self.assertIn("field 'owner' unknown", text)

    def test_missing_dependency_duplicate_alias_and_numbering(self):
        self.change('0102-gamma', 'Gamma', 'Draft', [step('0102.S2', 'X', deps=['0100.S9'], aliases=['AL-1'])])
        errors, _ = self.problems()
        text = '\n'.join(errors)
        self.assertIn('dependency 0100.S9 does not exist', text)
        self.assertIn('alias AL-1 names both', text)
        self.assertIn('not numbered S1 to S1', text)

    def test_whole_step_cycle_is_an_error_and_partial_cycle_a_warning(self):
        self.change('0102-gamma', 'Gamma', 'Draft', [step('0102.S1', 'X', deps=['0102.S2']),
                                                     step('0102.S2', 'Y', deps=['0102.S1'])])
        self.change('0103-delta', 'Delta', 'Draft', [step('0103.S1', 'X', deps=['0103.S2 (part)']),
                                                     step('0103.S2', 'Y', deps=['0103.S1'])])
        errors, warnings = self.problems()
        self.assertTrue(any('cycle among: 0102.S1, 0102.S2' in e for e in errors))
        self.assertFalse(any('0103' in e for e in errors))
        self.assertTrue(any('0103.S1, 0103.S2' in w for w in warnings))

    def test_change_status_must_agree_with_steps(self):
        self.change('0102-gamma', 'Gamma', 'Implemented', [step('0102.S1', 'X', status='Landed'),
                                                           step('0102.S2', 'Y')])
        self.change('0103-delta', 'Delta', 'Decided', [step('0103.S1', 'X', status='Building')])
        self.change('0104-eps', 'Eps', 'In progress', [step('0104.S1', 'X')])
        text = '\n'.join(self.problems()[0])
        self.assertIn('0102-gamma: status Implemented but a step is open', text)
        self.assertIn('0103-delta: status Decided but a step has started', text)
        self.assertIn('0104-eps: status In progress but no step has started', text)

    def test_step_entries_in_change_md_are_refused(self):
        p = os.path.join(self.root, 'changes', '0100-alpha', 'change.md')
        C.write(p, C.read(p) + '\n### 0100.S3 Stray\n')
        self.assertTrue(any('belong in steps.md' in e for e in self.problems()[0]))

    def test_check_exits_nonzero_on_a_problem(self):
        self.change('0102-gamma', 'Gamma', 'Draft', [step('0102.S1', 'X', deps=['nope'])])
        rc, out, _ = self.run_cli('check')
        self.assertEqual(rc, 1)
        self.assertIn("dependency 'nope' is not a step identifier", out)


class BoardTest(Fixture):
    def test_board_classifies_steps_and_places_milestones(self):
        self.assertEqual(self.run_cli('board')[0], 0)
        board = C.read(os.path.join(self.root, 'changes', 'BOARD.md'))
        self.assertIn('### M1: First (current)', board)
        self.assertIn('Not yet resolved to steps: 0105.', board)
        self.assertIn('| [0100.S1](0100-alpha/steps.md#0100s1-one) | One | Ready |  |', board)
        self.assertIn('| [0100.S2](0100-alpha/steps.md#0100s2-two) | Two | Blocked | 0100.S1 |', board)
        self.assertIn('| [0101.S1](0101-beta/steps.md#0101s1-uno) | Uno | Waiting on decisions | 0101 is `Draft`; a part waits on 0100.S2 |', board)
        self.assertIn('| [0101](0101-beta/change.md) | Beta | `Draft` | 0 of 1 | 1 |', board)

    def test_a_ruled_ready_step_of_a_draft_change_is_ready(self):
        p = os.path.join(self.root, 'changes', '0101-beta', 'steps.md')
        C.write(p, C.read(p).replace('- lanes: [specification, code]', '- lanes: [specification, code]\n- ruled-ready: 2026-02-03, names only'))
        self.assertEqual(self.problems()[0], [])
        self.run_cli('board')
        board = C.read(os.path.join(self.root, 'changes', 'BOARD.md'))
        self.assertIn('| Uno | Ready | a part waits on 0100.S2 |', board)
        C.write(p, C.read(p).replace('ruled-ready: 2026-02-03, names only', 'ruled-ready: soon'))
        self.assertTrue(any('ruled-ready must start with the date' in e for e in self.problems()[0]))

    def test_a_draft_change_may_build_only_its_ruled_ready_steps(self):
        p = os.path.join(self.root, 'changes', '0101-beta', 'steps.md')
        C.write(p, C.read(p).replace('- status: Planned', '- status: Building').replace(
            '- lanes: [specification, code]', '- lanes: [specification, code]\n- ruled-ready: 2026-02-03, names only'))
        self.assertEqual(self.problems()[0], [])
        C.write(p, C.read(p).replace('- ruled-ready: 2026-02-03, names only\n', ''))
        self.assertTrue(any('status Draft but a step has started' in e for e in self.problems()[0]))

    def test_board_check_detects_a_stale_board(self):
        self.run_cli('board')
        self.assertEqual(self.run_cli('board', '--check')[0], 0)
        self.run_cli('step', '0100.S1', 'Landed')
        rc, out, _ = self.run_cli('board', '--check')
        self.assertEqual(rc, 1)
        self.assertIn('out of date', out)


class EditTest(Fixture):
    def test_new_takes_the_next_number_after_folders_members_and_proposals(self):
        rc, out, _ = self.run_cli('new', 'zeta-work', 'Zeta work')
        self.assertEqual((rc, out.strip()), (0, '0106-zeta-work'))
        d = os.path.join(self.root, 'changes', '0106-zeta-work')
        self.assertEqual(sorted(os.listdir(d)), ['change.md', 'decisions.md', 'review-history.md', 'steps.md'])
        self.assertIn('Status: `Draft`, revision 1, 2026-02-03.', C.read(os.path.join(d, 'change.md')))
        self.assertEqual(self.problems()[0], [])

    def test_new_refuses_a_bad_slug(self):
        rc, _, err = self.run_cli('new', 'Bad Slug')
        self.assertEqual(rc, 2)
        self.assertIn('not lowercase words', err)

    def test_step_sets_the_status_and_notes_a_disagreeing_change(self):
        rc, out, _ = self.run_cli('step', '0100.S1', 'Building')
        self.assertEqual(rc, 0)
        self.assertIn('disagrees with its steps', out)
        self.assertIn('- status: Building', C.read(os.path.join(self.root, 'changes', '0100-alpha', 'steps.md')))
        self.assertEqual(self.run_cli('step', '0100.S1', 'Gone')[0], 2)
        self.assertEqual(self.run_cli('step', '0100.S7', 'Landed')[0], 2)

    def test_status_refuses_a_status_the_steps_contradict(self):
        rc, _, err = self.run_cli('status', '0100', 'Implemented')
        self.assertEqual(rc, 2)
        self.assertIn('a step is open', err)
        self.run_cli('step', '0100.S1', 'Landed')
        self.assertEqual(self.run_cli('status', '0100', 'In progress')[0], 0)
        self.assertIn('Status: `In progress`, revision 1, 2026-02-03.',
                      C.read(os.path.join(self.root, 'changes', '0100-alpha', 'change.md')))
        self.assertEqual(self.run_cli('status', '0100', 'Finished')[0], 2)
        self.assertEqual(self.run_cli('status', '0999', 'Draft')[0], 2)

    def test_archive_moves_the_folder_and_rewrites_links_both_ways(self):
        a = os.path.join(self.root, 'changes', '0100-alpha')
        C.write(os.path.join(a, 'change.md'), C.read(os.path.join(a, 'change.md')) +
                '\nSee [beta](../0101-beta/change.md#problem), [steps](steps.md#steps), and [readme](../README.md).\n')
        C.write(os.path.join(self.root, 'changes', 'README.md'), '# Changes\n')
        b = os.path.join(self.root, 'changes', '0101-beta', 'change.md')
        C.write(b, C.read(b) + '\nSee [alpha](../0100-alpha/change.md#problem) and `[code](../0100-alpha/change.md)`.\n')
        self.assertEqual(self.run_cli('archive', '0100')[0], 2)  # still Decided
        self.run_cli('step', '0100.S1', 'Landed')
        self.run_cli('step', '0100.S2', 'Landed')
        self.run_cli('status', '0100', 'Implemented')
        rc, out, _ = self.run_cli('archive', '0100')
        self.assertEqual((rc, out.strip()), (0, 'archive/2026-02-03-0100-alpha'))
        moved = os.path.join(self.root, 'changes', 'archive', '2026-02-03-0100-alpha', 'change.md')
        self.assertIn('[beta](../../0101-beta/change.md#problem)', C.read(moved))
        self.assertIn('[steps](steps.md#steps)', C.read(moved))
        self.assertIn('[readme](../../README.md)', C.read(moved))
        self.assertIn('[alpha](../archive/2026-02-03-0100-alpha/change.md#problem)', C.read(b))
        self.assertIn('`[code](../0100-alpha/change.md)`', C.read(b))
        self.assertEqual(C.broken_links(self.tree, C.md_files(self.tree)), [])
        self.assertEqual(self.problems()[0], [])
        self.assertEqual(self.run_cli('archive', '0100')[0], 2)
        board_rc = self.run_cli('board')[0]
        self.assertEqual(board_rc, 0)
        self.assertNotIn('0100-alpha', C.read(os.path.join(self.root, 'changes', 'BOARD.md')))


class LinksTest(Fixture):
    def test_broken_files_and_anchors_are_reported_against_a_baseline(self):
        b = os.path.join(self.root, 'changes', '0101-beta', 'change.md')
        C.write(b, C.read(b) + '\n[gone](../nowhere.md) [bad](../0100-alpha/change.md#nope) [ok](#problem)\n')
        found = C.broken_links(self.tree, C.md_files(self.tree))
        self.assertEqual(sorted(k for _, _, k in found), ['anchor', 'file'])
        base = os.path.join(self.root, 'base.json')
        self.assertEqual(self.run_cli('links', '--write-baseline', base)[0], 0)
        self.assertEqual(self.run_cli('links', '--baseline', base)[0], 0)
        C.write(b, C.read(b) + '\n[new](../0100-alpha/steps.md#missing)\n')
        rc, out, _ = self.run_cli('links', '--baseline', base)
        self.assertEqual(rc, 1)
        self.assertIn('1 not in the baseline', out)

    def test_anchors_follow_duplicate_numbering_and_explicit_ids(self):
        got = C.anchors('# A\n## Notes\n## Notes\n## X {#custom}\n```\n## Not a heading\n```\n<a id="raw"></a>\n')
        self.assertEqual(got, {'a', 'notes', 'notes-1', 'x', 'custom', 'raw'})


STATUS_PROGRAM = """---
program: p
goal: >-
  Everything lands
  by January 2027.
members: [0100, 0101, 0105]
milestones:
  - id: M1
    title: First
    steps: [0100]
    exit: The alpha work has landed (0100.S1, 0100.S2).
  - id: M2
    title: Second, after 0100 lands
    steps: [0101.S1, 0105]
    exit: >-
      The beta step has landed
      and 0101.S1 is checked.
  - id: M3
    title: Third
    exit: Nothing yet.
current: M2
---

# Program p
"""


class StatusPageTest(Fixture):
    def setUp(self):
        super().setUp()
        C.write(os.path.join(self.root, 'changes', 'programs', 'p.md'), STATUS_PROGRAM)
        os.makedirs(os.path.join(self.root, 'docs', 'about'))
        self.page = os.path.join(self.root, 'docs', 'about', 'status.md')
        C.write(self.page, '# Status\n\nIntro written by hand.\n\n<!-- generated:status -->\nstale\n<!-- /generated:status -->\n\n## Surfaces\n\nKept.\n')

    def test_status_page_states_goal_milestones_exit_criteria_and_share_landed(self):
        self.run_cli('step', '0100.S1', 'Landed')
        self.run_cli('step', '0100.S2', 'Building')
        self.assertEqual(self.run_cli('status-page')[0], 0)
        page = C.read(self.page)
        self.assertTrue(page.startswith('# Status\n\nIntro written by hand.\n\n<!-- generated:status -->\n'))
        self.assertTrue(page.endswith('<!-- /generated:status -->\n\n## Surfaces\n\nKept.\n'))
        self.assertNotIn('stale', page)
        self.assertIn('Goal: Everything lands by January 2027.', page)
        self.assertIn('| First | 1 of 2 | In progress |', page)
        self.assertIn('| Second, after the "Alpha" change lands (current) | 0 of 1 | Not started |', page)
        self.assertIn('| Third | None assigned yet | Not started |', page)
        self.assertIn('Exit criteria: The alpha work has landed.\n', page)
        self.assertIn('Exit criteria: The beta step has landed and a step of the "Beta" change is checked.', page)
        self.assertIn('1 of its 2 steps have landed, and 1 is being built.', page)
        self.assertIn('Part of its work is not yet divided into steps', page)
        self.assertIn('Its one step has not started. Part of its work', page)
        self.assertIn('No steps are assigned to this milestone yet.', page)
        self.assertIn('https://github.com/lennylabs/lenny/blob/main/changes/BOARD.md', page)
        self.assertNotIn('M1', page)
        self.assertNotIn('0100', page)

    def test_a_milestone_whose_steps_all_landed_is_done_and_cancelled_steps_do_not_count(self):
        self.run_cli('step', '0100.S1', 'Landed')
        self.run_cli('step', '0100.S2', 'Cancelled')
        self.run_cli('status-page')
        page = C.read(self.page)
        self.assertIn('| First | 1 of 1 | Done |', page)
        self.assertIn('Its one step has landed.', page)

    def test_status_page_check_detects_a_stale_page(self):
        self.assertEqual(self.run_cli('status-page', '--check')[0], 1)
        self.run_cli('status-page')
        self.assertEqual(self.run_cli('status-page', '--check')[0], 0)
        self.run_cli('step', '0100.S1', 'Landed')
        rc, out, _ = self.run_cli('status-page', '--check')
        self.assertEqual(rc, 1)
        self.assertIn('out of date', out)

    def test_status_page_without_markers_is_an_error(self):
        C.write(self.page, '# Status\n')
        rc, _, err = self.run_cli('status-page')
        self.assertEqual(rc, 2)
        self.assertIn('no <!-- generated:status -->', err)



class SymlinkTest(Fixture):
    """The commands refuse to write through a symbolic link or into a
    directory that resolves outside the repository."""

    def setUp(self):
        super().setUp()
        self.outside = tempfile.mkdtemp()
        self.victim = os.path.join(self.outside, 'victim.txt')
        C.write(self.victim, 'untouched\n')

    def tearDown(self):
        shutil.rmtree(self.outside)
        super().tearDown()

    def test_board_refuses_a_symlinked_board(self):
        os.symlink(self.victim, os.path.join(self.root, 'changes', 'BOARD.md'))
        rc, _, err = self.run_cli('board')
        self.assertEqual(rc, 2)
        self.assertIn('symbolic link', err)
        self.assertEqual(C.read(self.victim), 'untouched\n')

    def test_status_page_refuses_a_symlinked_page_and_a_directory_outside(self):
        C.write(os.path.join(self.root, 'changes', 'programs', 'p.md'), STATUS_PROGRAM)
        page = '# Status\n\n<!-- generated:status -->\n<!-- /generated:status -->\n'
        C.write(self.victim, page)
        os.makedirs(os.path.join(self.root, 'docs', 'about'))
        os.symlink(self.victim, os.path.join(self.root, 'docs', 'about', 'status.md'))
        rc, _, err = self.run_cli('status-page')
        self.assertEqual(rc, 2)
        self.assertIn('symbolic link', err)
        self.assertEqual(C.read(self.victim), page)
        os.remove(os.path.join(self.root, 'docs', 'about', 'status.md'))
        os.rmdir(os.path.join(self.root, 'docs', 'about'))
        C.write(os.path.join(self.outside, 'status.md'), page)
        os.symlink(self.outside, os.path.join(self.root, 'docs', 'about'))
        rc, _, err = self.run_cli('status-page')
        self.assertEqual(rc, 2)
        self.assertIn('outside the repository', err)
        self.assertEqual(C.read(os.path.join(self.outside, 'status.md')), page)

    def test_new_refuses_a_change_folder_that_is_a_symlink(self):
        os.symlink(self.outside, os.path.join(self.root, 'changes', '0106-zeta'))
        rc, _, err = self.run_cli('new', 'zeta')
        self.assertEqual(rc, 2)
        self.assertIn('symbolic link', err)
        self.assertEqual(sorted(os.listdir(self.outside)), ['victim.txt'])

    def test_archive_refuses_an_archive_directory_outside_the_repository(self):
        self.run_cli('step', '0100.S1', 'Landed')
        self.run_cli('step', '0100.S2', 'Landed')
        self.run_cli('status', '0100', 'Implemented')
        os.symlink(self.outside, os.path.join(self.root, 'changes', 'archive'))
        rc, _, err = self.run_cli('archive', '0100')
        self.assertEqual(rc, 2)
        self.assertIn('outside the repository', err)
        self.assertTrue(os.path.isdir(os.path.join(self.root, 'changes', '0100-alpha')))
        self.assertEqual(sorted(os.listdir(self.outside)), ['victim.txt'])

    def test_step_refuses_a_symlinked_steps_file(self):
        steps = os.path.join(self.root, 'changes', '0100-alpha', 'steps.md')
        C.write(self.victim, C.read(steps))
        os.remove(steps)
        os.symlink(self.victim, steps)
        before = C.read(self.victim)
        rc, _, err = self.run_cli('step', '0100.S1', 'Building')
        self.assertEqual(rc, 2)
        self.assertIn('symbolic link', err)
        self.assertEqual(C.read(self.victim), before)


if __name__ == '__main__':
    unittest.main()
