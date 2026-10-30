/* SPDX-License-Identifier: MIT */
/*
 * Raw Linux system call wrappers for the runtime supervisor.
 *
 * The supervisor is a freestanding program: it links no C library, so it
 * has no program interpreter, no dynamic section, no dlopen, and no NSS
 * (spec: §4.7.11 item 8, Protection from author code). Every kernel
 * interface it uses is declared here, for aarch64 and x86_64.
 *
 * Every raw wrapper returns the kernel's value: a non-negative result, or
 * a negated errno. The sv_* retry wrappers below repeat a call that
 * returns -EINTR, so no caller handles EINTR itself. A test build defines
 * SV_TEST, which routes each retried call through sv_eintr_hook so the
 * C unit harness can inject EINTR into every wrapped call.
 */
#ifndef LENNY_SUPERVISOR_SYSCALLS_H
#define LENNY_SUPERVISOR_SYSCALLS_H

typedef unsigned long sv_size;
typedef long sv_ssize;
typedef unsigned long long sv_u64;
typedef long long sv_i64;
typedef unsigned int sv_u32;
typedef unsigned short sv_u16;
typedef unsigned char sv_u8;

#define SV_NULL ((void *)0)

/* errno values. */
#define SV_EPERM 1
#define SV_ENOENT 2
#define SV_ESRCH 3
#define SV_EINTR 4
#define SV_EAGAIN 11
#define SV_EACCES 13
#define SV_EEXIST 17
#define SV_ENOTDIR 20
#define SV_EISDIR 21
#define SV_EINVAL 22
#define SV_EPIPE 32
#define SV_ENOTEMPTY 39
#define SV_ELOOP 40
#define SV_EALREADY 114
#define SV_EINPROGRESS 115
#define SV_EISCONN 106
#define SV_ECONNREFUSED 111
#define SV_ECHILD 10
#define SV_EROFS 30

/* openat flags. O_DIRECTORY and O_NOFOLLOW differ between the two ABIs. */
#define SV_O_RDONLY 00
#define SV_O_WRONLY 01
#define SV_O_RDWR 02
#define SV_O_CREAT 0100
#define SV_O_EXCL 0200
#define SV_O_TRUNC 01000
#define SV_O_CLOEXEC 02000000
#if defined(__aarch64__)
#define SV_O_DIRECTORY 040000
#define SV_O_NOFOLLOW 0100000
#elif defined(__x86_64__)
#define SV_O_DIRECTORY 0200000
#define SV_O_NOFOLLOW 0400000
#else
#error "the runtime supervisor supports aarch64 and x86_64 only"
#endif

#define SV_AT_FDCWD (-100)
#define SV_AT_SYMLINK_NOFOLLOW 0x100
#define SV_AT_REMOVEDIR 0x200
#define SV_AT_EMPTY_PATH 0x1000

#define SV_POLLIN 0x0001
#define SV_POLLERR 0x0008
#define SV_POLLHUP 0x0010
#define SV_POLLNVAL 0x0020
#define SV_POLLRDHUP 0x2000

#define SV_AF_UNIX 1
#define SV_SOCK_STREAM 1
#define SV_SOCK_CLOEXEC 02000000
#define SV_SOL_SOCKET 1
#define SV_SO_PEERCRED 17

#define SV_PR_SET_DUMPABLE 4
#define SV_PR_GET_DUMPABLE 3
#define SV_PTRACE_SEIZE 0x4206

#define SV_IPC_RMID 0
#define SV_MSG_STAT 11
#define SV_MSG_INFO 12
#define SV_SHM_STAT 13
#define SV_SHM_INFO 14
#define SV_SEM_STAT 18
#define SV_SEM_INFO 19

#define SV_CLOCK_MONOTONIC 1
#define SV_WNOHANG 1

#define SV_SIG_BLOCK 0
#define SV_SIG_UNBLOCK 1
#define SV_SIG_SETMASK 2
#define SV_SA_RESTORER 0x04000000UL
#define SV_SIG_DFL ((void (*)(int))0)

#define SV_SIGHUP 1
#define SV_SIGINT 2
#define SV_SIGILL 4
#define SV_SIGBUS 7
#define SV_SIGFPE 8
#define SV_SIGKILL 9
#define SV_SIGUSR1 10
#define SV_SIGSEGV 11
#define SV_SIGPIPE 13
#define SV_SIGALRM 14
#define SV_SIGTERM 15
#define SV_SIGCHLD 17
#define SV_SIGCONT 18
#define SV_SIGSTOP 19
#define SV_NSIG 65

#define SV_STATX_TYPE 0x0001U
#define SV_STATX_UID 0x0008U
#define SV_STATX_INO 0x0100U
#define SV_S_IFMT 0170000
#define SV_S_IFDIR 0040000
#define SV_S_IFLNK 0120000

#define SV_DT_DIR 4
#define SV_DT_UNKNOWN 0

/* Wait status decoding, as the kernel encodes it. */
#define SV_WIFEXITED(s) (((s)&0x7f) == 0)
#define SV_WEXITSTATUS(s) (((s) >> 8) & 0xff)
#define SV_WIFSIGNALED(s) (((signed char)(((s)&0x7f) + 1) >> 1) > 0)
#define SV_WTERMSIG(s) ((s)&0x7f)

struct sv_timespec {
	sv_i64 tv_sec;
	long tv_nsec;
};

struct sv_pollfd {
	int fd;
	short events;
	short revents;
};

struct sv_ucred {
	int pid;
	sv_u32 uid;
	sv_u32 gid;
};

struct sv_sockaddr_un {
	sv_u16 sun_family;
	char sun_path[108];
};

struct sv_kernel_sigaction {
	void (*handler)(int);
	unsigned long flags;
	void (*restorer)(void);
	sv_u64 mask;
};

struct sv_statx_timestamp {
	sv_i64 tv_sec;
	sv_u32 tv_nsec;
	int reserved;
};

struct sv_statx {
	sv_u32 stx_mask;
	sv_u32 stx_blksize;
	sv_u64 stx_attributes;
	sv_u32 stx_nlink;
	sv_u32 stx_uid;
	sv_u32 stx_gid;
	sv_u16 stx_mode;
	sv_u16 spare0;
	sv_u64 stx_ino;
	sv_u64 stx_size;
	sv_u64 stx_blocks;
	sv_u64 stx_attributes_mask;
	struct sv_statx_timestamp stx_atime, stx_btime, stx_ctime, stx_mtime;
	sv_u32 stx_rdev_major, stx_rdev_minor, stx_dev_major, stx_dev_minor;
	sv_u64 spare2[14];
};

struct sv_dirent64 {
	sv_u64 d_ino;
	sv_i64 d_off;
	sv_u16 d_reclen;
	sv_u8 d_type;
	char d_name[];
};

/*
 * The leading fields of the kernel's ipc64_perm, which starts each of
 * shmid64_ds, msqid64_ds, and semid64_ds on both ABIs.
 */
struct sv_ipc64_perm {
	int key;
	sv_u32 uid;
	sv_u32 gid;
	sv_u32 cuid;
	sv_u32 cgid;
};

#if defined(__aarch64__)
#define SV_NR_read 63
#define SV_NR_write 64
#define SV_NR_openat 56
#define SV_NR_close 57
#define SV_NR_getdents64 61
#define SV_NR_statx 291
#define SV_NR_unlinkat 35
#define SV_NR_renameat2 276
#define SV_NR_fchmod 52
#define SV_NR_fsync 82
#define SV_NR_fchdir 50
#define SV_NR_exit_group 94
#define SV_NR_clone 220
#define SV_NR_execve 221
#define SV_NR_wait4 260
#define SV_NR_kill 129
#define SV_NR_getpid 172
#define SV_NR_getppid 173
#define SV_NR_getuid 174
#define SV_NR_setsid 157
#define SV_NR_prctl 167
#define SV_NR_rt_sigaction 134
#define SV_NR_rt_sigprocmask 135
#define SV_NR_rt_sigreturn 139
#define SV_NR_ppoll 73
#define SV_NR_clock_gettime 113
#define SV_NR_socket 198
#define SV_NR_connect 203
#define SV_NR_getsockopt 209
#define SV_NR_ptrace 117
#define SV_NR_shmctl 195
#define SV_NR_msgctl 187
#define SV_NR_semctl 191
#define SV_NR_pipe2 59
#define SV_NR_kill_pg SV_NR_kill
#else
#define SV_NR_read 0
#define SV_NR_write 1
#define SV_NR_openat 257
#define SV_NR_close 3
#define SV_NR_getdents64 217
#define SV_NR_statx 332
#define SV_NR_unlinkat 263
#define SV_NR_renameat2 316
#define SV_NR_fchmod 91
#define SV_NR_fsync 74
#define SV_NR_fchdir 81
#define SV_NR_exit_group 231
#define SV_NR_clone 56
#define SV_NR_execve 59
#define SV_NR_wait4 61
#define SV_NR_kill 62
#define SV_NR_getpid 39
#define SV_NR_getppid 110
#define SV_NR_getuid 102
#define SV_NR_setsid 112
#define SV_NR_prctl 157
#define SV_NR_rt_sigaction 13
#define SV_NR_rt_sigprocmask 14
#define SV_NR_rt_sigreturn 15
#define SV_NR_ppoll 271
#define SV_NR_clock_gettime 228
#define SV_NR_socket 41
#define SV_NR_connect 42
#define SV_NR_getsockopt 55
#define SV_NR_ptrace 101
#define SV_NR_shmctl 31
#define SV_NR_msgctl 71
#define SV_NR_semctl 66
#define SV_NR_pipe2 293
#endif

static inline long sv_raw6(long n, long a, long b, long c, long d, long e, long f)
{
#if defined(__aarch64__)
	register long x8 __asm__("x8") = n;
	register long x0 __asm__("x0") = a;
	register long x1 __asm__("x1") = b;
	register long x2 __asm__("x2") = c;
	register long x3 __asm__("x3") = d;
	register long x4 __asm__("x4") = e;
	register long x5 __asm__("x5") = f;
	__asm__ volatile("svc 0"
			 : "+r"(x0)
			 : "r"(x8), "r"(x1), "r"(x2), "r"(x3), "r"(x4), "r"(x5)
			 : "memory", "cc");
	return x0;
#else
	register long r10 __asm__("r10") = d;
	register long r8 __asm__("r8") = e;
	register long r9 __asm__("r9") = f;
	long ret;
	__asm__ volatile("syscall"
			 : "=a"(ret)
			 : "a"(n), "D"(a), "S"(b), "d"(c), "r"(r10), "r"(r8), "r"(r9)
			 : "rcx", "r11", "memory", "cc");
	return ret;
#endif
}

#define sv_raw0(n) sv_raw6((n), 0, 0, 0, 0, 0, 0)
#define sv_raw1(n, a) sv_raw6((n), (long)(a), 0, 0, 0, 0, 0)
#define sv_raw2(n, a, b) sv_raw6((n), (long)(a), (long)(b), 0, 0, 0, 0)
#define sv_raw3(n, a, b, c) sv_raw6((n), (long)(a), (long)(b), (long)(c), 0, 0, 0)
#define sv_raw4(n, a, b, c, d) sv_raw6((n), (long)(a), (long)(b), (long)(c), (long)(d), 0, 0)
#define sv_raw5(n, a, b, c, d, e) \
	sv_raw6((n), (long)(a), (long)(b), (long)(c), (long)(d), (long)(e), 0)

#ifdef SV_TEST
/*
 * The test build's EINTR injection point. The harness sets it; a non-zero
 * return makes the wrapper report -EINTR for that attempt without calling
 * the kernel.
 */
extern int (*sv_eintr_hook)(long nr);
#define SV_INJECT(nr) (sv_eintr_hook && sv_eintr_hook(nr))
#else
#define SV_INJECT(nr) 0
#endif

/*
 * SV_RETRY evaluates a raw call until it returns anything other than
 * -EINTR. Every call that can be interrupted goes through it.
 */
#define SV_RETRY(nr, call)                                         \
	({                                                         \
		long sv_r_;                                        \
		for (;;) {                                         \
			sv_r_ = SV_INJECT(nr) ? -SV_EINTR : (call); \
			if (sv_r_ != -SV_EINTR)                    \
				break;                             \
		}                                                  \
		sv_r_;                                             \
	})

static inline sv_ssize sv_read(int fd, void *buf, sv_size n)
{
	return SV_RETRY(SV_NR_read, sv_raw3(SV_NR_read, fd, buf, n));
}

static inline sv_ssize sv_write(int fd, const void *buf, sv_size n)
{
	return SV_RETRY(SV_NR_write, sv_raw3(SV_NR_write, fd, buf, n));
}

static inline int sv_openat(int dirfd, const char *path, int flags, int mode)
{
	return (int)SV_RETRY(SV_NR_openat, sv_raw4(SV_NR_openat, dirfd, path, flags, mode));
}

/* close is never retried: on Linux the descriptor is released even on EINTR. */
static inline int sv_close(int fd)
{
	return (int)sv_raw1(SV_NR_close, fd);
}

static inline long sv_getdents64(int fd, void *buf, sv_size n)
{
	return SV_RETRY(SV_NR_getdents64, sv_raw3(SV_NR_getdents64, fd, buf, n));
}

static inline int sv_statx(int dirfd, const char *path, int flags, sv_u32 mask, struct sv_statx *st)
{
	return (int)SV_RETRY(SV_NR_statx, sv_raw5(SV_NR_statx, dirfd, path, flags, mask, st));
}

static inline int sv_unlinkat(int dirfd, const char *path, int flags)
{
	return (int)SV_RETRY(SV_NR_unlinkat, sv_raw3(SV_NR_unlinkat, dirfd, path, flags));
}

static inline int sv_renameat(int olddir, const char *oldp, int newdir, const char *newp)
{
	return (int)SV_RETRY(SV_NR_renameat2,
			     sv_raw5(SV_NR_renameat2, olddir, oldp, newdir, newp, 0));
}

static inline int sv_fchmod(int fd, sv_u32 mode)
{
	return (int)SV_RETRY(SV_NR_fchmod, sv_raw2(SV_NR_fchmod, fd, mode));
}

static inline int sv_fsync(int fd)
{
	return (int)SV_RETRY(SV_NR_fsync, sv_raw1(SV_NR_fsync, fd));
}

static inline int sv_fchdir(int fd)
{
	return (int)SV_RETRY(SV_NR_fchdir, sv_raw1(SV_NR_fchdir, fd));
}

__attribute__((noreturn)) static inline void sv_exit(int code)
{
	for (;;)
		sv_raw1(SV_NR_exit_group, code);
}

/* sv_fork is clone with SIGCHLD and no other flag, which is fork(2). */
static inline int sv_fork(void)
{
	return (int)SV_RETRY(SV_NR_clone, sv_raw5(SV_NR_clone, SV_SIGCHLD, 0, 0, 0, 0));
}

#define SV_CLONE_NEWUSER 0x10000000
#define SV_CLONE_NEWPID 0x20000000

/* sv_clone_flags is fork(2) with the given namespace flags added. */
static inline int sv_clone_flags(unsigned long flags)
{
	return (int)SV_RETRY(SV_NR_clone, sv_raw5(SV_NR_clone, flags | SV_SIGCHLD, 0, 0, 0, 0));
}

static inline int sv_execve(const char *path, char *const argv[], char *const envp[])
{
	return (int)sv_raw3(SV_NR_execve, path, argv, envp);
}

static inline int sv_wait4(int pid, int *status, int options)
{
	return (int)SV_RETRY(SV_NR_wait4, sv_raw4(SV_NR_wait4, pid, status, options, 0));
}

static inline int sv_kill(int pid, int sig)
{
	return (int)SV_RETRY(SV_NR_kill, sv_raw2(SV_NR_kill, pid, sig));
}

static inline int sv_getpid(void) { return (int)sv_raw0(SV_NR_getpid); }
static inline int sv_getppid(void) { return (int)sv_raw0(SV_NR_getppid); }
static inline sv_u32 sv_getuid(void) { return (sv_u32)sv_raw0(SV_NR_getuid); }
static inline int sv_setsid(void) { return (int)sv_raw0(SV_NR_setsid); }

static inline int sv_prctl(int op, unsigned long a)
{
	return (int)sv_raw5(SV_NR_prctl, op, a, 0, 0, 0);
}

static inline int sv_sigaction(int sig, const struct sv_kernel_sigaction *act)
{
	return (int)sv_raw4(SV_NR_rt_sigaction, sig, act, 0, 8);
}

static inline int sv_sigprocmask(int how, const sv_u64 *set, sv_u64 *old)
{
	return (int)sv_raw4(SV_NR_rt_sigprocmask, how, set, old, 8);
}

/*
 * sv_ppoll is not retried: a signal that interrupts it is how the main
 * loop learns of the signal, so -EINTR is returned to the caller.
 */
static inline int sv_ppoll(struct sv_pollfd *fds, unsigned long n, const struct sv_timespec *ts,
			   const sv_u64 *mask)
{
	if (SV_INJECT(SV_NR_ppoll))
		return -SV_EINTR;
	return (int)sv_raw5(SV_NR_ppoll, fds, n, ts, mask, 8);
}

static inline int sv_clock_gettime(int clk, struct sv_timespec *ts)
{
	return (int)sv_raw2(SV_NR_clock_gettime, clk, ts);
}

static inline int sv_socket(int domain, int type, int proto)
{
	return (int)sv_raw3(SV_NR_socket, domain, type, proto);
}

static inline int sv_connect_raw(int fd, const void *addr, sv_u32 len)
{
	if (SV_INJECT(SV_NR_connect))
		return -SV_EINTR;
	return (int)sv_raw3(SV_NR_connect, fd, addr, len);
}

static inline int sv_getsockopt(int fd, int level, int name, void *val, sv_u32 *len)
{
	return (int)sv_raw5(SV_NR_getsockopt, fd, level, name, val, len);
}

static inline long sv_ptrace(long req, long pid)
{
	return sv_raw4(SV_NR_ptrace, req, pid, 0, 0);
}

static inline int sv_shmctl(int id, int cmd, void *buf)
{
	return (int)SV_RETRY(SV_NR_shmctl, sv_raw3(SV_NR_shmctl, id, cmd, buf));
}

static inline int sv_msgctl(int id, int cmd, void *buf)
{
	return (int)SV_RETRY(SV_NR_msgctl, sv_raw3(SV_NR_msgctl, id, cmd, buf));
}

static inline int sv_semctl(int id, int num, int cmd, void *buf)
{
	return (int)SV_RETRY(SV_NR_semctl, sv_raw4(SV_NR_semctl, id, num, cmd, buf));
}

static inline int sv_pipe2(int fds[2], int flags)
{
	return (int)sv_raw2(SV_NR_pipe2, fds, flags);
}

#endif
