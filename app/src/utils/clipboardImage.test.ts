import { describe, expect, it, vi } from "vitest";
import { firstClipboardImage, uploadPastedImage } from "./clipboardImage";

vi.mock("../api/channels", () => ({
  pasteImage: vi.fn(async () => "/work/.loop/pastes/paste-x.png"),
}));
import { pasteImage } from "../api/channels";

// A minimal DataTransfer stand-in: firstClipboardImage only touches `.items`.
function dt(items: Array<Partial<DataTransferItem>>): DataTransfer {
  return { items } as unknown as DataTransfer;
}

describe("firstClipboardImage", () => {
  it("returns null for missing clipboard data", () => {
    expect(firstClipboardImage(null)).toBeNull();
    expect(firstClipboardImage(undefined)).toBeNull();
  });

  it("returns null when there is no image item", () => {
    const d = dt([{ kind: "string", type: "text/plain", getAsFile: () => null }]);
    expect(firstClipboardImage(d)).toBeNull();
  });

  it("ignores non-image files", () => {
    const pdf = new File([], "x.pdf", { type: "application/pdf" });
    const d = dt([{ kind: "file", type: "application/pdf", getAsFile: () => pdf }]);
    expect(firstClipboardImage(d)).toBeNull();
  });

  it("returns the first image file, skipping earlier non-image items", () => {
    const img = new File([], "x.png", { type: "image/png" });
    const d = dt([
      { kind: "string", type: "text/plain", getAsFile: () => null },
      { kind: "file", type: "image/png", getAsFile: () => img },
    ]);
    expect(firstClipboardImage(d)).toBe(img);
  });
});

describe("uploadPastedImage", () => {
  it("base64-encodes the file bytes and returns the saved path", async () => {
    const file = new File([new Uint8Array([1, 2, 3, 4])], "x.png", { type: "image/png" });
    const path = await uploadPastedImage("ch-1", file);
    expect(path).toBe("/work/.loop/pastes/paste-x.png");
    expect(pasteImage).toHaveBeenCalledWith("ch-1", btoa("\x01\x02\x03\x04"), "image/png");
  });
});
