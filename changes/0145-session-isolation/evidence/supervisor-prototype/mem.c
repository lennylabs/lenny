/* SPDX-License-Identifier: MIT */
/*
 * The memory primitives the compiler may emit calls to in a freestanding
 * build (structure copies and zeroing). The C unit harness links a C
 * library, which provides them, and leaves this file out.
 */
#include "supervisor.h"

void *memset(void *dst, int c, sv_size n);
void *memcpy(void *dst, const void *src, sv_size n);
void *memmove(void *dst, const void *src, sv_size n);
int memcmp(const void *a, const void *b, sv_size n);

void *memset(void *dst, int c, sv_size n)
{
	volatile unsigned char *d = dst;
	for (sv_size i = 0; i < n; i++)
		d[i] = (unsigned char)c;
	return dst;
}

void *memcpy(void *dst, const void *src, sv_size n)
{
	volatile unsigned char *d = dst;
	const unsigned char *s = src;
	for (sv_size i = 0; i < n; i++)
		d[i] = s[i];
	return dst;
}

void *memmove(void *dst, const void *src, sv_size n)
{
	volatile unsigned char *d = dst;
	const unsigned char *s = src;
	if (d < s) {
		for (sv_size i = 0; i < n; i++)
			d[i] = s[i];
	} else {
		for (sv_size i = n; i > 0; i--)
			d[i - 1] = s[i - 1];
	}
	return dst;
}

int memcmp(const void *a, const void *b, sv_size n)
{
	const unsigned char *x = a, *y = b;
	for (sv_size i = 0; i < n; i++)
		if (x[i] != y[i])
			return x[i] < y[i] ? -1 : 1;
	return 0;
}
