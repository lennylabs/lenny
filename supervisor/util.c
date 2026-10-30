/* SPDX-License-Identifier: MIT */
/*
 * String, number, clock, write, and environment helpers for the runtime
 * supervisor, which links no C library.
 */
#include "supervisor.h"

#ifdef SV_TEST
int (*sv_eintr_hook)(long nr);
#endif

sv_size sv_strlen(const char *s)
{
	sv_size n = 0;
	while (s[n])
		n++;
	return n;
}

int sv_streq(const char *a, const char *b)
{
	while (*a && *a == *b) {
		a++;
		b++;
	}
	return *a == *b;
}

int sv_strneq(const char *a, const char *b, sv_size n)
{
	for (sv_size i = 0; i < n; i++) {
		if (a[i] != b[i])
			return 0;
		if (!a[i])
			return 1;
	}
	return 1;
}

void sv_bytecopy(void *dst, const void *src, sv_size n)
{
	volatile unsigned char *d = dst;
	const unsigned char *s = src;
	for (sv_size i = 0; i < n; i++)
		d[i] = s[i];
}

void sv_bytezero(void *dst, sv_size n)
{
	volatile unsigned char *d = dst;
	for (sv_size i = 0; i < n; i++)
		d[i] = 0;
}

sv_size sv_fmt_u64(char *buf, sv_u64 n)
{
	char tmp[20];
	sv_size k = 0;
	do {
		tmp[k++] = (char)('0' + n % 10);
		n /= 10;
	} while (n);
	for (sv_size i = 0; i < k; i++)
		buf[i] = tmp[k - 1 - i];
	return k;
}

int sv_parse_u64(const char *s, sv_u64 *out)
{
	sv_u64 v = 0;
	if (!*s)
		return -1;
	for (; *s; s++) {
		if (*s < '0' || *s > '9')
			return -1;
		sv_u64 d = (sv_u64)(*s - '0');
		if (v > (~(sv_u64)0 - d) / 10)
			return -1;
		v = v * 10 + d;
	}
	*out = v;
	return 0;
}

int sv_parse_duration_ms(const char *s, sv_u64 *out)
{
	char digits[24];
	sv_size n = 0;
	while (s[n] >= '0' && s[n] <= '9') {
		if (n >= sizeof(digits) - 1)
			return -1;
		digits[n] = s[n];
		n++;
	}
	digits[n] = 0;
	sv_u64 v;
	if (sv_parse_u64(digits, &v))
		return -1;
	const char *unit = s + n;
	sv_u64 mul;
	if (!*unit || sv_streq(unit, "ms"))
		mul = 1;
	else if (sv_streq(unit, "s"))
		mul = 1000;
	else if (sv_streq(unit, "m"))
		mul = 60000;
	else
		return -1;
	if (v > ~(sv_u64)0 / mul)
		return -1;
	*out = v * mul;
	return 0;
}

#ifdef SV_TEST
sv_u64 (*sv_now_hook)(void);
#endif

sv_u64 sv_now_ms(void)
{
#ifdef SV_TEST
	if (sv_now_hook)
		return sv_now_hook();
#endif
	struct sv_timespec ts;
	sv_clock_gettime(SV_CLOCK_MONOTONIC, &ts);
	return (sv_u64)ts.tv_sec * 1000 + (sv_u64)ts.tv_nsec / 1000000;
}

struct sv_timespec sv_ts_until(sv_u64 now_ms, sv_u64 deadline_ms)
{
	struct sv_timespec ts = {0, 0};
	if (deadline_ms > now_ms) {
		sv_u64 rem = deadline_ms - now_ms;
		ts.tv_sec = (sv_i64)(rem / 1000);
		ts.tv_nsec = (long)((rem % 1000) * 1000000);
	}
	return ts;
}

int sv_write_all(int fd, const void *buf, sv_size n)
{
	const char *p = buf;
	while (n) {
		sv_ssize r = sv_write(fd, p, n);
		if (r < 0)
			return (int)r;
		if (r == 0)
			return -SV_EPIPE;
		p += r;
		n -= (sv_size)r;
	}
	return 0;
}

int sv_path_join(char *out, sv_size cap, const char *a, const char *b)
{
	sv_size la = sv_strlen(a), lb = sv_strlen(b);
	if (la + 1 + lb + 1 > cap)
		return -1;
	sv_bytecopy(out, a, la);
	out[la] = '/';
	sv_bytecopy(out + la + 1, b, lb);
	out[la + 1 + lb] = 0;
	return 0;
}

int sv_is_numeric(const char *s)
{
	if (!*s)
		return 0;
	for (; *s; s++)
		if (*s < '0' || *s > '9')
			return 0;
	return 1;
}

/*
 * The launch environment (spec: §4.7.10, Environment): the fixed PATH,
 * then each --env pair in order, a later pair replacing an earlier one of
 * the same name.
 */
void sv_env_init(struct sv_env *e)
{
	e->n = 0;
	e->vars[e->n++] = (char *)SV_FIXED_PATH;
	e->vars[e->n] = SV_NULL;
}

static sv_size sv_env_name_len(const char *kv)
{
	sv_size n = 0;
	while (kv[n] && kv[n] != '=')
		n++;
	return n;
}

int sv_env_valid(const char *s)
{
	sv_size n = sv_env_name_len(s);
	return n > 0 && s[n] == '=';
}

int sv_env_set(struct sv_env *e, char *kv)
{
	if (!sv_env_valid(kv))
		return -1;
	sv_size n = sv_env_name_len(kv);
	for (int i = 0; i < e->n; i++) {
		if (sv_env_name_len(e->vars[i]) == n && sv_strneq(e->vars[i], kv, n)) {
			e->vars[i] = kv;
			return 0;
		}
	}
	if (e->n >= SV_MAX_ENV)
		return -1;
	e->vars[e->n++] = kv;
	e->vars[e->n] = SV_NULL;
	return 0;
}

/*
 * sv_clearenv discards the environment the kernel placed on the initial
 * stack and zeroes its strings, so nothing of the container's environment
 * stays in the supervisor's memory (spec: §4.7.11 item 8, Protection from
 * author code).
 */
void sv_clearenv(char **envp)
{
	for (; envp && *envp; envp++) {
		sv_bytezero(*envp, sv_strlen(*envp));
		*envp = SV_NULL;
	}
}
