import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type AgentConfig, fetchAgentConfig, updateAgentConfig } from "../../api/channels";
import { AgentConfigPill } from "./AgentConfigPill";

vi.mock("../../api/channels", () => ({
  fetchAgentConfig: vi.fn(),
  updateAgentConfig: vi.fn(),
}));

const fetchMock = vi.mocked(fetchAgentConfig);
const updateMock = vi.mocked(updateAgentConfig);

function config(patch: Partial<AgentConfig> = {}): AgentConfig {
  return { model: "", effort: "", default_model: "claude-opus-5", default_effort: "high", models: ["claude-opus-5", "claude-sonnet-5"], ...patch } as AgentConfig;
}

async function renderPill(cfg: AgentConfig = config()) {
  fetchMock.mockResolvedValue(cfg);
  const user = userEvent.setup();
  render(<AgentConfigPill channelId="ch-1" />);
  await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("ch-1"));
  await act(async () => {});
  return user;
}

const pill = () => screen.getAllByRole("button")[0]!;

async function tipText(user: ReturnType<typeof userEvent.setup>) {
  await user.hover(pill());
  return (await screen.findByRole("tooltip")).textContent;
}

describe("AgentConfigPill", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    updateMock.mockResolvedValue(undefined);
  });
  afterEach(() => vi.restoreAllMocks());

  it("names the defaults that will run, without the claude- prefix", async () => {
    const user = await renderPill();
    expect(pill().textContent).toBe("opus-5 · high");
    expect(await tipText(user)).toBe("Model/effortOverride them for this channel's runs (default: claude-opus-5 · high).");
  });

  it("falls back to a generic label and the config when nothing is known", async () => {
    const user = await renderPill(config({ default_model: "", default_effort: "", models: undefined }));
    expect(pill().textContent).toBe("model");
    expect(await tipText(user)).toBe("Model/effortOverride them for this channel's runs (default: config).");
    await user.click(pill());
    expect(screen.getByRole("button", { name: "✓Default" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "✓Default (model default)" }).getAttribute("title")).toMatch(/^No claude_effort in config/);
  });

  it.each([
    [{ model: "claude-sonnet-5" }, "sonnet-5 · high", "Model/effort overrideclaude-sonnet-5 · (default effort) for this channel's runs, from the next run."],
    [{ effort: "low" }, "opus-5 · low", "Model/effort override(default claude-opus-5) · low for this channel's runs, from the next run."],
  ])("shows an override %o in the label and the tip", async (patch, label, tip) => {
    const user = await renderPill(config(patch));
    expect(pill().textContent).toBe(label);
    expect(await tipText(user)).toBe(tip);
  });

  it("opens the menu with the defaults checked and closes on the backdrop", async () => {
    const user = await renderPill();
    await user.click(pill());
    const modelDefault = screen.getByRole("button", { name: "✓Default (claude-opus-5)" });
    expect(modelDefault).toBeTruthy();
    expect(screen.getByRole("button", { name: "claude-sonnet-5" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "✓Default (high)" }).getAttribute("title")).toBe("From config: high");
    expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual(expect.arrayContaining(["low", "medium", "high", "xhigh", "max"]));
    expect(screen.getByText("Applies to this channel's next run")).toBeTruthy();
    const backdrop = modelDefault.parentElement!.previousElementSibling!;
    fireEvent.mouseDown(backdrop);
    expect(screen.queryByText("Applies to this channel's next run")).toBeNull();
    await user.click(pill());
    await user.click(pill());
    expect(screen.queryByText("Applies to this channel's next run")).toBeNull();
  });

  it("applies a picked model and effort right away", async () => {
    const user = await renderPill();
    await user.click(pill());
    await user.click(screen.getByRole("button", { name: "claude-sonnet-5" }));
    expect(updateMock).toHaveBeenLastCalledWith("ch-1", "claude-sonnet-5", "");
    expect(screen.getByRole("button", { name: "✓claude-sonnet-5" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "max" }));
    expect(updateMock).toHaveBeenLastCalledWith("ch-1", "claude-sonnet-5", "max");
    expect(screen.getByRole("button", { name: "✓max" })).toBeTruthy();
    expect(pill().textContent).toBe("sonnet-5 · max");
    await user.click(screen.getByRole("button", { name: "Default (high)" }));
    expect(updateMock).toHaveBeenLastCalledWith("ch-1", "claude-sonnet-5", "");
    await user.click(screen.getByRole("button", { name: "Default (claude-opus-5)" }));
    expect(updateMock).toHaveBeenLastCalledWith("ch-1", "", "");
  });

  it("applies a typed custom model on Enter and lists it while set", async () => {
    const user = await renderPill();
    await user.click(pill());
    const box = screen.getByPlaceholderText("custom model id…");
    await user.type(box, "   {Enter}");
    expect(updateMock).not.toHaveBeenCalled();
    await user.clear(box);
    await user.type(box, " my-model {Enter}");
    expect(updateMock).toHaveBeenCalledWith("ch-1", "my-model", "");
    expect((box as HTMLInputElement).value).toBe("");
    const row = screen.getByRole("button", { name: "✓my-model" });
    await user.click(row);
    expect(updateMock).toHaveBeenLastCalledWith("ch-1", "my-model", "");
  });

  it("rolls back and shows the error when the update fails", async () => {
    const user = await renderPill(config({ effort: "low" }));
    updateMock.mockRejectedValueOnce(new Error("bad model"));
    await user.click(pill());
    await user.click(screen.getByRole("button", { name: "claude-sonnet-5" }));
    expect(await screen.findByText("bad model")).toBeTruthy();
    expect(pill().textContent).toBe("opus-5 · low");
    updateMock.mockRejectedValueOnce("nope");
    await user.click(screen.getByRole("button", { name: "max" }));
    expect(await screen.findByText("Failed to update")).toBeTruthy();
    // A later pick clears it.
    await user.click(screen.getByRole("button", { name: "medium" }));
    await waitFor(() => expect(screen.queryByText("Failed to update")).toBeNull());
  });

  it("logs a failed fetch and keeps the generic label", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    fetchMock.mockRejectedValue(new Error("offline"));
    render(<AgentConfigPill channelId="ch-1" />);
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching agent config:", expect.any(Error)));
    expect(pill().textContent).toBe("model");
  });

  it("drops a config for a channel it no longer shows", async () => {
    let resolve!: (c: AgentConfig) => void;
    fetchMock.mockReturnValueOnce(new Promise((r) => (resolve = r)));
    fetchMock.mockResolvedValueOnce(config({ default_model: "claude-sonnet-5" }));
    const { rerender } = render(<AgentConfigPill channelId="ch-1" />);
    rerender(<AgentConfigPill channelId="ch-2" />);
    await waitFor(() => expect(pill().textContent).toBe("sonnet-5 · high"));
    await act(async () => resolve(config({ default_model: "claude-opus-5", default_effort: "max" })));
    expect(pill().textContent).toBe("sonnet-5 · high");
  });
});
