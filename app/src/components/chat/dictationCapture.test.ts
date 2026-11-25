import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MicCapture } from "./dictationCapture";
import { DICTATION_CHUNK_SAMPLES, DICTATION_SAMPLE_RATE, PCM_WORKLET_NAME } from "./dictationPcm";

/** Stand-ins for the Web Audio and media APIs the capture opens. */
function audioEnv(opts: { state?: string; closeFails?: boolean; addModule?: () => Promise<void> } = {}) {
  const track = { stop: vi.fn() };
  const stream = { getTracks: () => [track] };
  const getUserMedia = vi.fn(async (_c: unknown) => stream);
  const contexts: FakeContext[] = [];
  const nodes: FakeNode[] = [];
  const connect = vi.fn();

  class FakeContext {
    state = opts.state ?? "running";
    addModule = vi.fn((_url: string) => (opts.addModule ? opts.addModule() : Promise.resolve()));
    audioWorklet = { addModule: this.addModule };
    resume = vi.fn(async () => {});
    close = vi.fn(() => (opts.closeFails ? Promise.reject(new Error("closed already")) : Promise.resolve()));
    createMediaStreamSource = vi.fn((_s: unknown) => ({ connect }));
    constructor() {
      contexts.push(this);
    }
  }

  class FakeNode {
    port = { onmessage: null as ((e: { data: { type: string; buf: ArrayBuffer | null } }) => void) | null, postMessage: vi.fn(), close: vi.fn() };
    disconnect = vi.fn();
    constructor(
      readonly ctx: unknown,
      readonly name: string,
      readonly options: unknown,
    ) {
      nodes.push(this);
    }
    emit(type: string, buf: ArrayBuffer | null) {
      this.port.onmessage?.({ data: { type, buf } });
    }
  }

  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } });
  vi.stubGlobal("AudioContext", FakeContext);
  vi.stubGlobal("AudioWorkletNode", FakeNode);
  const createObjectURL = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:worklet");
  return { track, stream, getUserMedia, contexts, nodes, connect, createObjectURL };
}

const flush = () => new Promise((r) => setTimeout(r, 0));
const buf = (n: number) => new Uint8Array([n, 0]).buffer;

describe("MicCapture", () => {
  beforeEach(() => vi.useRealTimers());
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("opens a mono microphone through the PCM worklet and hands chunks on", async () => {
    const env = audioEnv();
    const chunks: ArrayBuffer[] = [];
    const onError = vi.fn();
    const cap = new MicCapture();
    cap.start((b) => chunks.push(b), onError);
    await flush();
    expect(env.getUserMedia).toHaveBeenCalledWith({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true } });
    const ctx = env.contexts[0]!;
    expect(ctx.addModule).toHaveBeenCalledWith("blob:worklet");
    const node = env.nodes[0]!;
    expect(node.name).toBe(PCM_WORKLET_NAME);
    expect(node.options).toMatchObject({ numberOfInputs: 1, numberOfOutputs: 0, processorOptions: { outRate: DICTATION_SAMPLE_RATE, chunkSamples: DICTATION_CHUNK_SAMPLES } });
    expect(env.connect).toHaveBeenCalledWith(node);
    expect(ctx.resume).not.toHaveBeenCalled();
    node.emit("chunk", buf(1));
    node.emit("chunk", null);
    expect(chunks).toEqual([buf(1)]);
    expect(onError).not.toHaveBeenCalled();
  });

  it("resumes a suspended context and reuses the worklet module", async () => {
    const env = audioEnv({ state: "suspended" });
    const a = new MicCapture();
    a.start(() => {}, vi.fn());
    await flush();
    const b = new MicCapture();
    b.start(() => {}, vi.fn());
    await flush();
    expect(env.contexts[0]!.resume).toHaveBeenCalled();
    expect(env.contexts[1]!.addModule).toHaveBeenCalledWith(env.contexts[0]!.addModule.mock.calls[0]![0]);
  });

  it("finishes by flushing the worklet's last chunk, then closes everything", async () => {
    const env = audioEnv({ closeFails: true });
    const chunks: ArrayBuffer[] = [];
    const cap = new MicCapture();
    cap.start((b) => chunks.push(b), vi.fn());
    await flush();
    const node = env.nodes[0]!;
    node.port.postMessage.mockImplementation(() => node.emit("flushed", buf(9)));
    await cap.finish();
    expect(node.port.postMessage).toHaveBeenCalledWith("flush");
    expect(chunks).toEqual([buf(9)]);
    expect(node.port.close).toHaveBeenCalled();
    expect(node.disconnect).toHaveBeenCalled();
    expect(env.track.stop).toHaveBeenCalled();
    expect(env.contexts[0]!.close).toHaveBeenCalled();
    // A chunk after the close reaches no one.
    node.emit("chunk", buf(10));
    expect(chunks).toEqual([buf(9)]);
  });

  it("gives up waiting for the flush after 300 ms", async () => {
    const env = audioEnv();
    const cap = new MicCapture();
    cap.start(() => {}, vi.fn());
    await flush();
    vi.useFakeTimers();
    const done = cap.finish();
    await vi.advanceTimersByTimeAsync(299);
    expect(env.track.stop).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    await done;
    expect(env.track.stop).toHaveBeenCalled();
  });

  it("reports a failed open and releases what it opened", async () => {
    const env = audioEnv();
    env.getUserMedia.mockRejectedValueOnce(Object.assign(new Error("no"), { name: "NotAllowedError" }));
    const onError = vi.fn();
    const cap = new MicCapture();
    cap.start(() => {}, onError);
    await flush();
    expect(onError).toHaveBeenCalledWith(expect.objectContaining({ name: "NotAllowedError" }));
    // Finishing a capture that never opened just closes it.
    await cap.finish();
    expect(env.nodes).toHaveLength(0);
  });

  it("stays quiet about a failure after it was stopped", async () => {
    const env = audioEnv();
    let reject!: (e: unknown) => void;
    env.getUserMedia.mockReturnValueOnce(new Promise((_r, j) => (reject = j)));
    const onError = vi.fn();
    const cap = new MicCapture();
    cap.start(() => {}, onError);
    cap.stop();
    reject(new Error("late"));
    await flush();
    expect(onError).not.toHaveBeenCalled();
  });

  it("closes the stream when stopped while getUserMedia was pending", async () => {
    const env = audioEnv();
    let resolve!: (s: typeof env.stream) => void;
    env.getUserMedia.mockReturnValueOnce(new Promise((r) => (resolve = r)));
    const cap = new MicCapture();
    cap.start(() => {}, vi.fn());
    cap.stop();
    resolve(env.stream);
    await flush();
    expect(env.track.stop).toHaveBeenCalled();
    expect(env.contexts).toHaveLength(0);
  });

  it("closes the context when stopped while the worklet was loading", async () => {
    let loaded!: () => void;
    const env = audioEnv({ addModule: () => new Promise<void>((r) => (loaded = r)) });
    const cap = new MicCapture();
    cap.start(vi.fn(), vi.fn());
    await flush();
    const ctx = env.contexts[0]!;
    cap.stop();
    loaded();
    await flush();
    expect(env.nodes).toHaveLength(0);
    expect(ctx.close).toHaveBeenCalled();
    expect(env.track.stop).toHaveBeenCalled();
  });
});
