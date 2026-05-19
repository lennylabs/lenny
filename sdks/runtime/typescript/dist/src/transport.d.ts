import { Socket } from "node:net";
import type { Readable, Writable } from "node:stream";
export declare const MAX_FRAME_BYTES: number;
export declare class LineReader {
    private buf;
    private readonly chunks;
    private resolver;
    private done;
    private error;
    constructor(stream: Readable);
    private wake;
    next(): Promise<string | null>;
}
export declare class FrameWriter {
    private readonly stream;
    private tail;
    constructor(stream: Writable);
    write(frame: unknown): Promise<void>;
}
export declare function dialUnixSocket(name: string, timeoutMs: number): Promise<Socket>;
export declare function sleep(ms: number): Promise<void>;
//# sourceMappingURL=transport.d.ts.map