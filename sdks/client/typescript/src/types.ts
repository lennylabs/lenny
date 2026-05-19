// SPDX-License-Identifier: MIT

/**
 * A §7.1 session lifecycle state. The gateway returns it as the
 * `state` field of every session envelope.
 */
export type State =
  | 'created'
  | 'ready'
  | 'running'
  | 'suspended'
  | 'completed'
  | 'cancelled'
  | 'failed'
  | 'expired';

/**
 * The body of POST /v1/sessions. `runtimeRef` is required; the
 * remaining fields are optional.
 */
export interface CreateSessionRequest {
  /** The runtime the session targets. Required. */
  runtimeRef: string;

  /** The §10.2 user the session is created for. Optional. */
  userId?: string;

  /**
   * The §14 workspace plan submitted at creation. Optional; an absent
   * plan starts the session with an empty workspace.
   */
  workspacePlan?: unknown;

  /** The §10.6 environment the session is created in. Optional. */
  environment?: string;

  /**
   * The §5.3 isolation profile the session is pinned to. Optional; the
   * gateway resolves a default when this is empty.
   */
  isolationProfile?: string;
}

/**
 * The §15.1 session envelope returned by GET, the transition
 * endpoints, and DELETE.
 */
export interface Session {
  id: string;
  tenantId: string;
  userId?: string;
  runtimeRef?: string;
  environment?: string;
  state: State;
  createdAt: string;
  updatedAt: string;

  /** Populated when state is `failed`. */
  failureClass?: string;

  /**
   * Echoes the §14 plan stored at creation. Absent when the session
   * was created without a plan.
   */
  workspacePlan?: unknown;
}

/**
 * The §7.1 sessionIsolationLevel object on the create response.
 */
export interface IsolationLevel {
  executionMode: string;
  isolationProfile: string;
  podReuse: boolean;
  scrubPolicy?: string;
  residualStateWarning: boolean;
}

/**
 * The §15.1 POST /v1/sessions response. It extends the session
 * envelope with the §7.1 uploadToken and isolation-level fields.
 */
export interface CreateSessionResult extends Session {
  /** The §7.1 single-use upload token. Treat it as a secret. */
  uploadToken: string;

  /** The §7.1 sessionIsolationLevel object. */
  sessionIsolationLevel?: IsolationLevel;
}

/**
 * Filters for GET /v1/sessions. An unset field applies no filter for
 * that dimension.
 */
export interface ListOptions {
  /** Filters by session state. */
  state?: State;

  /** Filters by runtimeRef. */
  runtime?: string;

  /**
   * The pagination cursor from a prior page's nextCursor. An unset
   * cursor requests the first page.
   */
  cursor?: string;

  /** Caps the page size. Unset leaves the gateway default in place. */
  limit?: number;
}

/**
 * One page of a GET /v1/sessions listing. The gateway returns the
 * session array; the pagination fields are populated from the §25.2
 * pagination envelope when the gateway supplies it.
 */
export interface SessionPage {
  /** The page of session envelopes. */
  sessions: Session[];

  /** The cursor for the following page. Empty when hasMore is false. */
  nextCursor: string;

  /** Whether more pages follow this one. */
  hasMore: boolean;
}
