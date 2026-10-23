/* SPDX-License-Identifier: MIT */
/*
 * Cases for the property self-test outside a PID namespace. The passing
 * kill-reach and init-immunity results need the supervisor as namespace
 * init and are asserted from its ready message in cases_ns.c.
 *
 * spec: §4.7.11 item 8 (Runtime supervisor, Property self-test).
 */
#define _GNU_SOURCE
#include "harness.h"

#include <signal.h>
#include <sys/prctl.h>
#include <sys/stat.h>

/* The non-dumpable mark resists the same-UID inspecting child; without it the property fails. */
SV_CASE(selftest_nondumpable_property)
{
	SV_EQ_INT(sv_install_signals(), 0);
	SV_EQ_INT(prctl(PR_SET_DUMPABLE, 1), 0);
	SV_EQ_INT(sv_selftest_nondumpable(), 0);
	SV_EQ_INT(prctl(PR_SET_DUMPABLE, 0), 0);
	SV_EQ_INT(sv_selftest_nondumpable(), 1);
	return 0;
}

static int kill_nothing(int sig)
{
	(void)sig;
	return 0;
}

/*
 * Kill reach fails without a kill, with a kill that reaches nothing, and
 * with a grandchild that exited before the kill, whose absence would
 * otherwise read as a reached kill.
 */
SV_CASE(selftest_kill_reach_negative_paths)
{
	SV_EQ_INT(sv_install_signals(), 0);
	SV_EQ_INT(sv_selftest_kill_reach(NULL), 0);
	SV_EQ_INT(sv_selftest_kill_reach(kill_nothing), 0);
	sv_test_grandchild_exits_early = 1;
	SV_EQ_INT(sv_selftest_kill_reach(kill_nothing), 0);
	sv_test_grandchild_exits_early = 0;
	return 0;
}

/* Outside a PID namespace's init no immunity is reported and nothing is sent to PID 1. */
SV_CASE(selftest_init_immunity_outside_namespace_init)
{
	int stop = -1, killed = -1;
	sv_selftest_init_immune(&stop, &killed);
	SV_EQ_INT(stop, 0);
	SV_EQ_INT(killed, 0);
	return 0;
}

/*
 * selftest mode writes one line per property to stdout and exits non-zero
 * when a gating property fails: outside a namespace init kill reach
 * fails, so the mode exits with the self-test status.
 */
SV_CASE(modes_selftest_outside_namespace_reports_and_fails)
{
	char sup[4096];
	sv_bin_path(sup, sizeof(sup), "lenny-supervisor-prefixed");
	int fds[2];
	SV_EQ_INT(pipe(fds), 0);
	pid_t pid = fork();
	if (pid == 0) {
		dup2(fds[1], 1);
		execl(sup, sup, "selftest", (char *)NULL);
		_exit(126);
	}
	close(fds[1]);
	char buf[512];
	size_t len = 0;
	ssize_t n;
	while ((n = read(fds[0], buf + len, sizeof(buf) - 1 - len)) > 0)
		len += (size_t)n;
	buf[len] = 0;
	int st;
	waitpid(pid, &st, 0);
	SV_CHECK(WIFEXITED(st) && WEXITSTATUS(st) == SV_EXIT_SELFTEST, "status %d", st);
	SV_EQ_STR(buf, "nondumpable pass\nkillReach fail\ninitSigkillImmune fail\ninitSigstopImmune fail\n");
	return 0;
}

/* stage copies the binary to DEST/lenny-supervisor with mode 0555. */
SV_CASE(modes_stage_copies_binary_read_only)
{
	char sup[4096], dest[4096], staged[4200];
	sv_bin_path(sup, sizeof(sup), "lenny-supervisor-prefixed");
	snprintf(dest, sizeof(dest), "%s/stage-%d", SV_PATH_PREFIX, getpid());
	sv_rm_rf(dest);
	sv_mkdir_p(dest);
	pid_t pid = fork();
	if (pid == 0) {
		execl(sup, sup, "stage", dest, (char *)NULL);
		_exit(126);
	}
	int st;
	waitpid(pid, &st, 0);
	SV_CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 0, "status %d", st);
	snprintf(staged, sizeof(staged), "%s/lenny-supervisor", dest);
	struct stat a, b;
	SV_EQ_INT(stat(staged, &a), 0);
	SV_EQ_INT(stat(sup, &b), 0);
	SV_EQ_INT(a.st_mode & 07777, 0555);
	SV_EQ_INT(a.st_size, b.st_size);
	/* A destination that is a symbolic link is refused. */
	char link[4200];
	snprintf(link, sizeof(link), "%s-link", dest);
	unlink(link);
	symlink(dest, link);
	pid = fork();
	if (pid == 0) {
		execl(sup, sup, "stage", link, (char *)NULL);
		_exit(126);
	}
	waitpid(pid, &st, 0);
	SV_CHECK(WIFEXITED(st) && WEXITSTATUS(st) == SV_EXIT_STAGE, "status %d", st);
	unlink(link);
	sv_rm_rf(dest);
	return 0;
}
