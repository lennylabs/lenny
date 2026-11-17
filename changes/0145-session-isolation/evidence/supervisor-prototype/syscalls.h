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
#define SV_EBADF 9
#define SV_EINTR 4
#define SV_EAGAIN 11
#define SV_EACCES 13
#define SV_EEXIST 17
#define SV_ENOTDIR 20
#define SV_EISDIR 21
#define SV_EINVAL 22
#define SV_EPIPE 32
#define SV_ECONNRESET 104
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

/* fd broker: openat2, descriptor passing, and the restore calls. */
#define SV_O_NONBLOCK 04000
#define SV_O_NOCTTY 0400
#define SV_O_PATH 010000000
#define SV_RESOLVE_NO_XDEV 0x01
#define SV_RESOLVE_NO_MAGICLINKS 0x02
#define SV_RESOLVE_NO_SYMLINKS 0x04
#define SV_RESOLVE_BENEATH 0x08
#define SV_ENXIO 6
#define SV_ENAMETOOLONG 36
#define SV_S_IFIFO 0010000
#define SV_SCM_RIGHTS 1
#define SV_MSG_NOSIGNAL 0x4000
#define SV_MSG_CTRUNC 0x8
#define SV_MSG_CMSG_CLOEXEC 0x40000000
/* The setup program's descriptors and file-size limit. */
#define SV_F_DUPFD_CLOEXEC 1030
#define SV_RLIMIT_FSIZE 1
#define SV_RLIMIT_CORE 4
#define SV_PR_SET_NO_NEW_PRIVS 38
#define SV_MSG_DONTWAIT 0x40
struct sv_rlimit {
	sv_u64 cur;
	sv_u64 max;
};
#define SV_IN_NONBLOCK 04000
#define SV_IN_CLOEXEC 02000000
#define SV_IN_ONLYDIR 0x01000000
/* IC's tracker mask: MODIFY ATTRIB CLOSE_WRITE MOVED_FROM MOVED_TO CREATE DELETE DELETE_SELF MOVE_SELF. */
#define SV_IN_TRACK_MASK 0x00000FCE
struct sv_open_how {
	sv_u64 flags;
	sv_u64 mode;
	sv_u64 resolve;
};
struct sv_iovec {
	void *base;
	sv_size len;
};
struct sv_msghdr {
	void *name;
	sv_u32 namelen;
	struct sv_iovec *iov;
	sv_size iovlen;
	void *control;
	sv_size controllen;
	int flags;
};
struct sv_cmsghdr {
	sv_size len;
	int level;
	int type;
};

#define SV_POLLIN 0x0001
#define SV_POLLOUT 0x0004
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
#define SV_WEXITED 4
#define SV_WNOWAIT 0x01000000
#define SV_P_ALL 0
/* siginfo_t as waitid fills it: si_pid sits at byte 16 on both ABIs. */
struct sv_siginfo {
	int signo;
	int errno_;
	int code;
	int pad;
	int pid;
	int rest[27];
};

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
#define SV_STATX_BASIC_STATS 0x07ffU
#define SV_STATX_MNT_ID 0x1000U
#define SV_EXDEV 18
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
	sv_u64 stx_mnt_id;
	sv_u64 spare2[13];
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
#define SV_NR_waitid 95
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
#define SV_NR_sendmsg 211
#define SV_NR_recvmsg 212
#define SV_NR_dup3 24
#define SV_NR_fcntl 25
#define SV_NR_prlimit64 261
#define SV_NR_mkdirat 34
#define SV_NR_symlinkat 36
#define SV_NR_linkat 37
#define SV_NR_mknodat 33
#define SV_NR_utimensat 88
#define SV_NR_inotify_init1 26
#define SV_NR_inotify_add_watch 27
#define SV_NR_umask 166
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
#define SV_NR_waitid 247
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
#define SV_NR_sendmsg 46
#define SV_NR_recvmsg 47
#define SV_NR_dup3 292
#define SV_NR_fcntl 72
#define SV_NR_prlimit64 302
#define SV_NR_mkdirat 258
#define SV_NR_symlinkat 266
#define SV_NR_linkat 265
#define SV_NR_mknodat 259
#define SV_NR_utimensat 280
#define SV_NR_inotify_init1 294
#define SV_NR_inotify_add_watch 254
#define SV_NR_umask 95
#endif
#define SV_NR_close_range 436
#define SV_NR_openat2 437

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

static inline int sv_waitid(int idtype, int id, struct sv_siginfo *si, int options)
{
	return (int)SV_RETRY(SV_NR_waitid, sv_raw5(SV_NR_waitid, idtype, id, si, options, 0));
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

static inline int sv_openat2(int dirfd, const char *path, struct sv_open_how *how)
{
	return (int)SV_RETRY(SV_NR_openat2, sv_raw4(SV_NR_openat2, dirfd, path, how, sizeof(*how)));
}

static inline sv_ssize sv_sendmsg(int fd, const struct sv_msghdr *m, int flags)
{
	return SV_RETRY(SV_NR_sendmsg, sv_raw3(SV_NR_sendmsg, fd, m, flags));
}

static inline sv_ssize sv_recvmsg(int fd, struct sv_msghdr *m, int flags)
{
	return SV_RETRY(SV_NR_recvmsg, sv_raw3(SV_NR_recvmsg, fd, m, flags));
}

static inline int sv_dup3(int o, int n, int flags)
{
	return (int)sv_raw3(SV_NR_dup3, o, n, flags);
}

static inline int sv_fcntl(int fd, int cmd, long arg)
{
	return (int)sv_raw3(SV_NR_fcntl, fd, cmd, arg);
}

/* sv_close_range closes every descriptor from first to last, with no flag. */
static inline int sv_close_range(sv_u32 first, sv_u32 last)
{
	return (int)sv_raw3(SV_NR_close_range, first, last, 0);
}

/* sv_set_rlimit sets both the soft and the hard limit, so the process cannot raise it. */
static inline int sv_set_rlimit(int resource, sv_u64 value)
{
	struct sv_rlimit r = {value, value};
	return (int)sv_raw4(SV_NR_prlimit64, 0, resource, &r, 0);
}

static inline int sv_mkdirat(int d, const char *p, sv_u32 mode)
{
	return (int)SV_RETRY(SV_NR_mkdirat, sv_raw3(SV_NR_mkdirat, d, p, mode));
}

static inline int sv_symlinkat(const char *target, int d, const char *p)
{
	return (int)SV_RETRY(SV_NR_symlinkat, sv_raw3(SV_NR_symlinkat, target, d, p));
}

static inline int sv_linkat(int od, const char *op, int nd, const char *np)
{
	return (int)SV_RETRY(SV_NR_linkat, sv_raw5(SV_NR_linkat, od, op, nd, np, 0));
}

static inline int sv_mknodat(int d, const char *p, sv_u32 mode)
{
	return (int)SV_RETRY(SV_NR_mknodat, sv_raw4(SV_NR_mknodat, d, p, mode, 0));
}

static inline int sv_utimensat(int d, const char *p, const struct sv_timespec ts[2], int flags)
{
	return (int)SV_RETRY(SV_NR_utimensat, sv_raw4(SV_NR_utimensat, d, p, ts, flags));
}

static inline int sv_inotify_init1(int flags)
{
	return (int)sv_raw1(SV_NR_inotify_init1, flags);
}

static inline int sv_inotify_add_watch(int fd, const char *p, sv_u32 mask)
{
	return (int)SV_RETRY(SV_NR_inotify_add_watch, sv_raw3(SV_NR_inotify_add_watch, fd, p, mask));
}

static inline sv_u32 sv_umask(sv_u32 m)
{
	return (sv_u32)sv_raw1(SV_NR_umask, m);
}

#endif
