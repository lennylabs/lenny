/* SPDX-License-Identifier: MIT */
/*
 * The property self-test of the runtime supervisor.
 *
 * spec: §4.7.11 item 8 (Runtime supervisor, Property self-test). Before it
 * dials, the supervisor tests that its non-dumpable mark resists same-UID
 * inspection and that its kill reaches a double-forked descendant, and
 * whether it is immune as PID-namespace init to SIGKILL and SIGSTOP. No
 * property tests a signal's sender, because no supervisor action reads
 * one.
 */
#include "supervisor.h"

#ifdef SV_TEST
int sv_test_grandchild_exits_early;
#endif

static void sv_sleep_ms(sv_u64 ms)
{
	sv_u64 end = sv_now_ms() + ms;
	for (sv_u64 now = sv_now_ms(); now < end; now = sv_now_ms()) {
		struct sv_timespec ts = sv_ts_until(now, end);
		sv_ppoll(SV_NULL, 0, &ts, sv_wait_mask());
	}
}

/* sv_proc_path writes /proc/<pid>[/leaf] into out. */
static void sv_proc_path(char *out, int pid, const char *leaf)
{
	sv_size n = 0;
	const char *pre = "/proc/";
	while (*pre)
		out[n++] = *pre++;
	n += sv_fmt_u64(out + n, (sv_u64)pid);
	if (leaf) {
		out[n++] = '/';
		while (*leaf)
			out[n++] = *leaf++;
	}
	out[n] = 0;
}

static int sv_proc_present(int pid)
{
	char path[64];
	struct sv_statx sx;
	sv_proc_path(path, pid, SV_NULL);
	return sv_statx(SV_AT_FDCWD, path, SV_AT_SYMLINK_NOFOLLOW, SV_STATX_TYPE, &sx) == 0;
}

/* sv_wait_exit_code blocks for pid and returns its exit code, or -1 for a signal. */
static int sv_wait_exit_code(int pid)
{
	int st = 0;
	if (sv_wait4(pid, &st, 0) != pid || SV_WIFSIGNALED(st))
		return -1;
	return SV_WEXITSTATUS(st);
}

/*
 * The inspecting child: attaching with ptrace (PTRACE_SEIZE, which runs
 * the same permission check as PTRACE_ATTACH without stopping the
 * target), opening the parent's /proc mem, and listing its /proc fd
 * directory must each fail.
 */
static int sv_inspect_parent(void)
{
	int ppid = sv_getppid();
	char path[64];
	int exposed = 0;
	if (sv_ptrace(SV_PTRACE_SEIZE, ppid) >= 0)
		exposed = 1; /* the child's exit detaches it */
	sv_proc_path(path, ppid, "mem");
	int fd = sv_openat(SV_AT_FDCWD, path, SV_O_RDONLY | SV_O_CLOEXEC, 0);
	if (fd >= 0) {
		exposed = 1;
		sv_close(fd);
	}
	sv_proc_path(path, ppid, "fd");
	fd = sv_openat(SV_AT_FDCWD, path, SV_O_RDONLY | SV_O_DIRECTORY | SV_O_CLOEXEC, 0);
	if (fd >= 0) {
		char buf[512] __attribute__((aligned(8)));
		if (sv_getdents64(fd, buf, sizeof(buf)) > 0)
			exposed = 1;
		sv_close(fd);
	}
	return exposed ? 1 : 0;
}

int sv_selftest_nondumpable(void)
{
	int pid = sv_fork();
	if (pid < 0)
		return 0;
	if (pid == 0)
		sv_exit(sv_inspect_parent());
	return sv_wait_exit_code(pid) == 0;
}

/* The grandchild waits until a signal ends it. */
__attribute__((noreturn)) static void sv_grandchild(void)
{
#ifdef SV_TEST
	if (sv_test_grandchild_exits_early)
		sv_exit(0);
#endif
	for (;;)
		sv_ppoll(SV_NULL, 0, SV_NULL, sv_wait_mask());
}

/*
 * sv_spawn_grandchild double-forks and returns the grandchild's PID, or
 * -1. The intermediate child writes it to a pipe and exits, so the
 * grandchild is reparented to the namespace init.
 */
static int sv_spawn_grandchild(void)
{
	int fds[2];
	if (sv_pipe2(fds, SV_O_CLOEXEC) < 0)
		return -1;
	int a = sv_fork();
	if (a == 0) {
		sv_close(fds[0]);
		int b = sv_fork();
		if (b == 0)
			sv_grandchild();
		sv_write(fds[1], &b, sizeof(b));
		sv_exit(0);
	}
	sv_close(fds[1]);
	int b = -1;
	if (a < 0 || sv_read(fds[0], &b, sizeof(b)) != (sv_ssize)sizeof(b))
		b = -1;
	sv_close(fds[0]);
	if (a > 0)
		sv_wait_exit_code(a);
	return b > 0 ? b : -1;
}

/* sv_await_absent reaps pid when it is the caller's child and waits for its /proc entry to go. */
static int sv_await_absent(int pid, sv_u64 ms)
{
	sv_u64 end = sv_now_ms() + ms;
	for (;;) {
		int st;
		sv_wait4(pid, &st, SV_WNOHANG);
		if (!sv_proc_present(pid))
			return 1;
		if (sv_now_ms() >= end)
			return 0;
		sv_sleep_ms(5);
	}
}

int sv_selftest_kill_reach(sv_kill_fn kill_all)
{
	if (!kill_all)
		return 0;
	int b = sv_spawn_grandchild();
	if (b < 0)
		return 0;
#ifdef SV_TEST
	if (sv_test_grandchild_exits_early)
		sv_await_absent(b, SV_SELFTEST_SETTLE_MS * 10);
#endif
	/* A grandchild that is already gone cannot show that the kill reached it. */
	if (!sv_proc_present(b))
		return 0;
	kill_all(SV_SIGKILL);
	return sv_await_absent(b, SV_SELFTEST_SETTLE_MS * 10);
}

/* sv_proc_state returns the state letter of /proc/<pid>/stat, or 0. */
static char sv_proc_state(int pid)
{
	char path[64];
	char buf[512];
	sv_proc_path(path, pid, "stat");
	int fd = sv_openat(SV_AT_FDCWD, path, SV_O_RDONLY | SV_O_CLOEXEC, 0);
	if (fd < 0)
		return 0;
	sv_ssize n = sv_read(fd, buf, sizeof(buf) - 1);
	sv_close(fd);
	if (n <= 0)
		return 0;
	buf[n] = 0;
	sv_ssize close_paren = -1;
	for (sv_ssize i = 0; i < n; i++)
		if (buf[i] == ')')
			close_paren = i;
	if (close_paren < 0 || close_paren + 2 >= n)
		return 0;
	return buf[close_paren + 2];
}

/*
 * The immunity probe, run in a child of the namespace init: SIGSTOP is
 * sent first and the init's state read. Only when the init stayed
 * running is SIGKILL sent, because the kernel discards both signals for a
 * namespace init through the same rule, and an init that a SIGSTOP
 * stopped would end at the SIGKILL.
 */
static int sv_probe_init_immunity(void)
{
	sv_kill(1, SV_SIGSTOP);
	sv_sleep_ms(SV_SELFTEST_SETTLE_MS);
	char st = sv_proc_state(1);
	if (st == 'T' || st == 't' || st == 0) {
		sv_kill(1, SV_SIGCONT);
		return 1;
	}
	sv_kill(1, SV_SIGKILL);
	sv_sleep_ms(SV_SELFTEST_SETTLE_MS);
	return 0;
}

void sv_selftest_init_immune(int *sigstop_immune, int *sigkill_immune)
{
	*sigstop_immune = 0;
	*sigkill_immune = 0;
	/* Outside a namespace init there is no immunity, and nothing is sent. */
	if (sv_getpid() != 1)
		return;
	int pid = sv_fork();
	if (pid < 0)
		return;
	if (pid == 0)
		sv_exit(sv_probe_init_immunity());
	if (sv_wait_exit_code(pid) == 0) {
		*sigstop_immune = 1;
		*sigkill_immune = 1;
	}
}

void sv_selftest(struct sv_properties *out)
{
	out->nondumpable = sv_selftest_nondumpable();
	/* kill(-1) is sent only as a namespace init, where it reaches the namespace alone. */
	out->kill_reach = sv_selftest_kill_reach(sv_getpid() == 1 ? sv_kill_all_default : SV_NULL);
	sv_selftest_init_immune(&out->init_sigstop_immune, &out->init_sigkill_immune);
}
