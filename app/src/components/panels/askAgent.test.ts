import { describe, expect, it } from "vitest";
import type { ChatPaneInput } from "../../uiBridge/types";
import { type AskDeps, askAgent, askMessage, MAX_ASK_CHARS } from "./askAgent";

describe("askMessage", () => {
  it("asks about the lines, quoting them", () => {
    expect(askMessage("/root/main.go", 3, 5, "func a() {\n\treturn\n}\n")).toBe("Explain /root/main.go:3-5\n\n```\nfunc a() {\n\treturn\n}\n```");
  });

  it("names one line, and what it's about", () => {
    expect(askMessage("main.go", 7, 7, "x := 1", "The counter")).toBe("Explain main.go:7 (The counter)\n\n```\nx := 1\n```");
  });

  it("fences text with backticks in it", () => {
    expect(askMessage("a.md", 1, 1, "````go")).toBe("Explain a.md:1\n\n`````\n````go\n`````");
  });

  it("cuts long text short, saying so", () => {
    const msg = askMessage("a.go", 1, 900, "y".repeat(MAX_ASK_CHARS + 5));
    expect(msg).toContain(`\`\`\`\n${"y".repeat(MAX_ASK_CHARS)}\n\`\`\``);
    expect(msg.endsWith(`(The first ${MAX_ASK_CHARS} characters of the lines.)`)).toBe(true);
  });
});

class FakeDeps implements AskDeps {
  sent: string[] = [];
  chatSent: string[] = [];
  hasChat = true;
  chat(channelId: string): ChatPaneInput | undefined {
    if (!this.hasChat) return undefined;
    return {
      busy: () => false,
      blocked: () => null,
      messages: () => [],
      send: async (text) => {
        this.chatSent.push(`${channelId} ${text}`);
      },
    };
  }
  async send(channelId: string, text: string) {
    this.sent.push(`${channelId} ${text}`);
  }
}

describe("askAgent", () => {
  it("queues the message as the chat pane's composer does", async () => {
    const deps = new FakeDeps();
    await askAgent("c1", "Explain a.go:1", deps);
    expect(deps.chatSent).toEqual(["c1 Explain a.go:1"]);
    expect(deps.sent).toEqual([]);
  });

  it("sends it to the channel without a chat pane", async () => {
    const deps = new FakeDeps();
    deps.hasChat = false;
    await askAgent("c1", "Explain a.go:1", deps);
    expect(deps.sent).toEqual(["c1 Explain a.go:1"]);
  });
});
