# SPDX-License-Identifier: MIT
#
# The runtime supervisor (spec: §4.7.10 Runtime supervisor; §4.7.11 item
# 8). It is a freestanding static C program: no C library, no program
# interpreter, no dynamic section, and no NSS.
#
#   make                 builds $(OUT)/lenny-supervisor
#   make harness         builds the tier-1 C unit harness, its probe, the
#                        fuzz driver, and a supervisor copy whose platform
#                        volume paths sit under $(TEST_PREFIX)
#
# OUT defaults to ./bin. The tier-0 binary check and the tier-1 driver
# under tests/ build into a temporary OUT.

CC ?= cc
OUT ?= bin
TEST_PREFIX ?= /nonexistent-supervisor-test-prefix

SRCS := supervisor.c wire.c procs.c sweep.c selftest.c signals.c util.c
FREESTANDING := start.c mem.c
HDRS := supervisor.h syscalls.h

CFLAGS_COMMON := -std=gnu11 -Wall -Wextra -Werror -Wno-unused-parameter
CFLAGS_FREE := $(CFLAGS_COMMON) -Os -ffreestanding -fno-builtin -fno-stack-protector \
	-fno-asynchronous-unwind-tables -fno-unwind-tables -fno-pie -fno-plt \
	-ffunction-sections -fdata-sections
ifeq ($(shell uname -m),aarch64)
CFLAGS_FREE += -mno-outline-atomics
endif
LDFLAGS_FREE := -static -nostdlib -no-pie -Wl,--gc-sections -Wl,-z,noexecstack -Wl,--build-id=none

TEST_CFLAGS := $(CFLAGS_COMMON) -O1 -g -DSV_TEST

.PHONY: all harness clean
all: $(OUT)/lenny-supervisor

$(OUT):
	mkdir -p $(OUT)

$(OUT)/lenny-supervisor: $(SRCS) $(FREESTANDING) $(HDRS) | $(OUT)
	$(CC) $(CFLAGS_FREE) $(LDFLAGS_FREE) -o $@ $(SRCS) $(FREESTANDING) -lgcc

# The freestanding binary with its platform volume paths under TEST_PREFIX,
# which the namespace cases run as the init of a scratch PID namespace.
$(OUT)/lenny-supervisor-prefixed: $(SRCS) $(FREESTANDING) $(HDRS) | $(OUT)
	$(CC) $(CFLAGS_FREE) -DSV_PATH_PREFIX='"$(TEST_PREFIX)"' $(LDFLAGS_FREE) -o $@ $(SRCS) $(FREESTANDING) -lgcc

# The prefixed binary with a sweep worker that never reports and a short
# sweep backstop, which the tier-3 contract test runs to reach the
# supervisor's backstop answer through the adapter client.
$(OUT)/lenny-supervisor-sweepstall: $(SRCS) $(FREESTANDING) $(HDRS) | $(OUT)
	$(CC) $(CFLAGS_FREE) -DSV_PATH_PREFIX='"$(TEST_PREFIX)"' -DSV_TEST_SWEEP_STALL -DSV_SWEEP_BACKSTOP_MS=1500 $(LDFLAGS_FREE) -o $@ $(SRCS) $(FREESTANDING) -lgcc

# HARNESS_LDFLAGS is empty for the tier-1 driver. The supervise-probe image
# sets it to -static so the harness runs in its distroless image, where
# tier 6 runs the synchronous-fault case under each sandboxed runtime.
HARNESS_LDFLAGS ?=

$(OUT)/harness: $(SRCS) $(HDRS) test/harness.c test/harness.h test/cases_*.c | $(OUT)
	$(CC) $(TEST_CFLAGS) -DSV_PATH_PREFIX='"$(TEST_PREFIX)"' -I. $(HARNESS_LDFLAGS) -o $@ $(SRCS) test/harness.c test/cases_*.c

# The fuzz driver is built with the address and undefined-behaviour
# sanitizers where the toolchain links and runs a sanitized program, and
# with stack protection and fortified string calls where it does not.
$(OUT)/fuzz: $(SRCS) $(HDRS) test/fuzz.c | $(OUT)
	@if printf 'int main(void){return 0;}\n' | $(CC) -fsanitize=address,undefined -x c - -o $(OUT)/.sancheck 2>/dev/null && $(OUT)/.sancheck 2>/dev/null; then \
		san="-fsanitize=address,undefined -fno-sanitize-recover=all"; \
	else \
		san="-fstack-protector-strong -D_FORTIFY_SOURCE=2"; \
	fi; \
	rm -f $(OUT)/.sancheck; \
	echo "$(CC) $(TEST_CFLAGS) $$san -I. -o $@ $(SRCS) test/fuzz.c"; \
	$(CC) $(TEST_CFLAGS) $$san -I. -o $@ $(SRCS) test/fuzz.c

$(OUT)/probe: test/probe.c | $(OUT)
	$(CC) $(CFLAGS_COMMON) -O1 -o $@ test/probe.c

harness: $(OUT)/lenny-supervisor $(OUT)/lenny-supervisor-prefixed $(OUT)/harness $(OUT)/fuzz $(OUT)/probe

clean:
	rm -rf $(OUT)
