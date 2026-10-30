// SPDX-License-Identifier: MIT

/**
 * The language-native helper the tier-3 SDK contract harness
 * (tests/tier3_contract/sdks/harness) spawns for the TypeScript client
 * SDK. It reads one Command JSON object per line on stdin, executes
 * each op through the SDK against the gateway, and writes one Response
 * JSON object per line on stdout.
 *
 * The gateway base URL is read from the LENNY_GATEWAY_URL environment
 * variable, set by the harness when it spawns the helper. An `init` op
 * may also carry the URL in its args for harnesses that pass the URL
 * after spawn rather than via the environment.
 *
 * The protocol matches harness.Command / harness.Response: a request
 * is `{"op": "...", "args": {...}}` and a reply is
 * `{"ok": true, "result": {...}}` or
 * `{"ok": false, "error": "...", "code": "..."}`.
 */

import { createInterface } from 'node:readline';

import { asApiError, type ApiError } from './errors.js';
import { Client, newClient } from './client.js';
import { verifierWithSecret, WebhookError } from './webhook.js';
import type { CreateSessionRequest, Session } from './types.js';

/** One harness request. It mirrors harness.Command. */
interface Command {
  op: string;
  args?: Record<string, unknown>;
}

/** One helper reply. It mirrors harness.Response. */
interface Response {
  ok: boolean;
  error?: string;
  code?: string;
  result?: Record<string, unknown>;
}

/** stringArg returns args[key] as a string, or '' when absent. */
function stringArg(args: Record<string, unknown> | undefined, key: string): string {
  if (!args) {
    return '';
  }
  const v = args[key];
  return typeof v === 'string' ? v : '';
}

/** envOrDefault returns the environment variable, or def when unset. */
function envOrDefault(key: string, def: string): string {
  const v = process.env[key];
  return v !== undefined && v !== '' ? v : def;
}

/**
 * sessionResult flattens a session envelope into the harness result
 * map, merging any extra fields.
 */
function sessionResult(s: Session, extra?: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {
    id: s.id,
    state: s.state,
    runtime: s.runtimeRef ?? '',
  };
  if (s.tenantId) {
    out.tenant_id = s.tenantId;
  }
  if (s.userId) {
    out.user_id = s.userId;
  }
  if (extra) {
    Object.assign(out, extra);
  }
  return out;
}

/**
 * errResponse converts an SDK error into a failure response. A typed
 * ApiError contributes its §15.1 code; a WebhookError contributes the
 * SIGNATURE_INVALID code; any other error is reported with no code.
 */
function errResponse(err: unknown): Response {
  const apiErr: ApiError | undefined = asApiError(err);
  if (apiErr) {
    return { ok: false, error: apiErr.message, code: apiErr.code };
  }
  if (err instanceof WebhookError) {
    return { ok: false, error: err.message, code: 'SIGNATURE_INVALID' };
  }
  return { ok: false, error: err instanceof Error ? err.message : String(err) };
}

/** Helper holds the SDK client and the harness session state. */
class Helper {
  private gatewayUrl: string;
  private tenantId: string;
  private client?: Client;

  constructor() {
    this.gatewayUrl = process.env.LENNY_GATEWAY_URL ?? '';
    this.tenantId = envOrDefault('LENNY_TENANT_ID', 'acme');
  }

  /** connect builds the SDK client against the configured gateway URL. */
  connect(): void {
    if (!this.gatewayUrl) {
      throw new Error('LENNY_GATEWAY_URL is not set');
    }
    this.client = newClient(this.gatewayUrl, { tenantId: this.tenantId });
  }

  /** dispatch routes one command to its op handler. */
  async dispatch(cmd: Command): Promise<Response> {
    if (cmd.op === 'init') {
      return this.opInit(cmd.args);
    }
    if (cmd.op === 'verify_webhook') {
      return this.opVerifyWebhook(cmd.args);
    }

    // Every remaining op needs a live client.
    if (!this.client) {
      try {
        this.connect();
      } catch (err) {
        return { ok: false, error: err instanceof Error ? err.message : String(err) };
      }
    }

    switch (cmd.op) {
      case 'create_session':
        return this.opCreateSession(cmd.args);
      case 'get_session':
        return this.opGetSession(cmd.args);
      case 'list_sessions':
        return this.opListSessions(cmd.args);
      case 'delete_session':
        return this.opDeleteSession(cmd.args);
      case 'finalize':
        return this.opTransition(cmd.args, (c, id) => c.finalize(id));
      case 'start':
        return this.opTransition(cmd.args, (c, id) => c.start(id));
      case 'interrupt':
        return this.opTransition(cmd.args, (c, id) => c.interrupt(id));
      case 'terminate':
        return this.opTransition(cmd.args, (c, id) => c.terminate(id));
      default:
        return { ok: false, error: `unknown op: ${cmd.op}` };
    }
  }

  /**
   * opInit (re)points the client at a gateway URL supplied in the
   * command args. It is the post-spawn URL-injection path.
   */
  private opInit(args: Record<string, unknown> | undefined): Response {
    const url = stringArg(args, 'gateway_url');
    if (url) {
      this.gatewayUrl = url;
    }
    const tenant = stringArg(args, 'tenant_id');
    if (tenant) {
      this.tenantId = tenant;
    }
    try {
      this.connect();
    } catch (err) {
      return { ok: false, error: err instanceof Error ? err.message : String(err) };
    }
    return { ok: true, result: { gateway_url: this.gatewayUrl } };
  }

  /**
   * opCreateSession executes the create_session op. It honors an
   * optional idempotency_key arg.
   */
  private async opCreateSession(args: Record<string, unknown> | undefined): Promise<Response> {
    const req: CreateSessionRequest = {
      runtimeRef: stringArg(args, 'runtime'),
    };
    const userId = stringArg(args, 'user_id');
    if (userId) {
      req.userId = userId;
    }
    const key = stringArg(args, 'idempotency_key');
    try {
      const res = await this.client!.createSession(
        req,
        key ? { idempotencyKey: key } : {},
      );
      return { ok: true, result: sessionResult(res, { upload_token: res.uploadToken }) };
    } catch (err) {
      return errResponse(err);
    }
  }

  /** opGetSession executes the get_session op. */
  private async opGetSession(args: Record<string, unknown> | undefined): Promise<Response> {
    try {
      const s = await this.client!.getSession(stringArg(args, 'id'));
      return { ok: true, result: sessionResult(s) };
    } catch (err) {
      return errResponse(err);
    }
  }

  /**
   * opListSessions executes the list_sessions op. It honors an
   * optional state filter.
   */
  private async opListSessions(args: Record<string, unknown> | undefined): Promise<Response> {
    try {
      const state = stringArg(args, 'state');
      const page = await this.client!.listSessions(
        state ? { state: state as Session['state'] } : {},
      );
      return {
        ok: true,
        result: {
          count: page.sessions.length,
          ids: page.sessions.map((s) => s.id),
          has_more: page.hasMore,
        },
      };
    } catch (err) {
      return errResponse(err);
    }
  }

  /** opDeleteSession executes the delete_session op. */
  private async opDeleteSession(args: Record<string, unknown> | undefined): Promise<Response> {
    try {
      const s = await this.client!.deleteSession(stringArg(args, 'id'));
      return { ok: true, result: sessionResult(s) };
    } catch (err) {
      return errResponse(err);
    }
  }

  /**
   * opTransition executes a no-body lifecycle op (finalize, start,
   * interrupt, terminate).
   */
  private async opTransition(
    args: Record<string, unknown> | undefined,
    fn: (c: Client, id: string) => Promise<Session>,
  ): Promise<Response> {
    try {
      const s = await fn(this.client!, stringArg(args, 'id'));
      return { ok: true, result: sessionResult(s) };
    } catch (err) {
      return errResponse(err);
    }
  }

  /**
   * opVerifyWebhook executes the verify_webhook op. It does not need a
   * gateway: it exercises the SDK webhook signature verifier directly
   * against the supplied signature, body, and secret.
   */
  private opVerifyWebhook(args: Record<string, unknown> | undefined): Response {
    const secret = stringArg(args, 'secret');
    const signature = stringArg(args, 'signature');
    const body = stringArg(args, 'body');
    try {
      const v = verifierWithSecret(secret);
      v.verify(body, signature);
      return { ok: true, result: { verified: true } };
    } catch (err) {
      if (err instanceof WebhookError) {
        return { ok: false, error: err.message, code: 'SIGNATURE_INVALID' };
      }
      return { ok: false, error: err instanceof Error ? err.message : String(err) };
    }
  }
}

/** writeResponse writes resp as one stdout line. */
function writeResponse(resp: Response): void {
  let line: string;
  try {
    line = JSON.stringify(resp);
  } catch (err) {
    line = JSON.stringify({
      ok: false,
      error: `marshal response: ${err instanceof Error ? err.message : String(err)}`,
    });
  }
  process.stdout.write(line + '\n');
}

/**
 * main reads one command per stdin line, dispatches it, and writes one
 * response per line. A `shutdown` op replies ok:true and exits.
 */
function main(): void {
  const helper = new Helper();
  try {
    helper.connect();
  } catch (err) {
    // A missing gateway URL is reported on the first op rather than
    // aborting at startup, so the harness can still issue a shutdown
    // cleanly.
    process.stderr.write(
      `test-helper: deferred client init: ${err instanceof Error ? err.message : String(err)}\n`,
    );
  }

  const rl = createInterface({ input: process.stdin });
  // Process lines strictly in order: each command is awaited before the
  // next is dequeued, so two responses never interleave on stdout.
  let chain: Promise<void> = Promise.resolve();
  rl.on('line', (line: string) => {
    chain = chain.then(async () => {
      if (line.length === 0) {
        return;
      }
      let cmd: Command;
      try {
        cmd = JSON.parse(line) as Command;
      } catch (err) {
        writeResponse({
          ok: false,
          error: `malformed command: ${err instanceof Error ? err.message : String(err)}`,
        });
        return;
      }
      if (cmd.op === 'shutdown') {
        writeResponse({ ok: true });
        rl.close();
        // Allow the buffered stdout line to flush before exit.
        process.exitCode = 0;
        process.stdout.once('drain', () => process.exit(0));
        if (process.stdout.writableLength === 0) {
          process.exit(0);
        }
        return;
      }
      writeResponse(await helper.dispatch(cmd));
    });
  });
}

main();
