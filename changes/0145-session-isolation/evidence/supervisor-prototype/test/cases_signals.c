/* SPDX-License-Identifier: MIT */
/*
 * Cases for the supervisor's signal dispositions, run in the case
 * process after it installs them as the supervisor does.
 *
 * spec: §4.7.10 (Runtime supervisor, Signals). The rows that need the
 * supervisor as PID-namespace init (the kubelet's stop signal, SIGKILL
 * and SIGSTOP from inside and outside the namespace, and each signal of
 * a row sent from inside and outside) are in cases_ns.c.
 */
#define _GNU_SOURCE
#include "harness.h"

#include <signal.h>
#include <sys/socket.h>
#include <sys/syscall.h>

static sv_u64 bit(int sig)
{
	return 1ULL << (sig - 1);
}

static struct sv_procs procs;
static struct sv_waiter waiter;

static void setup_waiter(void)
{
	sv_procs_init(&procs);
	memset(&waiter, 0, sizeof(waiter));
	waiter.procs = &procs;
	waiter.conn_fd = -1;
}

/* Every signal from 1 to _NSIG - 1 is handled other than SIGKILL, SIGSTOP, and the fault signals. */
SV_CASE(signals_handled_and_fault_masks)
{
	sv_u64 h = sv_handled_mask(), f = sv_fault_mask();
	for (int sig = 1; sig < SV_NSIG; sig++) {
		int fault = sig == SV_SIGSEGV || sig == SV_SIGBUS || sig == SV_SIGFPE || sig == SV_SIGILL;
		int uncatchable = sig == SV_SIGKILL || sig == SV_SIGSTOP;
		SV_EQ_INT(!!(h & bit(sig)), !fault && !uncatchable);
		SV_EQ_INT(!!(f & bit(sig)), fault);
		SV_EQ_INT(sv_is_fault_signal(sig), fault);
		SV_EQ_INT(sv_is_handled_signal(sig), !fault && !uncatchable);
	}
	SV_EQ_INT(sv_is_handled_signal(0), 0);
	SV_EQ_INT(sv_is_handled_signal(SV_NSIG), 0);
	return 0;
}

/*
 * After installation every handled signal and every fault signal is
 * blocked, each handled signal has a flag-setting handler without
 * SA_SIGINFO, each fault signal keeps SIG_DFL, and ppoll's mask unblocks
 * the handled signals alone.
 */
SV_CASE(signals_install_dispositions)
{
	SV_EQ_INT(sv_install_signals(), 0);
	unsigned long long blk = sv_status_mask(getpid(), "SigBlk");
	unsigned long long cgt = sv_status_mask(getpid(), "SigCgt");
	SV_CHECK(blk == (sv_handled_mask() | sv_fault_mask()), "SigBlk %llx", blk);
	SV_CHECK(cgt == sv_handled_mask(), "SigCgt %llx", cgt);
	for (int sig = 1; sig < SV_NSIG; sig++) {
		if (sig == SV_SIGKILL || sig == SV_SIGSTOP)
			continue;
		struct sv_kernel_sigaction old;
		memset(&old, 0, sizeof(old));
		SV_EQ_INT(syscall(SYS_rt_sigaction, sig, NULL, &old, 8), 0);
		if (sv_is_fault_signal(sig)) {
			SV_CHECK(old.handler == SV_SIG_DFL, "fault signal %d keeps SIG_DFL", sig);
		} else {
			SV_CHECK(old.handler != SV_SIG_DFL && old.handler != (void (*)(int))1,
				 "signal %d has a handler", sig);
			SV_CHECK(!(old.flags & 4UL), "signal %d is installed without SA_SIGINFO", sig);
			SV_CHECK((old.mask & sv_handled_mask()) == sv_handled_mask(),
				 "signal %d blocks every handled signal while its handler runs", sig);
		}
	}
	SV_CHECK(*sv_wait_mask() == sv_fault_mask(), "ppoll mask %llx", *sv_wait_mask());
	return 0;
}

/*
 * The row for any catchable signal other than SIGCHLD and the fault
 * signals: the supervisor takes no action. Each signal arrives while
 * blocked, interrupts the next wait, sets only the signal flag, and the
 * process keeps running.
 */
SV_CASE(signals_catchable_signal_takes_no_action)
{
	SV_EQ_INT(sv_install_signals(), 0);
	setup_waiter();
	for (int sig = 1; sig < SV_NSIG; sig++) {
		if (!sv_is_handled_signal(sig) || sig == SV_SIGCHLD)
			continue;
		sv_signal_flag = 0;
		sv_sigchld_flag = 0;
		SV_EQ_INT(kill(getpid(), sig), 0);
		unsigned long long t0 = sv_ms_now();
		sv_wait_until(&waiter, sv_now_ms() + 2000);
		SV_CHECK(sv_ms_now() - t0 < 1500, "signal %d interrupted the wait", sig);
		SV_CHECK(sv_signal_flag == 1, "signal %d set the flag", sig);
		SV_CHECK(sv_sigchld_flag == 0, "signal %d left the SIGCHLD flag clear", sig);
		SV_CHECK(!procs.exited_pending, "signal %d reported nothing", sig);
	}
	return 0;
}

/*
 * The row for SIGSEGV, SIGBUS, SIGFPE, and SIGILL from any sender: each
 * is blocked, sent with kill and with rt_sigqueueinfo it stays pending,
 * its default action never runs, and the process keeps running.
 */
SV_CASE(signals_sent_fault_signal_never_takes_effect)
{
	SV_EQ_INT(sv_install_signals(), 0);
	setup_waiter();
	static const int faults[] = {SV_SIGSEGV, SV_SIGBUS, SV_SIGFPE, SV_SIGILL};
	for (size_t i = 0; i < 4; i++) {
		SV_EQ_INT(kill(getpid(), faults[i]), 0);
		siginfo_t si;
		memset(&si, 0, sizeof(si));
		si.si_signo = faults[i];
		si.si_code = SI_QUEUE;
		si.si_pid = getpid();
		si.si_uid = getuid();
		SV_EQ_INT(syscall(SYS_rt_sigqueueinfo, getpid(), faults[i], &si), 0);
	}
	sv_wait_until(&waiter, sv_now_ms() + 50);
	unsigned long long pnd = sv_status_mask(getpid(), "ShdPnd") | sv_status_mask(getpid(), "SigPnd");
	SV_CHECK((pnd & sv_fault_mask()) == sv_fault_mask(), "fault signals pending %llx", pnd);
	return 0;
}

/*
 * The row for a synchronous fault in the supervisor: the kernel delivers
 * the signal through the block with its default action, and the process
 * ends.
 */
SV_CASE(signals_synchronous_fault_ends_process_through_block)
{
	pid_t pid = fork();
	if (pid == 0) {
		if (sv_install_signals() < 0)
			_exit(3);
		volatile int *volatile p = NULL;
		*p = 1;
		_exit(4);
	}
	int st = 0;
	waitpid(pid, &st, 0);
	SV_CHECK(WIFSIGNALED(st) && WTERMSIG(st) == SIGSEGV, "status %d", st);
	return 0;
}

/* A forged SIGCHLD runs the reaper, which collects nothing when no child exited. */
SV_CASE(signals_forged_sigchld_collects_nothing)
{
	SV_EQ_INT(sv_install_signals(), 0);
	setup_waiter();
	pid_t live = fork();
	if (live == 0) {
		pause();
		_exit(0);
	}
	procs.slot[SV_CHILD_RUNTIME].pid = live;
	SV_EQ_INT(kill(getpid(), SIGCHLD), 0);
	sv_wait_until(&waiter, sv_now_ms() + 1000);
	SV_CHECK(sv_sigchld_flag == 0, "the reaper cleared the flag");
	SV_CHECK(!procs.exited_pending, "no exit is reported");
	SV_EQ_INT(procs.slot[SV_CHILD_RUNTIME].pid, live);
	kill(live, SIGKILL);
	return 0;
}

static int zombie(pid_t pid)
{
	char path[64], buf[512];
	snprintf(path, sizeof(path), "/proc/%d/stat", pid);
	if (sv_read_file(path, buf, sizeof(buf)) < 0)
		return 0;
	char *rp = strrchr(buf, ')');
	return rp && rp[2] == 'Z';
}

/*
 * A SIGCHLD that arrives between the reap loop and the wait is not
 * missed: SIGCHLD is blocked outside ppoll, so the wait returns at once
 * and the reaper collects the child.
 */
SV_CASE(signals_sigchld_between_reap_loop_and_ppoll)
{
	SV_EQ_INT(sv_install_signals(), 0);
	setup_waiter();
	sv_service_children(&waiter);
	pid_t pid = fork();
	if (pid == 0)
		_exit(5);
	procs.slot[SV_CHILD_RUNTIME].pid = pid;
	procs.runtime_gen = 4;
	while (!zombie(pid))
		sv_sleep(1);
	unsigned long long t0 = sv_ms_now();
	sv_wait_until(&waiter, sv_now_ms() + 5000);
	SV_CHECK(sv_ms_now() - t0 < 2000, "the wait returned on the pending SIGCHLD");
	SV_CHECK(procs.exited_pending, "the exit was collected");
	SV_EQ_INT(procs.exited_gen, 4);
	SV_EQ_INT(SV_WEXITSTATUS(procs.exited_status), 5);
	return 0;
}

/*
 * A SIGCHLD during the reap loop runs the loop again: the flag is
 * cleared before the loop, so a child that exits after the loop's last
 * wait4 leaves the flag set for the next pass.
 */
SV_CASE(signals_sigchld_during_reap_loop_runs_it_again)
{
	SV_EQ_INT(sv_install_signals(), 0);
	setup_waiter();
	sv_sigchld_flag = 1;
	sv_service_children(&waiter);
	SV_EQ_INT(sv_sigchld_flag, 0);
	pid_t pid = fork();
	if (pid == 0)
		_exit(0);
	while (!zombie(pid))
		sv_sleep(1);
	/* The handler runs only inside ppoll, which the pending SIGCHLD interrupts at once. */
	unsigned long long t0 = sv_ms_now();
	sv_wait_until(&waiter, sv_now_ms() + 5000);
	SV_CHECK(sv_ms_now() - t0 < 2000, "the wait returned on the pending SIGCHLD");
	SV_EQ_INT(sv_sigchld_flag, 0);
	int st;
	SV_EQ_INT(waitpid(pid, &st, WNOHANG), -1);
	return 0;
}

/* The SIGPIPE row: a write to a closed connection fails rather than ending the process. */
SV_CASE(signals_sigpipe_is_a_failed_write)
{
	SV_EQ_INT(sv_install_signals(), 0);
	int sp[2];
	SV_EQ_INT(socketpair(AF_UNIX, SOCK_STREAM, 0, sp), 0);
	close(sp[1]);
	SV_EQ_INT(sv_write_all(sp[0], "x\n", 2), -SV_EPIPE);
	return 0;
}

/* A child that calls execve first empties its mask, and execve resets the handlers. */
SV_CASE(signals_child_mask_empty_before_execve)
{
	SV_EQ_INT(sv_install_signals(), 0);
	char probe[4096], report[4096];
	sv_bin_path(probe, sizeof(probe), "probe");
	snprintf(report, sizeof(report), "%s/mask-report-%d", SV_PATH_PREFIX, getpid());
	sv_mkdir_p(SV_PATH_PREFIX);
	char *argv[] = {probe, "report", report, "exit", NULL};
	char *env[] = {SV_FIXED_PATH, NULL};
	int e = 0;
	int pid = sv_launch_child(argv, env, &e);
	SV_CHECK(pid > 0 && e == 0, "launched pid %d errno %d", pid, e);
	int st = 0;
	waitpid(pid, &st, 0);
	SV_CHECK(WIFEXITED(st) && WEXITSTATUS(st) == 0, "probe status %d", st);
	char buf[8192];
	SV_CHECK(sv_read_file(report, buf, sizeof(buf)) > 0, "report written");
	SV_CHECK(strstr(buf, "SigBlk:\t0000000000000000"), "empty mask:\n%s", buf);
	SV_CHECK(strstr(buf, "SigIgn:\t0000000000000000"), "nothing ignored:\n%s", buf);
	SV_CHECK(strstr(buf, "SigCgt:\t0000000000000000"), "no handler survives execve:\n%s", buf);
	unlink(report);
	return 0;
}
