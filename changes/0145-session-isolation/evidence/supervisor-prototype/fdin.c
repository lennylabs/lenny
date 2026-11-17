/* SPDX-License-Identifier: MIT */
/*
 * Inbound descriptors on CH-SUPERVISE. The adapter attaches descriptors
 * to the first byte of a setup request line. On a stream socket one
 * recvmsg can return the bytes of earlier lines together with the chunk
 * that carries descriptors, and the kernel ends that read after the
 * chunk. Each batch is therefore recorded with the stream offset of the
 * last byte of the read that carried it, and belongs to the line that
 * contains that byte. A line takes the batches recorded at or before its
 * terminator; a later batch waits for its own line.
 *
 * The reader queues descriptors with close-on-exec set. Dispatch hands
 * exactly the request's nfds to the setup program and closes the
 * descriptors of every other line. The supervisor keeps only the cancel
 * pipe's read end, to watch it, until the run ends.
 */
#include "supervisor.h"

/* Test builds shrink the control buffer to reach MSG_CTRUNC. */
#ifndef SV_FDIN_MAX_FDS
#define SV_FDIN_MAX_FDS SV_FS_MAX
#endif

static int sv_q[SV_FS_MAX]; /* queued descriptors, in arrival order */
static int sv_nq;
static struct {
	sv_u64 end; /* stream offset of the last byte of the read that carried the batch */
	int n;
	int bad;    /* descriptors were lost: truncated or over the queue bound */
} sv_b[SV_FS_MAX];
static int sv_nb;
static sv_u64 sv_rx; /* stream bytes received */
static int sv_sel_b, sv_sel_n, sv_sel_bad; /* the current line's batches */

long sv_fdin_fill(struct sv_linebuf *lb, int fd)
{
	sv_size room = sizeof(lb->buf) - lb->len;
	if (room == 0) {
		lb->overflow = 1;
		return -SV_EINVAL;
	}
	static char cbuf[sizeof(struct sv_cmsghdr) + SV_FDIN_MAX_FDS * sizeof(int) + 8] __attribute__((aligned(8)));
	struct sv_iovec iov = {lb->buf + lb->len, room};
	struct sv_msghdr m = {SV_NULL, 0, &iov, 1, cbuf, sizeof(cbuf), 0};
	sv_ssize r = sv_recvmsg(fd, &m, SV_MSG_CMSG_CLOEXEC);
	if (r <= 0)
		return r;
	int got = 0, bad = (m.flags & SV_MSG_CTRUNC) != 0;
	sv_size off = 0;
	while (m.controllen && off + sizeof(struct sv_cmsghdr) <= m.controllen) {
		struct sv_cmsghdr *c = (struct sv_cmsghdr *)(cbuf + off);
		if (c->len < sizeof(*c) || off + c->len > m.controllen)
			break;
		if (c->level == SV_SOL_SOCKET && c->type == SV_SCM_RIGHTS) {
			int n = (int)((c->len - sizeof(*c)) / sizeof(int));
			int *v = (int *)(cbuf + off + sizeof(*c));
			for (int i = 0; i < n; i++) {
				if (sv_nq < SV_FS_MAX) {
					sv_q[sv_nq++] = v[i];
					got++;
				} else {
					sv_close(v[i]);
					bad = 1;
				}
			}
		}
		off += (c->len + 7) & ~(sv_size)7;
	}
	if (got || bad) {
		/* More batches than descriptors the queue holds: the stream cannot be attributed. */
		if (sv_nb == SV_FS_MAX)
			return -SV_EINVAL;
		sv_b[sv_nb].end = sv_rx + (sv_u64)r - 1;
		sv_b[sv_nb].n = got;
		sv_b[sv_nb].bad = bad;
		sv_nb++;
	}
	sv_rx += (sv_u64)r;
	lb->len += (sv_size)r;
	return r;
}

void sv_fdin_select(const struct sv_linebuf *lb, sv_size n)
{
	sv_u64 line_end = sv_rx - lb->len + n; /* the terminator's stream offset */
	sv_sel_b = sv_sel_n = sv_sel_bad = 0;
	while (sv_sel_b < sv_nb && sv_b[sv_sel_b].end <= line_end) {
		sv_sel_n += sv_b[sv_sel_b].n;
		sv_sel_bad |= sv_b[sv_sel_b].bad;
		sv_sel_b++;
	}
}

int sv_fdin_count(void)
{
	return sv_sel_bad ? -1 : sv_sel_n;
}

/* sv_fdin_release removes the current line's batches from the queue. */
static void sv_fdin_release(void)
{
	for (int i = sv_sel_n; i < sv_nq; i++)
		sv_q[i - sv_sel_n] = sv_q[i];
	sv_nq -= sv_sel_n;
	for (int i = sv_sel_b; i < sv_nb; i++)
		sv_b[i - sv_sel_b] = sv_b[i];
	sv_nb -= sv_sel_b;
	sv_sel_b = sv_sel_n = sv_sel_bad = 0;
}

void sv_fdin_take(int *out, int n)
{
	for (int i = 0; i < n && i < sv_sel_n; i++)
		out[i] = sv_q[i];
	sv_fdin_release();
}

void sv_fdin_drop(void)
{
	for (int i = 0; i < sv_sel_n; i++)
		sv_close(sv_q[i]);
	sv_fdin_release();
}

int sv_fdin_install(const int *fds, int n, int *errfd)
{
	int null = sv_openat(SV_AT_FDCWD, "/dev/null", SV_O_RDWR | SV_O_CLOEXEC, 0);
	if (null < 0)
		return null;
	/*
	 * Target i's source: /dev/null, the two log pipes, then passed index i
	 * from 3 up. Passed index 2, the cancel pipe, has no target. Target n is
	 * the spawn status pipe, close-on-exec. Every source is copied above
	 * 0..n first, so placing one never overwrites another.
	 */
	int src[SV_FS_MAX + 1];
	for (int i = 0; i <= n; i++) {
		int from = i == n ? *errfd : i == 0 ? null : i < 3 ? fds[i - 1] : fds[i];
		src[i] = sv_fcntl(from, SV_F_DUPFD_CLOEXEC, n + 1);
		if (src[i] < 0)
			return src[i];
	}
	/* The status pipe first: from here on it is n, which no placement touches. */
	if (sv_dup3(src[n], n, SV_O_CLOEXEC) < 0)
		return -SV_EBADF;
	*errfd = n;
	for (int i = 0; i < n; i++)
		if (sv_dup3(src[i], i, 0) < 0)
			return -SV_EBADF;
	/* Everything above goes: the copies, the cancel pipe, the connection, the broker roots, and the rest. */
	return sv_close_range((sv_u32)n + 1, ~0U);
}
