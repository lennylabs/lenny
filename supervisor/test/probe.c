/* SPDX-License-Identifier: MIT */
/*
 * The tier-1 harness's probe: the program the harness has the supervisor
 * launch as a runtime process or run as a cleanup command. Each
 * subcommand records what it observed into a file the harness reads, or
 * acts on the supervisor so the harness can assert the supervisor's
 * response.
 *
 *   probe sleep                    waits until a signal ends it
 *   probe exit N                   exits with status N
 *   probe killself SIG             ends itself with SIG
 *   probe report FILE [then]       writes its signal mask, sigaction
 *                                  dispositions, open descriptors,
 *                                  session, working directory, and
 *                                  environment to FILE, then sleeps
 *                                  ("sleep") or exits 0 ("exit")
 *   probe daemon FILE              double-forks a sleeping daemon,
 *                                  writes its PID to FILE, and sleeps
 *   probe sigrecord FILE           handles every catchable signal and
 *                                  appends each one it receives to FILE
 *   probe signal-init FILE         sends PID 1 every signal (SIGKILL and
 *                                  SIGSTOP included, the fault signals
 *                                  with kill and with rt_sigqueueinfo),
 *                                  writes "done" to FILE, and sleeps
 *   probe touch FILE               creates FILE and exits 0
 *   probe setsid-sleep FILE        starts a new session, writes "ok" to
 *                                  FILE, and sleeps
 *   probe count-procs FILE         writes the number of numeric /proc
 *                                  entries to FILE and exits 0
 *   probe fork-sleep FILE          forks a child in the probe's process
 *                                  group, writes the child's PID to
 *                                  FILE, and sleeps
 */
#define _GNU_SOURCE
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <unistd.h>

extern char **environ;

static void sleep_forever(void)
{
	for (;;)
		pause();
}

static int write_file(const char *path, const char *s)
{
	int fd = open(path, O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0644);
	if (fd < 0)
		return -1;
	size_t n = strlen(s);
	int ok = write(fd, s, n) == (ssize_t)n;
	close(fd);
	return ok ? 0 : -1;
}

static void copy_status_line(FILE *out, const char *field)
{
	FILE *f = fopen("/proc/self/status", "re");
	if (!f)
		return;
	char line[512];
	size_t n = strlen(field);
	while (fgets(line, sizeof(line), f))
		if (strncmp(line, field, n) == 0 && line[n] == ':')
			fputs(line, out);
	fclose(f);
}

static int cmd_report(const char *path, const char *then)
{
	char tmp[4096];
	snprintf(tmp, sizeof(tmp), "%s.tmp", path);
	FILE *out = fopen(tmp, "we");
	if (!out)
		return 3;
	copy_status_line(out, "SigBlk");
	copy_status_line(out, "SigIgn");
	copy_status_line(out, "SigCgt");
	fprintf(out, "Pid: %d\nSid: %d\nPgid: %d\n", getpid(), getsid(0), getpgid(0));
	char cwd[4096];
	if (getcwd(cwd, sizeof(cwd)))
		fprintf(out, "Cwd: %s\n", cwd);
	/* Descriptors other than the one opendir holds. */
	DIR *d = opendir("/proc/self/fd");
	int self_fd = d ? dirfd(d) : -1;
	struct dirent *e;
	while (d && (e = readdir(d))) {
		if (e->d_name[0] == '.')
			continue;
		int fd = atoi(e->d_name);
		if (fd == self_fd || fd == fileno(out))
			continue;
		char link[64], target[4096];
		snprintf(link, sizeof(link), "/proc/self/fd/%d", fd);
		ssize_t n = readlink(link, target, sizeof(target) - 1);
		target[n < 0 ? 0 : n] = 0;
		fprintf(out, "Fd: %d %s\n", fd, target);
	}
	if (d)
		closedir(d);
	for (char **v = environ; v && *v; v++)
		fprintf(out, "Env: %s\n", *v);
	fclose(out);
	rename(tmp, path);
	if (then && strcmp(then, "exit") == 0)
		return 0;
	sleep_forever();
	return 0;
}

static int cmd_daemon(const char *path)
{
	int fds[2];
	if (pipe(fds) < 0)
		return 3;
	pid_t a = fork();
	if (a == 0) {
		pid_t b = fork();
		if (b == 0) {
			close(fds[0]);
			close(fds[1]);
			setsid();
			sleep_forever();
		}
		if (write(fds[1], &b, sizeof(b)) != (ssize_t)sizeof(b))
			_exit(1);
		_exit(0);
	}
	close(fds[1]);
	pid_t b = -1;
	if (read(fds[0], &b, sizeof(b)) != (ssize_t)sizeof(b))
		return 3;
	char buf[32];
	snprintf(buf, sizeof(buf), "%d\n", (int)b);
	char tmp[4096];
	snprintf(tmp, sizeof(tmp), "%s.tmp", path);
	write_file(tmp, buf);
	rename(tmp, path);
	sleep_forever();
	return 0;
}

static const char *sigrecord_path;

static void on_record(int sig)
{
	int fd = open(sigrecord_path, O_WRONLY | O_CREAT | O_APPEND | O_CLOEXEC, 0644);
	if (fd < 0)
		return;
	char buf[16];
	int n = snprintf(buf, sizeof(buf), "%d\n", sig);
	if (write(fd, buf, (size_t)n) < 0) {
	}
	close(fd);
}

static int cmd_sigrecord(const char *path)
{
	sigrecord_path = path;
	for (int sig = 1; sig < 65; sig++) {
		if (sig == SIGKILL || sig == SIGSTOP)
			continue;
		struct sigaction sa;
		memset(&sa, 0, sizeof(sa));
		sa.sa_handler = on_record;
		sigaction(sig, &sa, NULL);
	}
	write_file(path, "");
	char ready[4096];
	snprintf(ready, sizeof(ready), "%s.ready", path);
	write_file(ready, "ready\n");
	sleep_forever();
	return 0;
}

static int cmd_signal_init(const char *path)
{
	for (int sig = 1; sig < 65; sig++) {
		if (sig == SIGKILL || sig == SIGSTOP)
			continue; /* sent last, below */
		kill(1, sig);
		if (sig == SIGSEGV || sig == SIGBUS || sig == SIGFPE || sig == SIGILL) {
			siginfo_t si;
			memset(&si, 0, sizeof(si));
			si.si_signo = sig;
			si.si_code = SI_QUEUE;
			si.si_pid = getpid();
			si.si_uid = getuid();
			syscall(SYS_rt_sigqueueinfo, 1, sig, &si);
		}
	}
	kill(1, SIGSTOP);
	kill(1, SIGKILL);
	write_file(path, "done\n");
	sleep_forever();
	return 0;
}

static int cmd_setsid_sleep(const char *path)
{
	setsid();
	write_file(path, "ok\n");
	sleep_forever();
	return 0;
}

static int cmd_count_procs(const char *path)
{
	DIR *d = opendir("/proc");
	if (!d)
		return 3;
	int n = 0;
	struct dirent *e;
	while ((e = readdir(d))) {
		const char *s = e->d_name;
		if (!*s)
			continue;
		while (*s >= '0' && *s <= '9')
			s++;
		if (!*s)
			n++;
	}
	closedir(d);
	char buf[32];
	snprintf(buf, sizeof(buf), "%d\n", n);
	return write_file(path, buf) == 0 ? 0 : 3;
}

static int cmd_fork_sleep(const char *path)
{
	pid_t c = fork();
	if (c == 0)
		sleep_forever();
	char buf[32];
	snprintf(buf, sizeof(buf), "%d\n", (int)c);
	char tmp[4096];
	snprintf(tmp, sizeof(tmp), "%s.tmp", path);
	write_file(tmp, buf);
	rename(tmp, path);
	sleep_forever();
	return 0;
}

int main(int argc, char **argv)
{
	if (argc < 2)
		return 2;
	/*
	 * The probe ends with its parent, so a harness run killed mid-case
	 * leaves no probe behind; a child the probe forks does not inherit
	 * this, so a double-forked daemon still outlives its parent.
	 */
	prctl(PR_SET_PDEATHSIG, SIGKILL);
	const char *c = argv[1];
	if (!strcmp(c, "sleep"))
		sleep_forever();
	if (!strcmp(c, "exit") && argc == 3)
		return atoi(argv[2]);
	if (!strcmp(c, "killself") && argc == 3) {
		int sig = atoi(argv[2]);
		signal(sig, SIG_DFL);
		kill(getpid(), sig);
		sleep_forever();
	}
	if (!strcmp(c, "report") && argc >= 3)
		return cmd_report(argv[2], argc >= 4 ? argv[3] : "sleep");
	if (!strcmp(c, "daemon") && argc == 3)
		return cmd_daemon(argv[2]);
	if (!strcmp(c, "sigrecord") && argc == 3)
		return cmd_sigrecord(argv[2]);
	if (!strcmp(c, "signal-init") && argc == 3)
		return cmd_signal_init(argv[2]);
	if (!strcmp(c, "touch") && argc == 3)
		return write_file(argv[2], "touched\n") == 0 ? 0 : 3;
	if (!strcmp(c, "setsid-sleep") && argc == 3)
		return cmd_setsid_sleep(argv[2]);
	if (!strcmp(c, "count-procs") && argc == 3)
		return cmd_count_procs(argv[2]);
	if (!strcmp(c, "fork-sleep") && argc == 3)
		return cmd_fork_sleep(argv[2]);
	return 2;
}
