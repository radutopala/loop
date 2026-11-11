import { describe, expect, it } from "vitest";
import { DICTATION_CHUNK_SAMPLES, DICTATION_SAMPLE_RATE, makePcmEncoder, PCM_WORKLET_NAME, pcmLevel, pcmWorkletSource } from "./dictationPcm";

function samples(buf: ArrayBuffer): number[] {
  const v = new DataView(buf);
  return Array.from({ length: buf.byteLength / 2 }, (_, i) => v.getInt16(i * 2, true));
}

describe("makePcmEncoder", () => {
  it("averages each group of three samples from 48 kHz to 16 kHz", () => {
    const enc = makePcmEncoder(48000, 16000, 4);
    const out = enc.push(new Float32Array([0, 0.3, 0.6, 1, 1, 1, -1, -1, -1, 0.5, 0.5, 0.5]));
    expect(out).toHaveLength(1);
    expect(samples(out[0]!)).toEqual([Math.round(0.3 * 0x7fff), 0x7fff, -0x8000, Math.round(0.5 * 0x7fff)]);
    expect(enc.flush()).toBeNull();
  });

  it("writes little-endian bytes", () => {
    const enc = makePcmEncoder(16000, 16000, 1);
    const [buf] = enc.push(new Float32Array([1]));
    expect(Array.from(new Uint8Array(buf!))).toEqual([0xff, 0x7f]);
  });

  it("clamps out-of-range samples", () => {
    const enc = makePcmEncoder(16000, 16000, 2);
    expect(samples(enc.push(new Float32Array([2, -3]))[0]!)).toEqual([0x7fff, -0x8000]);
  });

  it("carries partial groups and chunks across pushes", () => {
    const enc = makePcmEncoder(48000, 16000, 3);
    expect(enc.push(new Float32Array([1, 1]))).toEqual([]);
    expect(enc.push(new Float32Array([1, 0, 0]))).toEqual([]);
    const out = enc.push(new Float32Array([0, 0, 0, 0]));
    expect(out).toHaveLength(1);
    expect(samples(out[0]!)).toEqual([0x7fff, 0, 0]);
    // One sample of the next group is pending; nothing complete to flush.
    expect(enc.flush()).toBeNull();
  });

  it("flushes the samples short of a chunk", () => {
    const enc = makePcmEncoder(16000, 16000, 10);
    expect(enc.push(new Float32Array([0.5, -0.5]))).toEqual([]);
    expect(samples(enc.flush()!)).toEqual([Math.round(0.5 * 0x7fff), -0x4000]);
    expect(enc.flush()).toBeNull();
  });

  it("keeps the 16 kHz rate from 44.1 kHz input", () => {
    const enc = makePcmEncoder(44100, DICTATION_SAMPLE_RATE, DICTATION_CHUNK_SAMPLES);
    // One second of input in 128-sample render quanta.
    let n = 0;
    for (let i = 0; i < 44100; i += 128) {
      for (const b of enc.push(new Float32Array(Math.min(128, 44100 - i)))) n += b.byteLength / 2;
    }
    n += (enc.flush()?.byteLength ?? 0) / 2;
    expect(Math.abs(n - 16000)).toBeLessThanOrEqual(1);
  });

  it("repeats samples when the input rate is lower", () => {
    const enc = makePcmEncoder(8000, 16000, 4);
    expect(samples(enc.push(new Float32Array([0.5, -0.5]))[0]!)).toEqual([16384, 16384, -16384, -16384]);
  });
});

describe("pcmLevel", () => {
  it("is 0 for silence or nothing and near 1 at full scale", () => {
    expect(pcmLevel(new ArrayBuffer(0))).toBe(0);
    expect(pcmLevel(new ArrayBuffer(8))).toBe(0);
    const enc = makePcmEncoder(16000, 16000, 4);
    expect(pcmLevel(enc.push(new Float32Array([-1, -1, -1, -1]))[0]!)).toBe(1);
  });
});

describe("pcmWorkletSource", () => {
  it("registers a processor that posts chunks and answers a flush", () => {
    const posted: unknown[] = [];
    let Processor: any;
    class FakeProcessor {
      port = { onmessage: null as ((e: { data: unknown }) => void) | null, postMessage: (m: unknown) => posted.push(m) };
    }
    new Function("AudioWorkletProcessor", "registerProcessor", "sampleRate", pcmWorkletSource())(
      FakeProcessor,
      (name: string, cls: unknown) => {
        expect(name).toBe(PCM_WORKLET_NAME);
        Processor = cls;
      },
      48000,
    );
    const p = new Processor({ processorOptions: { outRate: 16000, chunkSamples: 2 } });
    expect(p.process([[new Float32Array([1, 1, 1, 0, 0, 0, 1, 1, 1])]])).toBe(true);
    expect(posted).toHaveLength(1);
    expect((posted[0] as { type: string }).type).toBe("chunk");
    expect(samples((posted[0] as { buf: ArrayBuffer }).buf)).toEqual([0x7fff, 0]);
    p.process([[]]);
    p.port.onmessage({ data: "flush" });
    expect((posted[1] as { type: string; buf: ArrayBuffer }).type).toBe("flushed");
    expect(samples((posted[1] as { buf: ArrayBuffer }).buf)).toEqual([0x7fff]);
    p.port.onmessage({ data: "flush" });
    expect(posted[2]).toEqual({ type: "flushed", buf: null });
  });
});
