import { FrameWriter } from "./transport.js";
import type { AdapterManifest, CredentialBundle } from "./types.js";
export interface LifecycleEvent {
    type: string;
    raw: Record<string, unknown>;
}
export interface LifecycleHooks {
    onCheckpoint?(checkpointId: string): Promise<void> | void;
    onInterrupt?(interruptId: string): Promise<void> | void;
    onCredentialsRotated?(creds: CredentialBundle | undefined): void;
    onDeadline?(event: LifecycleEvent): void;
}
export interface LifecycleHost {
    stdoutWriter: FrameWriter;
    reloadCredentials(): CredentialBundle | undefined;
    invokeTerminate(reason: string, deadlineMs: number): Promise<void>;
    stopFrameLoop(): void;
    log(msg: string): void;
}
export declare class Lifecycle {
    private readonly conn;
    private readonly writer;
    private readonly reader;
    private readonly hooks;
    private readonly host;
    private closed;
    private constructor();
    static dial(manifest: AdapterManifest, timeoutMs: number, hooks: LifecycleHooks, host: LifecycleHost): Promise<Lifecycle>;
    private loop;
    private handleCheckpoint;
    private handleInterrupt;
    private handleCredentialsRotated;
    private handleDeadline;
    private handleTerminate;
    send(frame: unknown): Promise<void>;
    close(): void;
}
//# sourceMappingURL=lifecycle.d.ts.map