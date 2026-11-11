import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type DictationStatus, fetchDictationStatus, startDictationModelDownload } from "../../api/dictation";
import { dictationAvailability, downloadDictationModel, downloadProgressText, MODEL_POLL_MS, micButtonTip, modelSizeLabel } from "./dictationModel";

function status(patch: Partial<DictationStatus> = {}, download: Partial<DictationStatus["download"]> = {}): DictationStatus {
  return {
    enabled: true,
    available: true,
    reason: "",
    model: "large-v3-turbo-q5_0",
    model_ready: false,
    model_size_bytes: 574041195,
    running: false,
    ...patch,
    download: { active: false, received: 0, total: 0, error: "", ...download },
  };
}

describe("dictationAvailability", () => {
  it.each([
    ["no status", null, "unknown"],
    ["turned off, whatever else holds", status({ enabled: false, model_ready: true }), "disabled"],
    ["no transcriber", status({ available: false, reason: "binary_missing" }), "unavailable"],
    ["no model", status(), "needs_model"],
    ["a download running", status({}, { active: true }), "downloading"],
    ["the model on disk", status({ model_ready: true }), "ready"],
  ] as const)("%s", (_name, s, want) => {
    expect(dictationAvailability(s)).toBe(want);
  });
});

describe("prompt text", () => {
  it("rounds the model size", () => {
    expect(modelSizeLabel(status())).toBe("~550 MB");
    expect(modelSizeLabel(null)).toBe("large");
  });

  it("shows download progress", () => {
    expect(downloadProgressText(status({}, { active: true, received: 287_000_000, total: 574_041_195 }))).toMatch(/^Downloading… 49% \(/);
    expect(downloadProgressText(status({}, { active: true }))).toBe("Downloading…");
  });

  it("explains a missing transcriber and names the hold key", () => {
    expect(micButtonTip("unavailable", status({ available: false, reason: "binary_missing" }), true)).toContain("isn't installed");
    expect(micButtonTip("ready", status({ model_ready: true }), true)).toContain("Right Option");
    expect(micButtonTip("ready", status({ model_ready: true }), false)).toContain("Right Alt");
    expect(micButtonTip("needs_model", status(), false)).toContain("~550 MB");
  });
});

describe("downloadDictationModel", () => {
  function deps(seq: DictationStatus[]) {
    const progress: DictationStatus[] = [];
    const sleeps: number[] = [];
    const calls: string[] = [];
    let i = 0;
    return {
      progress,
      sleeps,
      calls,
      d: {
        start: async () => {
          calls.push("start");
        },
        status: async () => {
          calls.push("status");
          return seq[Math.min(i++, seq.length - 1)]!;
        },
        onProgress: (s: DictationStatus) => progress.push(s),
        sleep: async (ms: number) => {
          sleeps.push(ms);
        },
      },
    };
  }

  it("starts the download and polls every 500 ms until the model is ready", async () => {
    const seq = [status({}, { active: true, received: 1, total: 10 }), status({}, { active: true, received: 5, total: 10 }), status({ model_ready: true })];
    const t = deps(seq);
    await expect(downloadDictationModel(t.d)).resolves.toEqual(seq[2]);
    expect(t.calls).toEqual(["start", "status", "status", "status"]);
    expect(t.progress).toEqual(seq);
    expect(t.sleeps).toEqual([MODEL_POLL_MS, MODEL_POLL_MS]);
    expect(MODEL_POLL_MS).toBe(500);
  });

  it("rejects with the download's error", async () => {
    const t = deps([status({}, { active: true }), status({}, { error: "disk full" })]);
    await expect(downloadDictationModel(t.d)).rejects.toThrow("disk full");
  });

  it("rejects when the download stops without a model or an error", async () => {
    const t = deps([status()]);
    await expect(downloadDictationModel(t.d)).rejects.toThrow("stopped");
    expect(t.sleeps.length).toBeGreaterThan(0);
  });

  it("rejects when the start fails", async () => {
    const t = deps([status()]);
    t.d.start = async () => {
      throw new Error("nope");
    };
    await expect(downloadDictationModel(t.d)).rejects.toThrow("nope");
  });

  it("stops polling for a cancelled caller", async () => {
    const t = deps([status({}, { active: true })]);
    let n = 0;
    await expect(downloadDictationModel({ ...t.d, cancelled: () => ++n > 2 })).resolves.toBeNull();
  });
});

describe("dictation API", () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    vi.stubGlobal("window", { location: { hash: "" } });
    vi.stubGlobal("sessionStorage", { getItem: () => null, setItem: () => {} });
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("reads the status", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify(status())));
    await expect(fetchDictationStatus()).resolves.toEqual(status());
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/dictation/status");
  });

  it("starts the model download with a POST and surfaces a refusal", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 202 }));
    await startDictationModelDownload();
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/dictation/model");
    expect(fetchMock.mock.calls[0]![1].method).toBe("POST");
    fetchMock.mockResolvedValue(new Response("busy", { status: 409 }));
    await expect(startDictationModelDownload()).rejects.toThrow("busy");
  });
});
