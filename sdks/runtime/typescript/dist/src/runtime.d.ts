import type { Readable, Writable } from "node:stream";
import type { LifecycleHooks } from "./lifecycle.js";
import type { Handler } from "./types.js";
export type IntegrationLevel = "basic" | "standard" | "full";
export interface RunOptions {
    level?: IntegrationLevel;
    lifecycle?: LifecycleHooks;
    manifestPath?: string;
    credentialsPath?: string;
    socketTransport?: boolean;
    dialTimeoutMs?: number;
    input?: Readable;
    output?: Writable;
    logger?(msg: string): void;
}
export declare function run(handler: Handler, opts?: RunOptions): Promise<void>;
//# sourceMappingURL=runtime.d.ts.map