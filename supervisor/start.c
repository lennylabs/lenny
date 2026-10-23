/* SPDX-License-Identifier: MIT */
/*
 * The freestanding entry point. The kernel starts the process at _start
 * with argc, argv, and envp on the stack; no C library runs first, so the
 * binary has no program interpreter (spec: §4.7.11 item 8, Protection
 * from author code). The C unit harness links the other units beside a
 * C library and leaves this file out.
 */
#include "supervisor.h"

__attribute__((noreturn, used)) void sv_start_c(long *sp)
{
	int argc = (int)sp[0];
	char **argv = (char **)(sp + 1);
	char **envp = argv + argc + 1;
	sv_exit(sv_main(argc, argv, envp));
}

#if defined(__aarch64__)
__asm__(".text\n"
	".global _start\n"
	".type _start,%function\n"
	"_start:\n"
	"	mov x29, #0\n"
	"	mov x30, #0\n"
	"	mov x0, sp\n"
	"	and x1, x0, #-16\n"
	"	mov sp, x1\n"
	"	bl sv_start_c\n");
#else
__asm__(".text\n"
	".global _start\n"
	".type _start,@function\n"
	"_start:\n"
	"	xor %rbp, %rbp\n"
	"	mov %rsp, %rdi\n"
	"	and $-16, %rsp\n"
	"	call sv_start_c\n"
	"	hlt\n");
#endif
