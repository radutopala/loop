/** The sample rate the transcriber takes. */
export const DICTATION_SAMPLE_RATE = 16000;
/** Samples per chunk sent to the daemon: 100 ms at 16 kHz. */
export const DICTATION_CHUNK_SAMPLES = 1600;

export interface PcmEncoder {
  /** Takes float samples at the input rate; returns the full chunks they completed. */
  push(input: Float32Array): ArrayBuffer[];
  /** Returns the samples still waiting for a full chunk, or null when none are. */
  flush(): ArrayBuffer | null;
}

/**
 * Builds a streaming encoder from float samples at inRate to signed 16-bit
 * little-endian mono at outRate, cut into chunks of chunkSamples. Each output
 * sample averages the input samples that fall in its interval (a box filter,
 * so downsampling doesn't fold high frequencies straight back in); an input
 * rate below the output repeats samples. It is self-contained (no outside
 * names) because the audio worklet gets its source text.
 */
export function makePcmEncoder(inRate: number, outRate: number, chunkSamples: number): PcmEncoder {
  const step = inRate / outRate;
  let acc = 0;
  let count = 0;
  let pos = 0;
  let chunk = new DataView(new ArrayBuffer(chunkSamples * 2));
  let filled = 0;
  const emit = (v: number, out: ArrayBuffer[]) => {
    const s = Math.max(-1, Math.min(1, v));
    chunk.setInt16(filled * 2, Math.round(s < 0 ? s * 0x8000 : s * 0x7fff), true);
    filled++;
    if (filled === chunkSamples) {
      out.push(chunk.buffer as ArrayBuffer);
      chunk = new DataView(new ArrayBuffer(chunkSamples * 2));
      filled = 0;
    }
  };
  return {
    push(input) {
      const out: ArrayBuffer[] = [];
      for (let i = 0; i < input.length; i++) {
        acc += input[i] as number;
        count++;
        pos += 1;
        if (pos >= step) {
          const v = acc / count;
          while (pos >= step) {
            emit(v, out);
            pos -= step;
          }
          acc = 0;
          count = 0;
        }
      }
      return out;
    },
    flush() {
      if (filled === 0) return null;
      const rest = chunk.buffer.slice(0, filled * 2) as ArrayBuffer;
      chunk = new DataView(new ArrayBuffer(chunkSamples * 2));
      filled = 0;
      return rest;
    },
  };
}

/** The RMS level of a 16-bit LE chunk, 0..1, for the input meter. */
export function pcmLevel(buf: ArrayBuffer): number {
  const n = Math.floor(buf.byteLength / 2);
  if (n === 0) return 0;
  const view = new DataView(buf);
  let sum = 0;
  for (let i = 0; i < n; i++) {
    const s = view.getInt16(i * 2, true) / 0x8000;
    sum += s * s;
  }
  return Math.min(1, Math.sqrt(sum / n));
}

/** The name the worklet registers its processor under. */
export const PCM_WORKLET_NAME = "loop-dictation-pcm";

/**
 * The audio worklet's source. It encodes its first input channel with
 * makePcmEncoder at the context's rate, posts each full chunk's buffer to
 * the port, and answers a "flush" message with what's left (or null). It
 * loads from a blob URL, which works the same from the dev server and from
 * the packaged app's file:// page.
 */
export function pcmWorkletSource(): string {
  return `const makePcmEncoder = ${makePcmEncoder.toString()};
class LoopDictationPcm extends AudioWorkletProcessor {
  constructor(options) {
    super();
    const o = (options && options.processorOptions) || {};
    this.enc = makePcmEncoder(sampleRate, o.outRate || ${DICTATION_SAMPLE_RATE}, o.chunkSamples || ${DICTATION_CHUNK_SAMPLES});
    this.port.onmessage = (e) => {
      if (e.data !== "flush") return;
      const rest = this.enc.flush();
      this.port.postMessage({ type: "flushed", buf: rest }, rest ? [rest] : []);
    };
  }
  process(inputs) {
    const ch = inputs[0] && inputs[0][0];
    if (ch) for (const buf of this.enc.push(ch)) this.port.postMessage({ type: "chunk", buf }, [buf]);
    return true;
  }
}
registerProcessor(${JSON.stringify(PCM_WORKLET_NAME)}, LoopDictationPcm);
`;
}
