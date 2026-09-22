import { describe, expect, it, vi } from "vitest";
import { type ClipboardWriter, writeClipboard } from "./copyToClipboard";

function fakeClipboard(overrides: Partial<ClipboardWriter> = {}): ClipboardWriter & { write: ReturnType<typeof vi.fn>; writeText: ReturnType<typeof vi.fn> } {
  return { write: vi.fn(async () => {}), writeText: vi.fn(async () => {}), ...overrides } as never;
}

// The real factory returns a ClipboardItem; the fake hands back the mime map
// itself so a test can read what was offered.
const makeItem = (data: Record<string, Blob>) => data as unknown as ClipboardItem;
const mimesOf = (item: ClipboardItem) => item as unknown as Record<string, Blob>;

async function textOf(blob: Blob | undefined): Promise<string> {
  return (blob as Blob).text();
}

describe("writeClipboard", () => {
  it("writes both flavors when html is given", async () => {
    const clip = fakeClipboard();
    await writeClipboard("a\tb", "<table></table>", clip, makeItem);

    expect(clip.writeText).not.toHaveBeenCalled();
    const [items] = clip.write.mock.calls[0] as [ClipboardItem[]];
    const mimes = mimesOf(items[0] as ClipboardItem);
    expect(Object.keys(mimes)).toEqual(["text/plain", "text/html"]);
    expect(await textOf(mimes["text/plain"])).toBe("a\tb");
    expect(await textOf(mimes["text/html"])).toBe("<table></table>");
  });

  it("writes plain text when there is no html", async () => {
    const clip = fakeClipboard();
    await writeClipboard("| a |", undefined, clip, makeItem);

    expect(clip.write).not.toHaveBeenCalled();
    expect(clip.writeText).toHaveBeenCalledWith("| a |");
  });

  it("falls back to plain text when the rich write is refused", async () => {
    const clip = fakeClipboard({
      write: vi.fn(async () => {
        throw new Error("NotAllowedError");
      }),
    });
    await writeClipboard("a\tb", "<table></table>", clip, makeItem);

    expect(clip.writeText).toHaveBeenCalledWith("a\tb");
  });

  it("falls back to plain text when the browser has no ClipboardItem", async () => {
    const clip = fakeClipboard();
    await writeClipboard("a\tb", "<table></table>", clip, undefined);

    expect(clip.write).not.toHaveBeenCalled();
    expect(clip.writeText).toHaveBeenCalledWith("a\tb");
  });

  it("falls back to plain text when the clipboard cannot write items", async () => {
    const clip = { writeText: vi.fn(async () => {}) } as ClipboardWriter & { writeText: ReturnType<typeof vi.fn> };
    await writeClipboard("a\tb", "<table></table>", clip, makeItem);

    expect(clip.writeText).toHaveBeenCalledWith("a\tb");
  });

  it("rejects when there is no clipboard at all", async () => {
    await expect(writeClipboard("a", undefined, undefined, makeItem)).rejects.toThrow("clipboard unavailable");
  });
});
