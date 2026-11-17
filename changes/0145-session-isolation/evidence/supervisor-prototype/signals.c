/* SPDX-License-Identifier: MIT */
/*
 * Signal dispositions of the runtime supervisor.
 *
 * spec: §4.7.10 (Runtime supervisor, Signals). A handler that only sets a
 * flag is installed for every signal from 1 to _NSIG - 1 other than
 * SIGKILL, SIGSTOP, and the four fault signals. The fault signals keep
 * SIG_DFL and stay blocked, so a sent one never takes effect while the
 * kernel still delivers a synchronous fault through the block. No handler
 * uses SA_SIGINFO, and no code path reads a signal's sender.
 */
#include "supervisor.h"

volatile int sv_sigchld_flag;
volatile int sv_signal_flag;

static sv_u64 sv_wait_mask_value;

static sv_u64 sv_bit(int sig)
{
	return (sv_u64)1 << (sig - 1);
}

int sv_is_fault_signal(int sig)
{
	return sig == SV_SIGSEGV || sig == SV_SIGBUS || sig == SV_SIGFPE || sig == SV_SIGILL;
}

int sv_is_handled_signal(int sig)
{
	if (sig < 1 || sig >= SV_NSIG)
		return 0;
	return sig != SV_SIGKILL && sig != SV_SIGSTOP && !sv_is_fault_signal(sig);
}

sv_u64 sv_handled_mask(void)
{
	sv_u64 m = 0;
	for (int sig = 1; sig < SV_NSIG; sig++)
		if (sv_is_handled_signal(sig))
			m |= sv_bit(sig);
	return m;
}

sv_u64 sv_fault_mask(void)
{
	return sv_bit(SV_SIGSEGV) | sv_bit(SV_SIGBUS) | sv_bit(SV_SIGFPE) | sv_bit(SV_SIGILL);
}

/* The only action of every handler: record that the signal arrived. */
static void sv_on_signal(int sig)
{
	if (sig == SV_SIGCHLD)
		sv_sigchld_flag = 1;
	else
		sv_signal_flag = 1;
}

/*
 * The kernel returns from a handler through the restorer. The supervisor
 * supplies its own, because it links no C library that would.
 */
void sv_sigreturn_trampoline(void);
#if defined(__aarch64__)
__asm__(".text\n"
	".global sv_sigreturn_trampoline\n"
	".type sv_sigreturn_trampoline,%function\n"
	"sv_sigreturn_trampoline:\n"
	"	mov x8, #139\n"
	"	svc 0\n");
#else
__asm__(".text\n"
	".global sv_sigreturn_trampoline\n"
	".type sv_sigreturn_trampoline,@function\n"
	"sv_sigreturn_trampoline:\n"
	"	mov $15, %rax\n"
	"	syscall\n");
#endif

int sv_install_signals(void)
{
	sv_u64 block = sv_handled_mask() | sv_fault_mask();
	/* Block first, so no signal is handled before the dispositions are complete. */
	int r = sv_sigprocmask(SV_SIG_SETMASK, &block, SV_NULL);
	if (r < 0)
		return r;
	for (int sig = 1; sig < SV_NSIG; sig++) {
		struct sv_kernel_sigaction act;
		sv_bytezero(&act, sizeof(act));
		act.flags = SV_SA_RESTORER;
		act.restorer = sv_sigreturn_trampoline;
		if (sv_is_handled_signal(sig)) {
			act.handler = sv_on_signal;
			/* Every handled signal stays blocked while a handler runs. */
			act.mask = sv_handled_mask();
		} else if (sv_is_fault_signal(sig)) {
			act.handler = SV_SIG_DFL;
		} else {
			continue; /* SIGKILL and SIGSTOP cannot be caught */
		}
		r = sv_sigaction(sig, &act);
		if (r < 0)
			return r;
	}
	/* ppoll unblocks the handled signals and nothing else. */
	sv_wait_mask_value = sv_fault_mask();
	return 0;
}

const sv_u64 *sv_wait_mask(void)
{
	return &sv_wait_mask_value;
}

/*
 * A child about to call execve empties its signal mask, and execve resets
 * every caught signal to SIG_DFL, so the launched process and each
 * cleanup command start with the default dispositions and no signal
 * blocked.
 */
void sv_child_reset_signals(void)
{
	sv_u64 none = 0;
	sv_sigprocmask(SV_SIG_SETMASK, &none, SV_NULL);
}
