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
  /** The §5.2 execution mode of the assigned pool: `session` or `service`. */
  executionMode: string;
  isolationProfile: string;
  podReuse: boolean;
  scrubPolicy?: string;
  residualStateWarning: boolean;

  /**
   * `platform` for session mode (the platform binds the session to a pod
   * and preserves conversation context across messages) or `none` for
   * service mode (the gateway routes each message to any ready replica
   * and maintains no conversation context between messages). spec: §7.1.
   */
  conversationContinuity: string;
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

/**
 * The §15.4 lines 1715-1723 delivery enum on an inbound MessagePayload.
 * Unknown values reject with `400 INVALID_DELIVERY_VALUE`.
 */
export type DeliveryMode = 'queued' | 'immediate';

/**
 * One §15.4 inbound MessageEnvelope on the
 * POST /v1/sessions/{id}/messages batch. spec: §15.4 lines 1672-1721.
 */
export interface MessagePayload {
  /** Optional client-supplied id; gateway stamps `msg_<random>` when absent. */
  id?: string;

  /** Message role (`user`, `assistant`, ...). The runtime interprets it. */
  role?: string;

  /** The message body delivered to the runtime. */
  content: string;

  /**
   * When set, names a pending `lenny/request_input` request the gateway
   * resolves directly (§7.2 path 1) instead of delivering to the
   * executor.
   */
  inReplyTo?: string;

  /** §15.4 closed enum: `queued` (default) or `immediate`. */
  delivery?: DeliveryMode;

  /** §5.2 concurrent-workspace slot identifier. */
  slotId?: string;
}

/**
 * The body of POST /v1/sessions/{id}/messages.
 */
export interface SendMessagesRequest {
  /** The inbound batch. At least one entry is required. */
  messages: MessagePayload[];
}

/**
 * The §15.4 lines 1725-1737 delivery_receipt envelope. spec: §7.2 line
 * 345.
 */
export interface DeliveryReceipt {
  /** Gateway-stamped or sender-supplied message id. */
  messageId: string;

  /**
   * One of `delivered` | `queued` | `dropped` | `expired` |
   * `rate_limited` | `error`.
   */
  status: string;

  /**
   * RFC 3339 timestamp the gateway accepted the message. Empty when
   * status is not `delivered`.
   */
  deliveredAt?: string;

  /**
   * Optional disposition reason for a non-`delivered` status
   * (`target_terminated`, `dlq_overflow`, ...).
   */
  reason?: string;
}

/**
 * One §8.5 MessagePart returned alongside a delivery receipt.
 */
export interface MessagePart {
  type: string;
  text?: string;
  data?: unknown;
}

/**
 * The POST /v1/sessions/{id}/messages response.
 */
export interface SendMessagesResponse {
  deliveryReceipt: DeliveryReceipt;
  output?: MessagePart[];
}

/**
 * One row of the §15.1 transcript page.
 */
export interface TranscriptEntry {
  seq: number;
  role?: string;
  content?: string;
  createdAt?: string;
  metadata?: unknown;
}

/**
 * The §15.1 GET /v1/sessions/{id}/transcript envelope.
 */
export interface TranscriptResponse {
  sessionId: string;
  entries: TranscriptEntry[];
}

/**
 * Filters for GET /v1/sessions/{id}/transcript.
 */
export interface TranscriptOptions {
  /** Returns only entries with seq > afterSeq. */
  afterSeq?: number;
  /** Caps the page size. Unset leaves the gateway default in place. */
  limit?: number;
}

/**
 * The response from a §15.1 interaction-resolution endpoint (tool-use
 * approve/deny, elicitation respond/dismiss).
 */
export interface InteractionResolution {
  /** Interaction id (tool_call_id or elicitation_id). */
  id: string;
  /** New phase (`approved`, `denied`, `responded`, `dismissed`). */
  phase: string;
  /** RFC 3339 resolution timestamp. */
  resolvedAt: string;
}
