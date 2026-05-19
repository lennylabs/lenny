// SPDX-License-Identifier: MIT
// This file holds the wire and convenience types the runtime-author SDK
// surfaces. The wire-level types (OutputPart, MessageEnvelope, the
// inbound and outbound frame types) mirror the §15.4.1 adapter binary
// protocol. The convenience types (CreateRequest, Message, Reply,
// CredentialBundle, AdapterManifest, WorkspacePlan) are §15.7 wrappers
// the SDK materializes from the manifest, the credential file, and the
// stdin framing before invoking Handler methods. They introduce no new
// wire types.
// SCHEMA_VERSION is the current OutputPart and MessageEnvelope schema
// revision (§15.4.1). Producers stamp it on every emitted OutputPart.
export const SCHEMA_VERSION = 1;
// text builds a minimal text OutputPart with schemaVersion set.
export function text(s) {
    return { schemaVersion: SCHEMA_VERSION, type: "text", inline: s };
}
// textReply builds a final Reply carrying a single text part.
export function textReply(s) {
    return { parts: [text(s)], final: true };
}
// ProtocolError signals a non-recoverable inbound-format violation.
// run rejects with it; an entrypoint maps it to the §15.4
// protocol-error exit code (2).
export class ProtocolError extends Error {
    constructor(message) {
        super(`protocol error: ${message}`);
        this.name = "ProtocolError";
    }
}
// isProtocolError reports whether err is a ProtocolError. An entrypoint
// uses it to select the §15.4 protocol-error exit code.
export function isProtocolError(err) {
    return err instanceof ProtocolError;
}
//# sourceMappingURL=types.js.map