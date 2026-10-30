/* SPDX-License-Identifier: MIT */
/*
 * The tier-1 C unit harness's runner and shared helpers. See harness.h.
 */
#define _GNU_SOURCE
#include "harness.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <poll.h>
#include <sched.h>
#include <signal.h>
#include <sys/mount.h>
#include <sys/prctl.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

#define SV_MAX_CASES 512

static struct sv_case sv_cases[SV_MAX_CASES];
static int sv_ncases;

void sv_register_case(const char *name, sv_case_fn fn)
{
	if (sv_ncases >= SV_MAX_CASES) {
		fprintf(stderr, "harness: too many cases\n");
		exit(2);
	}
	sv_cases[sv_ncases].name = name;
	sv_cases[sv_ncases].fn = fn;
	sv_ncases++;
}

static int sv_case_cmp(const void *a, const void *b)
{
	return strcmp(((const struct sv_case *)a)->name, ((const struct sv_case *)b)->name);
}

/*
 * sv_reap_descendants kills and collects every process the case left
 * behind: the case's process group, and every orphan that the runner, a
 * child subreaper, inherited. It signals only its own descendants.
 */
static void sv_reap_descendants(pid_t pgid)
{
	kill(-pgid, SIGKILL);
	for (int round = 0; round < 50; round++) {
		char path[64], buf[4096];
		snprintf(path, sizeof(path), "/proc/self/task/%d/children", getpid());
		long n = sv_read_file(path, buf, sizeof(buf));
		int any = 0;
		for (char *p = buf; n > 0 && *p;) {
			char *end;
			long pid = strtol(p, &end, 10);
			if (end == p)
				break;
			kill((pid_t)pid, SIGKILL);
			any = 1;
			p = end;
		}
		while (waitpid(-1, NULL, WNOHANG) > 0)
			any = 1;
		if (!any)
			return;
		sv_sleep(10);
	}
}

/*
 * sv_run_case runs one case in a child in its own process group, with
 * the runner as a child subreaper, and cleans up whatever the case left
 * running before it reports, so no descendant outlives the harness.
 */
static int sv_run_case(const struct sv_case *c)
{
	prctl(PR_SET_CHILD_SUBREAPER, 1);
	fflush(NULL);
	pid_t pid = fork();
	if (pid < 0)
		return 1;
	if (pid == 0) {
		setpgid(0, 0);
		/* A dying supervisor peer must not end the case with SIGPIPE. */
		signal(SIGPIPE, SIG_IGN);
		setvbuf(stdout, NULL, _IONBF, 0);
		int r = c->fn();
		fflush(NULL);
		_exit(r == 0 ? 0 : (r == SV_CASE_SKIP ? SV_CASE_SKIP : 1));
	}
	setpgid(pid, pid);
	int st = 0;
	while (waitpid(pid, &st, 0) < 0 && errno == EINTR)
		;
	sv_reap_descendants(pid);
	if (WIFSIGNALED(st)) {
		fprintf(stderr, "harness: case %s ended by signal %d\n", c->name, WTERMSIG(st));
		return 1;
	}
	return WEXITSTATUS(st);
}

int main(int argc, char **argv)
{
	qsort(sv_cases, (size_t)sv_ncases, sizeof(sv_cases[0]), sv_case_cmp);
	if (argc == 2 && strcmp(argv[1], "--list") == 0) {
		for (int i = 0; i < sv_ncases; i++)
			printf("%s\n", sv_cases[i].name);
		return 0;
	}
	if (argc == 3 && strcmp(argv[1], "--run") == 0) {
		for (int i = 0; i < sv_ncases; i++) {
			if (strcmp(sv_cases[i].name, argv[2]) != 0)
				continue;
			return sv_run_case(&sv_cases[i]);
		}
		fprintf(stderr, "harness: no case named %s\n", argv[2]);
		return 2;
	}
	fprintf(stderr, "usage: harness --list | --run NAME\n");
	return 2;
}

/* ---- Files and paths ----------------------------------------------------- */

void sv_bin_path(char *out, size_t cap, const char *name)
{
	char self[PATH_MAX];
	ssize_t n = readlink("/proc/self/exe", self, sizeof(self) - 1);
	if (n < 0)
		n = 0;
	self[n] = 0;
	char *slash = strrchr(self, '/');
	if (slash)
		*slash = 0;
	snprintf(out, cap, "%s/%s", self, name);
}

void sv_prefix_path(char *out, size_t cap, const char *rel)
{
	snprintf(out, cap, "%s/%s", SV_PATH_PREFIX, rel);
}

void sv_rm_rf(const char *path)
{
	struct stat st;
	if (lstat(path, &st) < 0)
		return;
	if (S_ISDIR(st.st_mode)) {
		chmod(path, 0700);
		DIR *d = opendir(path);
		if (d) {
			struct dirent *e;
			while ((e = readdir(d))) {
				if (!strcmp(e->d_name, ".") || !strcmp(e->d_name, ".."))
					continue;
				char sub[PATH_MAX];
				snprintf(sub, sizeof(sub), "%s/%s", path, e->d_name);
				sv_rm_rf(sub);
			}
			closedir(d);
		}
		rmdir(path);
		return;
	}
	unlink(path);
}

int sv_mkdir_p(const char *path)
{
	char tmp[PATH_MAX];
	snprintf(tmp, sizeof(tmp), "%s", path);
	for (char *p = tmp + 1; *p; p++) {
		if (*p != '/')
			continue;
		*p = 0;
		if (mkdir(tmp, 0755) < 0 && errno != EEXIST)
			return -1;
		*p = '/';
	}
	if (mkdir(tmp, 0755) < 0 && errno != EEXIST)
		return -1;
	return 0;
}

int sv_write_file(const char *path, const char *s)
{
	int fd = open(path, O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0644);
	if (fd < 0)
		return -1;
	size_t n = strlen(s);
	int ok = write(fd, s, n) == (ssize_t)n;
	close(fd);
	return ok ? 0 : -1;
}

long sv_read_file(const char *path, char *out, size_t cap)
{
	int fd = open(path, O_RDONLY | O_CLOEXEC);
	if (fd < 0)
		return -1;
	size_t len = 0;
	for (;;) {
		ssize_t n = read(fd, out + len, cap - 1 - len);
		if (n <= 0)
			break;
		len += (size_t)n;
		if (len >= cap - 1)
			break;
	}
	close(fd);
	out[len] = 0;
	return (long)len;
}

int sv_path_exists(const char *path)
{
	struct stat st;
	return lstat(path, &st) == 0;
}

unsigned long long sv_status_mask(int pid, const char *field)
{
	char path[64], buf[4096];
	snprintf(path, sizeof(path), "/proc/%d/status", pid);
	if (sv_read_file(path, buf, sizeof(buf)) < 0)
		return ~0ULL;
	char *p = strstr(buf, field);
	if (!p)
		return ~0ULL;
	p += strlen(field);
	while (*p == ':' || *p == ' ' || *p == '\t')
		p++;
	return strtoull(p, NULL, 16);
}

unsigned long long sv_ms_now(void)
{
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (unsigned long long)ts.tv_sec * 1000 + (unsigned long long)ts.tv_nsec / 1000000;
}

void sv_sleep(unsigned ms)
{
	struct timespec ts = {ms / 1000, (long)(ms % 1000) * 1000000L};
	while (nanosleep(&ts, &ts) < 0 && errno == EINTR)
		;
}

int sv_wait_file_contains(const char *path, const char *needle, unsigned ms)
{
	unsigned long long end = sv_ms_now() + ms;
	char buf[1 << 15];
	for (;;) {
		if (sv_read_file(path, buf, sizeof(buf)) >= 0 && strstr(buf, needle))
			return 1;
		if (sv_ms_now() >= end)
			return 0;
		sv_sleep(10);
	}
}

/* ---- EINTR injection ----------------------------------------------------- */

int sv_eintr_injected;

/*
 * Each wrapped call alternates between an injected EINTR and a real
 * attempt, so every retry loop runs at least once per call.
 */
int sv_eintr_every_other(long nr)
{
	static unsigned char flip[1024];
	unsigned idx = (unsigned)nr & 1023u;
	flip[idx] ^= 1;
	if (flip[idx]) {
		sv_eintr_injected++;
		return 1;
	}
	return 0;
}

/* ---- Namespace runner ---------------------------------------------------- */

int sv_ns_userns_available(void)
{
	pid_t pid = fork();
	if (pid < 0)
		return 0;
	if (pid == 0)
		_exit(unshare(CLONE_NEWUSER | CLONE_NEWPID | CLONE_NEWNS | CLONE_NEWIPC) == 0 ? 0 : 1);
	int st = 0;
	waitpid(pid, &st, 0);
	return WIFEXITED(st) && WEXITSTATUS(st) == 0;
}

int sv_ns_listen(struct sv_ns *ns)
{
	memset(ns, 0, sizeof(*ns));
	ns->launcher_pid = -1;
	ns->sup_pid = -1;
	ns->conn = -1;
	snprintf(ns->sock, sizeof(ns->sock), "lenny-sv-harness-%d-%llu", getpid(), sv_ms_now());
	ns->listen_fd = socket(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0);
	if (ns->listen_fd < 0)
		return -1;
	struct sockaddr_un a;
	memset(&a, 0, sizeof(a));
	a.sun_family = AF_UNIX;
	size_t n = strlen(ns->sock);
	memcpy(a.sun_path + 1, ns->sock, n);
	if (bind(ns->listen_fd, (struct sockaddr *)&a, (socklen_t)(sizeof(a.sun_family) + 1 + n)) < 0)
		return -1;
	return listen(ns->listen_fd, 4);
}

static int sv_write_proc_file(const char *path, const char *s)
{
	int fd = open(path, O_WRONLY | O_CLOEXEC);
	if (fd < 0)
		return -1;
	size_t n = strlen(s);
	int ok = write(fd, s, n) == (ssize_t)n;
	close(fd);
	return ok ? 0 : -1;
}

/*
 * The launcher: it enters the namespaces, maps its own UID and GID to
 * themselves, and forks the namespace init, which remounts /proc and
 * execs argv. The launcher exits with the init's status (128 + signal
 * for a signalled init). Both die with their parent.
 */
__attribute__((noreturn)) static void sv_ns_launcher(char *const argv[], int pidfd_w)
{
	prctl(PR_SET_PDEATHSIG, SIGKILL);
	uid_t uid = getuid();
	gid_t gid = getgid();
	if (unshare(CLONE_NEWUSER | CLONE_NEWPID | CLONE_NEWNS | CLONE_NEWIPC) < 0)
		_exit(120);
	char map[64];
	snprintf(map, sizeof(map), "%u %u 1", (unsigned)uid, (unsigned)uid);
	if (sv_write_proc_file("/proc/self/uid_map", map) < 0)
		_exit(121);
	sv_write_proc_file("/proc/self/setgroups", "deny");
	snprintf(map, sizeof(map), "%u %u 1", (unsigned)gid, (unsigned)gid);
	if (sv_write_proc_file("/proc/self/gid_map", map) < 0)
		_exit(122);
	pid_t init = fork();
	if (init < 0)
		_exit(123);
	if (init == 0) {
		prctl(PR_SET_PDEATHSIG, SIGKILL);
		if (mount(NULL, "/", NULL, MS_REC | MS_PRIVATE, NULL) < 0)
			_exit(124);
		if (mount("proc", "/proc", "proc", MS_NOSUID | MS_NODEV | MS_NOEXEC, NULL) < 0)
			_exit(125);
		execv(argv[0], argv);
		_exit(126);
	}
	int v = (int)init;
	if (write(pidfd_w, &v, sizeof(v)) != (ssize_t)sizeof(v))
		_exit(127);
	close(pidfd_w);
	int st = 0;
	while (waitpid(init, &st, 0) < 0 && errno == EINTR)
		;
	if (WIFSIGNALED(st))
		_exit(128 + WTERMSIG(st));
	_exit(WEXITSTATUS(st));
}

int sv_ns_start(struct sv_ns *ns, char *const argv[])
{
	int fds[2];
	if (pipe2(fds, O_CLOEXEC) < 0)
		return -1;
	fflush(NULL);
	pid_t l = fork();
	if (l < 0)
		return -1;
	if (l == 0) {
		close(fds[0]);
		sv_ns_launcher(argv, fds[1]);
	}
	close(fds[1]);
	ns->launcher_pid = (int)l;
	int v = -1;
	ssize_t n = read(fds[0], &v, sizeof(v));
	close(fds[0]);
	if (n != (ssize_t)sizeof(v))
		return -1;
	ns->sup_pid = v;
	return 0;
}

int sv_ns_accept(struct sv_ns *ns, unsigned ms)
{
	struct pollfd p = {ns->listen_fd, POLLIN, 0};
	if (poll(&p, 1, (int)ms) <= 0)
		return -1;
	ns->conn = accept4(ns->listen_fd, NULL, NULL, SOCK_CLOEXEC);
	return ns->conn < 0 ? -1 : 0;
}

int sv_ns_send(struct sv_ns *ns, const char *line)
{
	char buf[1 << 16];
	size_t n = (size_t)snprintf(buf, sizeof(buf), "%s\n", line);
	return write(ns->conn, buf, n) == (ssize_t)n ? 0 : -1;
}

long sv_ns_read(struct sv_ns *ns, char *out, size_t cap, unsigned ms)
{
	unsigned long long end = sv_ms_now() + ms;
	for (;;) {
		char *nl = memchr(ns->rbuf, '\n', ns->rlen);
		if (nl) {
			size_t len = (size_t)(nl - ns->rbuf);
			size_t c = len < cap - 1 ? len : cap - 1;
			memcpy(out, ns->rbuf, c);
			out[c] = 0;
			memmove(ns->rbuf, nl + 1, ns->rlen - len - 1);
			ns->rlen -= len + 1;
			return (long)(len ? len : 1);
		}
		unsigned long long now = sv_ms_now();
		if (now >= end)
			return -1;
		struct pollfd p = {ns->conn, POLLIN, 0};
		if (poll(&p, 1, (int)(end - now)) <= 0)
			continue;
		ssize_t n = read(ns->conn, ns->rbuf + ns->rlen, sizeof(ns->rbuf) - ns->rlen);
		if (n <= 0)
			return 0;
		ns->rlen += (size_t)n;
	}
}

long sv_ns_request(struct sv_ns *ns, const char *line, char *out, size_t cap, unsigned ms)
{
	if (sv_ns_send(ns, line) < 0)
		return 0;
	return sv_ns_read(ns, out, cap, ms);
}

int sv_ns_wait(struct sv_ns *ns, unsigned ms)
{
	if (ns->launcher_pid <= 0)
		return -1;
	unsigned long long end = sv_ms_now() + ms;
	for (;;) {
		int st = 0;
		pid_t r = waitpid(ns->launcher_pid, &st, WNOHANG);
		if (r == ns->launcher_pid) {
			ns->launcher_pid = -1;
			return WIFEXITED(st) ? WEXITSTATUS(st) : 128 + WTERMSIG(st);
		}
		if (sv_ms_now() >= end)
			return -1;
		sv_sleep(5);
	}
}

void sv_ns_stop(struct sv_ns *ns)
{
	if (ns->conn >= 0)
		close(ns->conn);
	ns->conn = -1;
	if (ns->listen_fd >= 0)
		close(ns->listen_fd);
	ns->listen_fd = -1;
	if (ns->launcher_pid > 0 && sv_ns_wait(ns, 3000) < 0) {
		/* The launcher's death sends the namespace init SIGKILL. */
		kill(ns->launcher_pid, SIGKILL);
		sv_ns_wait(ns, 3000);
	}
}

char **sv_ns_run_argv(struct sv_ns *ns, const char *const *extra_flags, const char *const *runtime)
{
	static char sup[PATH_MAX];
	static char sockarg[80];
	static char *argv[128];
	int n = 0;
	sv_bin_path(sup, sizeof(sup), "lenny-supervisor-prefixed");
	snprintf(sockarg, sizeof(sockarg), "@%s", ns->sock);
	argv[n++] = sup;
	argv[n++] = "run";
	argv[n++] = "--socket";
	argv[n++] = sockarg;
	argv[n++] = "--dial-timeout";
	argv[n++] = "5s";
	for (int i = 0; extra_flags && extra_flags[i]; i++)
		argv[n++] = (char *)extra_flags[i];
	argv[n++] = "--";
	for (int i = 0; runtime && runtime[i]; i++)
		argv[n++] = (char *)runtime[i];
	argv[n] = NULL;
	return argv;
}
