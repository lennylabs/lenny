/* SPDX-License-Identifier: MIT */
/*
 * Cases for the CH-SUPERVISE wire handling: the bounded line reader, the
 * strict request parser, and the reply templates.
 *
 * spec: §28.5.3 (CH-SUPERVISE, Messages, Degradation); §4.7.10 (Runtime
 * supervisor, Children); §4.7.11 item 8 (Property self-test).
 */
#include "harness.h"

static struct sv_request req;

static enum sv_parse_result parse(const char *line)
{
	return sv_parse_request(line, strlen(line), &req);
}

SV_CASE(wire_parse_each_request_type)
{
	SV_EQ_INT(parse("{\"type\":\"launch\",\"gen\":1,\"id\":7}"), SV_PARSE_OK);
	SV_EQ_INT(req.type, SV_REQ_LAUNCH);
	SV_EQ_INT(req.gen, 1);
	SV_EQ_INT(req.id, 7);
	SV_EQ_INT(parse("{\"type\":\"end\",\"gen\":2,\"id\":8,\"graceMs\":2500}"), SV_PARSE_OK);
	SV_EQ_INT(req.type, SV_REQ_END);
	SV_EQ_INT(req.grace_ms, 2500);
	SV_EQ_INT(parse("{\"type\":\"sweep\",\"gen\":2,\"id\":9}"), SV_PARSE_OK);
	SV_EQ_INT(req.type, SV_REQ_SWEEP);
	SV_EQ_INT(parse(" { \"id\" : 10 , \"type\" : \"ping\" , \"gen\" : 0 } "), SV_PARSE_OK);
	SV_EQ_INT(req.type, SV_REQ_PING);
	SV_EQ_INT(parse("{\"type\":\"cleanup\",\"gen\":3,\"id\":11,\"commands\":[[\"/bin/a\",\"x\"],[\"/bin/b\"]],"
			"\"timeoutMs\":30000,\"prevProvider\":\"anthropic\",\"prevLeaseId\":\"lease-1\"}"),
		  SV_PARSE_OK);
	SV_EQ_INT(req.type, SV_REQ_CLEANUP);
	SV_EQ_INT(req.ncommands, 2);
	SV_EQ_STR(req.commands[0][0], "/bin/a");
	SV_EQ_STR(req.commands[0][1], "x");
	SV_CHECK(req.commands[0][2] == NULL, "argv is NULL-terminated");
	SV_EQ_STR(req.commands[1][0], "/bin/b");
	SV_EQ_INT(req.timeout_ms, 30000);
	SV_EQ_STR(req.prev_provider, "anthropic");
	SV_EQ_STR(req.prev_lease_id, "lease-1");
	SV_EQ_INT(parse("{\"type\":\"cleanup\",\"gen\":3,\"id\":12,\"commands\":[],\"timeoutMs\":0,"
			"\"prevProvider\":\"\",\"prevLeaseId\":\"\"}"),
		  SV_PARSE_OK);
	SV_EQ_INT(req.ncommands, 0);
	return 0;
}

/*
 * A line whose type, gen, and id are readable but which is otherwise not
 * a valid request is malformed and answered; a line that cannot be
 * attributed to a request is unreadable.
 */
SV_CASE(wire_parse_rejections)
{
	static const struct {
		const char *line;
		enum sv_parse_result want;
	} cases[] = {
		/* Missing member. */
		{"{\"type\":\"end\",\"gen\":1,\"id\":1}", SV_PARSE_MALFORMED},
		{"{\"type\":\"cleanup\",\"gen\":1,\"id\":1,\"commands\":[],\"timeoutMs\":1,\"prevProvider\":\"\"}", SV_PARSE_MALFORMED},
		/* A member another type carries. */
		{"{\"type\":\"launch\",\"gen\":1,\"id\":1,\"graceMs\":0}", SV_PARSE_MALFORMED},
		/* Unknown key after type, gen, and id; before them it is unattributable. */
		{"{\"type\":\"ping\",\"gen\":1,\"id\":1,\"extra\":1}", SV_PARSE_MALFORMED},
		{"{\"extra\":1,\"type\":\"ping\",\"gen\":1,\"id\":1}", SV_PARSE_UNREADABLE},
		/* Duplicate key. */
		{"{\"type\":\"end\",\"gen\":1,\"id\":1,\"graceMs\":1,\"graceMs\":2}", SV_PARSE_MALFORMED},
		{"{\"type\":\"ping\",\"type\":\"ping\",\"gen\":1,\"id\":1}", SV_PARSE_UNREADABLE},
		/* Wrong value types and out-of-range numbers. */
		{"{\"type\":\"end\",\"gen\":1,\"id\":1,\"graceMs\":\"5\"}", SV_PARSE_MALFORMED},
		{"{\"type\":\"end\",\"gen\":1,\"id\":1,\"graceMs\":-1}", SV_PARSE_MALFORMED},
		{"{\"type\":\"end\",\"gen\":1,\"id\":1,\"graceMs\":1.5}", SV_PARSE_MALFORMED},
		{"{\"type\":\"end\",\"gen\":1,\"id\":1,\"graceMs\":1e3}", SV_PARSE_MALFORMED},
		{"{\"type\":\"end\",\"gen\":1,\"id\":1,\"graceMs\":01}", SV_PARSE_MALFORMED},
		{"{\"type\":\"end\",\"gen\":1,\"id\":1,\"graceMs\":9007199254740992}", SV_PARSE_MALFORMED},
		{"{\"type\":\"ping\",\"gen\":-1,\"id\":1}", SV_PARSE_UNREADABLE},
		{"{\"type\":\"ping\",\"gen\":1,\"id\":true}", SV_PARSE_UNREADABLE},
		{"{\"type\":\"reboot\",\"gen\":1,\"id\":1}", SV_PARSE_UNREADABLE},
		/* Structure. */
		{"", SV_PARSE_UNREADABLE},
		{"[]", SV_PARSE_UNREADABLE},
		{"{}", SV_PARSE_UNREADABLE},
		{"{\"type\":\"ping\",\"gen\":1,\"id\":1", SV_PARSE_MALFORMED},
		{"{\"type\":\"ping\",\"gen\":1,\"id\":1} x", SV_PARSE_MALFORMED},
		{"{\"type\":\"ping\",\"gen\":1,\"id\":1}{}", SV_PARSE_MALFORMED},
		/* Strings: NUL, lone surrogates, control characters, and unknown escapes. */
		{"{\"type\":\"cleanup\",\"gen\":1,\"id\":1,\"commands\":[[\"/a\\u0000b\"]],\"timeoutMs\":1,\"prevProvider\":\"\",\"prevLeaseId\":\"\"}", SV_PARSE_MALFORMED},
		{"{\"type\":\"cleanup\",\"gen\":1,\"id\":1,\"commands\":[[\"/a\\ud800\"]],\"timeoutMs\":1,\"prevProvider\":\"\",\"prevLeaseId\":\"\"}", SV_PARSE_MALFORMED},
		{"{\"type\":\"cleanup\",\"gen\":1,\"id\":1,\"commands\":[[\"/a\\udc00\"]],\"timeoutMs\":1,\"prevProvider\":\"\",\"prevLeaseId\":\"\"}", SV_PARSE_MALFORMED},
		{"{\"type\":\"cleanup\",\"gen\":1,\"id\":1,\"commands\":[[\"/a\tb\"]],\"timeoutMs\":1,\"prevProvider\":\"\",\"prevLeaseId\":\"\"}", SV_PARSE_MALFORMED},
		{"{\"type\":\"cleanup\",\"gen\":1,\"id\":1,\"commands\":[[\"/a\\x\"]],\"timeoutMs\":1,\"prevProvider\":\"\",\"prevLeaseId\":\"\"}", SV_PARSE_MALFORMED},
		/* argv lists: empty, an empty first element, and a non-string element. */
		{"{\"type\":\"cleanup\",\"gen\":1,\"id\":1,\"commands\":[[]],\"timeoutMs\":1,\"prevProvider\":\"\",\"prevLeaseId\":\"\"}", SV_PARSE_MALFORMED},
		{"{\"type\":\"cleanup\",\"gen\":1,\"id\":1,\"commands\":[[\"\"]],\"timeoutMs\":1,\"prevProvider\":\"\",\"prevLeaseId\":\"\"}", SV_PARSE_MALFORMED},
		{"{\"type\":\"cleanup\",\"gen\":1,\"id\":1,\"commands\":[[1]],\"timeoutMs\":1,\"prevProvider\":\"\",\"prevLeaseId\":\"\"}", SV_PARSE_MALFORMED},
		{"{\"type\":\"cleanup\",\"gen\":1,\"id\":1,\"commands\":\"/a\",\"timeoutMs\":1,\"prevProvider\":\"\",\"prevLeaseId\":\"\"}", SV_PARSE_MALFORMED},
	};
	for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); i++) {
		enum sv_parse_result got = parse(cases[i].line);
		if (got != cases[i].want)
			SV_FAIL("parse(%s) = %d, want %d", cases[i].line, got, cases[i].want);
	}
	return 0;
}

SV_CASE(wire_parse_unicode_escapes)
{
	SV_EQ_INT(parse("{\"type\":\"cleanup\",\"gen\":1,\"id\":1,\"commands\":[[\"/a\\u00e9\\ud83d\\ude00\\n\\/\"]],"
			"\"timeoutMs\":1,\"prevProvider\":\"\",\"prevLeaseId\":\"\"}"),
		  SV_PARSE_OK);
	SV_EQ_STR(req.commands[0][0], "/a\xc3\xa9\xf0\x9f\x98\x80\n/");
	return 0;
}

/* Build a cleanup line with the given argv list section. */
static char big[SV_MAX_LINE + 4096];

static void cleanup_line(const char *commands)
{
	snprintf(big, sizeof(big),
		 "{\"type\":\"cleanup\",\"gen\":1,\"id\":1,\"commands\":%s,\"timeoutMs\":1,\"prevProvider\":\"\",\"prevLeaseId\":\"\"}",
		 commands);
}

static char cmds[SV_MAX_LINE + 1024];

SV_CASE(wire_parse_argv_bounds)
{
	/* SV_MAX_COMMANDS lists parse; one more is malformed. */
	for (int extra = 0; extra <= 1; extra++) {
		size_t n = 0;
		n += (size_t)sprintf(cmds + n, "[");
		for (int i = 0; i < SV_MAX_COMMANDS + extra; i++)
			n += (size_t)sprintf(cmds + n, "%s[\"/c\"]", i ? "," : "");
		sprintf(cmds + n, "]");
		cleanup_line(cmds);
		SV_EQ_INT(parse(big), extra ? SV_PARSE_MALFORMED : SV_PARSE_OK);
	}
	/* SV_MAX_ARGS arguments parse; one more is malformed. */
	for (int extra = 0; extra <= 1; extra++) {
		size_t n = (size_t)sprintf(cmds, "[[");
		for (int i = 0; i < SV_MAX_ARGS + extra; i++)
			n += (size_t)sprintf(cmds + n, "%s\"/a\"", i ? "," : "");
		sprintf(cmds + n, "]]");
		cleanup_line(cmds);
		SV_EQ_INT(parse(big), extra ? SV_PARSE_MALFORMED : SV_PARSE_OK);
	}
	/* An argument of SV_MAX_ARG_BYTES parses; one byte more is malformed. */
	for (int extra = 0; extra <= 1; extra++) {
		size_t n = (size_t)sprintf(cmds, "[[\"/");
		for (int i = 1; i < SV_MAX_ARG_BYTES + extra; i++)
			cmds[n++] = 'a';
		sprintf(cmds + n, "\"]]");
		cleanup_line(cmds);
		SV_EQ_INT(parse(big), extra ? SV_PARSE_MALFORMED : SV_PARSE_OK);
	}
	/* The total argument bound across lists. */
	{
		size_t n = (size_t)sprintf(cmds, "[");
		int total = 0;
		for (int c = 0; c < SV_MAX_COMMANDS && total <= SV_MAX_ARGV_TOTAL; c++) {
			n += (size_t)sprintf(cmds + n, "%s[", c ? "," : "");
			for (int i = 0; i < 20; i++, total++)
				n += (size_t)sprintf(cmds + n, "%s\"/a\"", i ? "," : "");
			n += (size_t)sprintf(cmds + n, "]");
		}
		sprintf(cmds + n, "]");
		cleanup_line(cmds);
		SV_EQ_INT(parse(big), SV_PARSE_MALFORMED);
	}
	/* A line above SV_MAX_LINE is unreadable. */
	memset(big, ' ', SV_MAX_LINE + 1);
	big[0] = '{';
	SV_EQ_INT(sv_parse_request(big, SV_MAX_LINE + 1, &req), SV_PARSE_UNREADABLE);
	return 0;
}

SV_CASE(wire_line_reader_bounded)
{
	static struct sv_linebuf lb;
	const char *line;
	memset(&lb, 0, sizeof(lb));
	memcpy(lb.buf, "ab\ncd", 5);
	lb.len = 5;
	SV_EQ_INT(sv_line_take(&lb, &line), 2);
	SV_CHECK(strncmp(line, "ab", 2) == 0, "first line");
	sv_line_consume(&lb, 2);
	SV_EQ_INT(lb.len, 2);
	SV_EQ_INT(sv_line_take(&lb, &line), -1);
	/* SV_MAX_LINE bytes with no terminator are still a partial line; one more overflows. */
	memset(&lb, 0, sizeof(lb));
	memset(lb.buf, 'x', SV_MAX_LINE);
	lb.len = SV_MAX_LINE;
	SV_EQ_INT(sv_line_take(&lb, &line), -1);
	lb.buf[SV_MAX_LINE] = 'x';
	lb.len = SV_MAX_LINE + 1;
	SV_EQ_INT(sv_line_take(&lb, &line), -2);
	/* A full buffer accepts no further read. */
	SV_EQ_INT(sv_line_fill(&lb, 0), -SV_EINVAL);
	SV_CHECK(lb.overflow, "the overflow flag is set");
	return 0;
}

SV_CASE(wire_reply_templates)
{
	char out[SV_MAX_REPLY];
	sv_size n = sv_fmt_reply(out, SV_REQ_LAUNCH, 3, 9, SV_ERR_NONE);
	out[n] = 0;
	SV_EQ_STR(out, "{\"type\":\"launched\",\"gen\":3,\"id\":9}\n");
	n = sv_fmt_reply(out, SV_REQ_END, 3, 10, SV_ERR_END_DEADLINE);
	out[n] = 0;
	SV_EQ_STR(out, "{\"type\":\"ended\",\"gen\":3,\"id\":10,\"error\":\"end_deadline\"}\n");
	n = sv_fmt_reply(out, SV_REQ_SWEEP, 1, 2, SV_ERR_SWEEP_BOUND);
	out[n] = 0;
	SV_EQ_STR(out, "{\"type\":\"swept\",\"gen\":1,\"id\":2,\"error\":\"sweep_bound\"}\n");
	n = sv_fmt_reply(out, SV_REQ_CLEANUP, 1, 2, SV_ERR_CLEANUP_TIMEOUT);
	out[n] = 0;
	SV_EQ_STR(out, "{\"type\":\"cleaned\",\"gen\":1,\"id\":2,\"error\":\"cleanup_timeout\"}\n");
	n = sv_fmt_reply(out, SV_REQ_PING, 0, 1, SV_ERR_NONE);
	out[n] = 0;
	SV_EQ_STR(out, "{\"type\":\"pong\",\"gen\":0,\"id\":1}\n");
	/* The largest reply fits the template bound. */
	n = sv_fmt_reply(out, SV_REQ_CLEANUP, 9007199254740991ULL, 9007199254740991ULL, SV_ERR_GEN_MISMATCH);
	SV_CHECK(n < SV_MAX_REPLY, "reply length %lu", (unsigned long)n);
	return 0;
}

/* exited reports an exit status, or the signal for a signalled process (WIFSIGNALED first). */
SV_CASE(wire_exited_status_and_signal)
{
	char out[SV_MAX_REPLY];
	sv_size n = sv_fmt_exited(out, 2, 0);
	out[n] = 0;
	SV_EQ_STR(out, "{\"type\":\"exited\",\"gen\":2,\"status\":0}\n");
	n = sv_fmt_exited(out, 2, 3 << 8);
	out[n] = 0;
	SV_EQ_STR(out, "{\"type\":\"exited\",\"gen\":2,\"status\":3}\n");
	n = sv_fmt_exited(out, 2, SV_SIGKILL);
	out[n] = 0;
	SV_EQ_STR(out, "{\"type\":\"exited\",\"gen\":2,\"signal\":9}\n");
	return 0;
}

SV_CASE(wire_error_codes_distinct)
{
	for (int a = 1; a < SV_ERR_COUNT; a++) {
		SV_CHECK(sv_error_name((enum sv_error)a)[0] != 0, "error %d has a name", a);
		for (int b = a + 1; b < SV_ERR_COUNT; b++)
			SV_CHECK(strcmp(sv_error_name((enum sv_error)a), sv_error_name((enum sv_error)b)) != 0,
				 "errors %d and %d share a name", a, b);
	}
	SV_EQ_STR(sv_error_name(SV_ERR_COUNT), "");
	return 0;
}

SV_CASE(wire_ready_and_selftest_templates)
{
	char out[SV_MAX_REPLY];
	struct sv_properties p = {1, 0, 1, 0};
	sv_size n = sv_fmt_ready(out, &p);
	out[n] = 0;
	SV_EQ_STR(out, "{\"type\":\"ready\",\"gen\":0,\"properties\":{\"nondumpable\":\"pass\",\"killReach\":\"fail\","
		       "\"initSigkillImmune\":\"pass\",\"initSigstopImmune\":\"fail\"}}\n");
	n = sv_fmt_selftest(out, &p);
	out[n] = 0;
	SV_EQ_STR(out, "nondumpable pass\nkillReach fail\ninitSigkillImmune pass\ninitSigstopImmune fail\n");
	return 0;
}

/* The non-dumpable mark and kill reach gate; PID-namespace init immunity gates nothing. */
SV_CASE(wire_property_gate)
{
	for (int m = 0; m < 16; m++) {
		struct sv_properties p = {m & 1, (m >> 1) & 1, (m >> 2) & 1, (m >> 3) & 1};
		SV_EQ_INT(sv_properties_gate_ok(&p), (m & 1) && ((m >> 1) & 1));
	}
	return 0;
}

/* ---- Environment and argument helpers ---------------------------------- */

/* The fixed PATH first, then each pair in order; a later pair replaces an earlier one. */
SV_CASE(env_launch_order_and_replacement)
{
	struct sv_env e;
	sv_env_init(&e);
	SV_EQ_INT(e.n, 1);
	SV_EQ_STR(e.vars[0], SV_FIXED_PATH);
	char a[] = "VIRTUAL_ENV=/opt/venv";
	char b[] = "PATH=/opt/venv/bin:/usr/bin";
	char c[] = "LENNY_MANIFEST_PATH=/run/lenny/manifest.json";
	char d[] = "LENNY_MANIFEST_PATH=/forged";
	SV_EQ_INT(sv_env_set(&e, a), 0);
	SV_EQ_INT(sv_env_set(&e, b), 0);
	SV_EQ_INT(sv_env_set(&e, c), 0);
	SV_EQ_STR(e.vars[0], "PATH=/opt/venv/bin:/usr/bin");
	SV_EQ_STR(e.vars[1], "VIRTUAL_ENV=/opt/venv");
	SV_EQ_STR(e.vars[2], "LENNY_MANIFEST_PATH=/run/lenny/manifest.json");
	SV_EQ_INT(e.n, 3);
	/* A later pair of the same name replaces; the platform's pairs are rendered last. */
	SV_EQ_INT(sv_env_set(&e, d), 0);
	SV_EQ_STR(e.vars[2], "LENNY_MANIFEST_PATH=/forged");
	SV_CHECK(e.vars[e.n] == NULL, "terminated");
	char bad1[] = "=x", bad2[] = "NOEQUALS";
	SV_EQ_INT(sv_env_set(&e, bad1), -1);
	SV_EQ_INT(sv_env_set(&e, bad2), -1);
	/* The table is bounded. */
	static char names[SV_MAX_ENV + 2][16];
	sv_env_init(&e);
	int i;
	for (i = 0; i < SV_MAX_ENV + 1; i++) {
		snprintf(names[i], sizeof(names[i]), "V%d=1", i);
		if (sv_env_set(&e, names[i]) < 0)
			break;
	}
	SV_EQ_INT(i, SV_MAX_ENV - 1);
	return 0;
}

SV_CASE(env_clearenv_zeroes_strings)
{
	char a[] = "SECRET=hunter2", b[] = "HOME=/root";
	char *envp[] = {a, b, NULL};
	sv_clearenv(envp);
	SV_CHECK(envp[0] == NULL && envp[1] == NULL, "pointers cleared");
	for (size_t i = 0; i < sizeof(a) - 1; i++)
		SV_CHECK(a[i] == 0, "byte %zu of the first string is zeroed", i);
	SV_CHECK(b[0] == 0, "second string zeroed");
	return 0;
}

SV_CASE(util_numbers_and_durations)
{
	sv_u64 v;
	SV_EQ_INT(sv_parse_u64("18446744073709551615", &v), 0);
	SV_CHECK(v == ~0ULL, "max");
	SV_EQ_INT(sv_parse_u64("18446744073709551616", &v), -1);
	SV_EQ_INT(sv_parse_u64("", &v), -1);
	SV_EQ_INT(sv_parse_u64("1a", &v), -1);
	SV_EQ_INT(sv_parse_duration_ms("250", &v), 0);
	SV_EQ_INT(v, 250);
	SV_EQ_INT(sv_parse_duration_ms("250ms", &v), 0);
	SV_EQ_INT(v, 250);
	SV_EQ_INT(sv_parse_duration_ms("3s", &v), 0);
	SV_EQ_INT(v, 3000);
	SV_EQ_INT(sv_parse_duration_ms("2m", &v), 0);
	SV_EQ_INT(v, 120000);
	SV_EQ_INT(sv_parse_duration_ms("2h", &v), -1);
	SV_EQ_INT(sv_parse_duration_ms("s", &v), -1);
	char buf[32];
	buf[sv_fmt_u64(buf, 0)] = 0;
	SV_EQ_STR(buf, "0");
	SV_CHECK(sv_is_numeric("123") && !sv_is_numeric("") && !sv_is_numeric("12a"), "numeric");
	SV_EQ_INT(sv_path_join(buf, sizeof(buf), "/a", "b"), 0);
	SV_EQ_STR(buf, "/a/b");
	SV_EQ_INT(sv_path_join(buf, 4, "/a", "b"), -1);
	return 0;
}

/* sv_ts_until clamps a deadline that has passed to zero. */
SV_CASE(util_ts_until_clamps)
{
	struct sv_timespec ts = sv_ts_until(1000, 2500);
	SV_EQ_INT(ts.tv_sec, 1);
	SV_EQ_INT(ts.tv_nsec, 500000000);
	ts = sv_ts_until(2501, 2500);
	SV_EQ_INT(ts.tv_sec, 0);
	SV_EQ_INT(ts.tv_nsec, 0);
	ts = sv_ts_until(2500, 2500);
	SV_EQ_INT(ts.tv_sec, 0);
	return 0;
}

/* Run-mode argument errors are usage errors, reported before anything is dialled. */
SV_CASE(modes_run_usage_errors)
{
	static const char *const bad[][10] = {
		{"sup", "run", "--", "/bin/x", NULL},                                  /* no --socket */
		{"sup", "run", "--socket", "@s", "--", "relative", NULL},              /* argv[0] not absolute */
		{"sup", "run", "--socket", "@s", NULL},                                /* no argv */
		{"sup", "run", "--socket", "@", "--", "/bin/x", NULL},                 /* empty name */
		{"sup", "run", "--socket", "@s", "--adapter-uid", "x", "--", NULL},    /* bad uid */
		{"sup", "run", "--socket", "@s", "--dial-timeout", "5h", "--", NULL},  /* bad duration */
		{"sup", "run", "--socket", "@s", "--env", "NOEQ", "--", NULL},         /* bad env */
		{"sup", "run", "--socket", "@s", "--bogus", "1", "--", "/bin/x", NULL},/* unknown flag */
		{"sup", "bogus", NULL},
		{"sup", NULL},
	};
	for (size_t i = 0; i < sizeof(bad) / sizeof(bad[0]); i++) {
		int argc = 0;
		while (bad[i][argc])
			argc++;
		char *envp[] = {NULL};
		pid_t pid = fork();
		if (pid == 0)
			_exit(sv_main(argc, (char **)bad[i], envp));
		int st = 0;
		waitpid(pid, &st, 0);
		if (!WIFEXITED(st) || WEXITSTATUS(st) != SV_EXIT_USAGE)
			SV_FAIL("argv %zu: status %d, want usage %d", i, st, SV_EXIT_USAGE);
	}
	return 0;
}

/* Run mode outside a PID namespace's init exits before the self-test and before any kill. */
SV_CASE(modes_run_refuses_outside_namespace_init)
{
	static const char *const argv[] = {"sup", "run", "--socket", "@s", "--", "/bin/true", NULL};
	char *envp[] = {NULL};
	pid_t pid = fork();
	if (pid == 0)
		_exit(sv_main(6, (char **)argv, envp));
	int st = 0;
	waitpid(pid, &st, 0);
	SV_CHECK(WIFEXITED(st) && WEXITSTATUS(st) == SV_EXIT_NOT_INIT, "status %d", st);
	return 0;
}
