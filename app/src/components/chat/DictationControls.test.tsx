import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DictationStatus } from "../../api/dictation";
import type { useDictation } from "../../hooks/useDictation";
import { DictationBanner, DictationCorner, DictationMicButton, dictationCornerReserve, PromptEditButton } from "./DictationControls";
import { type DictationUiState, IDLE_DICTATION } from "./dictationController";
import type { DictationAvailability } from "./dictationModel";

type Dictation = ReturnType<typeof useDictation>;

function status(patch: Partial<DictationStatus> = {}, download: Partial<DictationStatus["download"]> = {}): DictationStatus {
  return {
    enabled: true,
    available: true,
    reason: "",
    model: "base",
    model_ready: true,
    model_size_bytes: 574_041_195,
    running: false,
    send_phrases: null,
    ...patch,
    download: { active: false, received: 0, total: 0, error: "", ...download },
  };
}

function hold() {
  return { onPointerDown: vi.fn(), onPointerUp: vi.fn(), onPointerCancel: vi.fn(), onMouseDown: vi.fn() };
}

function dict(
  opts: { availability?: DictationAvailability; ui?: Partial<DictationUiState>; canEdit?: boolean; status?: DictationStatus | null; downloading?: boolean; downloadError?: string | null } = {},
): Dictation {
  const ui = { ...IDLE_DICTATION, ...opts.ui };
  return {
    status: opts.status === undefined ? status() : opts.status,
    availability: opts.availability ?? "ready",
    ui,
    busy: ui.phase !== "idle",
    downloading: opts.downloading ?? false,
    downloadError: opts.downloadError ?? null,
    startDownload: vi.fn(async () => {}),
    dismiss: vi.fn(),
    micButton: hold(),
    editButton: hold(),
    canEdit: opts.canEdit ?? false,
    undoEdit: vi.fn(),
  } as unknown as Dictation;
}

const banner = () => screen.queryByTestId("dictation-banner");

describe("dictationCornerReserve", () => {
  it.each([
    ["unknown", false, 0],
    ["disabled", true, 0],
    ["ready", false, 17],
    ["unavailable", true, 38],
  ] as const)("reserves for %s (can edit: %s)", (availability, canEdit, want) => {
    expect(dictationCornerReserve(dict({ availability, canEdit }))).toBe(want);
  });
});

describe("DictationCorner", () => {
  it("renders nothing while dictation is unknown or off", () => {
    render(<DictationCorner d={dict({ availability: "disabled" })} />);
    expect(screen.queryByTestId("dictation-corner")).toBeNull();
  });

  it("holds the mic alone, or the mic and the edit button with a divider", () => {
    const { rerender } = render(<DictationCorner d={dict()} />);
    const corner = screen.getByTestId("dictation-corner");
    expect(screen.getByRole("button", { name: "Dictate" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Edit by voice" })).toBeNull();
    expect(corner.querySelectorAll(":scope > span")).toHaveLength(1);
    rerender(<DictationCorner d={dict({ canEdit: true })} />);
    expect(screen.getByRole("button", { name: "Edit by voice" })).toBeTruthy();
    expect(corner.querySelectorAll(":scope > span")).toHaveLength(3);
  });
});

describe("DictationMicButton", () => {
  it.each(["unknown", "disabled"] as const)("is hidden while %s", (availability) => {
    render(<DictationMicButton d={dict({ availability })} />);
    expect(screen.queryByTestId("dictation-mic")).toBeNull();
  });

  it("wires the hold handlers and shows how to dictate", async () => {
    const d = dict();
    render(<DictationMicButton d={d} />);
    const mic = screen.getByTestId("dictation-mic");
    expect(mic.getAttribute("aria-pressed")).toBe("false");
    expect(mic.getAttribute("data-phase")).toBe("idle");
    expect(mic.style.background).toBe("transparent");
    fireEvent.pointerDown(mic);
    fireEvent.pointerUp(mic);
    expect(d.micButton.onPointerDown).toHaveBeenCalledOnce();
    expect(d.micButton.onPointerUp).toHaveBeenCalledOnce();
    await userEvent.setup().hover(mic);
    expect((await screen.findByRole("tooltip")).textContent).toMatch(/^DictationHold Right Alt in the composer/);
  });

  it("pulses while recording and stays lit while transcribing", () => {
    const { rerender } = render(<DictationMicButton d={dict({ ui: { phase: "recording" } })} />);
    const mic = screen.getByTestId("dictation-mic");
    expect(mic.getAttribute("aria-pressed")).toBe("true");
    expect(mic.style.animation).toContain("loop-dictation-pulse");
    rerender(<DictationMicButton d={dict({ ui: { phase: "transcribing" } })} />);
    expect(mic.getAttribute("data-phase")).toBe("transcribing");
    expect(mic.style.animation).toBe("");
    expect(mic.style.background).not.toBe("transparent");
  });

  it("is dim and inert when the transcriber is missing", () => {
    const d = dict({ availability: "unavailable" });
    render(<DictationMicButton d={d} />);
    const mic = screen.getByTestId("dictation-mic");
    expect(mic.getAttribute("aria-disabled")).toBe("true");
    expect(mic.style.opacity).toBe("0.4");
    expect(mic.style.cursor).toBe("default");
    fireEvent.pointerDown(mic);
    expect(d.micButton.onPointerDown).not.toHaveBeenCalled();
  });
});

describe("PromptEditButton", () => {
  it.each([
    ["without edits", { canEdit: false }],
    ["while unknown", { canEdit: true, availability: "unknown" }],
    ["while off", { canEdit: true, availability: "disabled" }],
  ] as const)("is hidden %s", (_name, opts) => {
    render(<PromptEditButton d={dict(opts)} />);
    expect(screen.queryByTestId("prompt-edit-voice")).toBeNull();
  });

  it("wires the hold handlers when idle", () => {
    const d = dict({ canEdit: true });
    render(<PromptEditButton d={d} />);
    const btn = screen.getByTestId("prompt-edit-voice");
    expect(btn.getAttribute("aria-disabled")).toBe("false");
    expect(btn.getAttribute("data-phase")).toBe("idle");
    fireEvent.pointerDown(btn);
    expect(d.editButton.onPointerDown).toHaveBeenCalledOnce();
  });

  it("lights up through an edit hold and pulses while applying", () => {
    const { rerender } = render(<PromptEditButton d={dict({ canEdit: true, ui: { mode: "edit", phase: "recording" } })} />);
    const btn = screen.getByTestId("prompt-edit-voice");
    expect(btn.getAttribute("aria-pressed")).toBe("true");
    expect(btn.getAttribute("data-phase")).toBe("recording");
    expect(btn.style.animation).toContain("loop-dictation-pulse");
    rerender(<PromptEditButton d={dict({ canEdit: true, ui: { mode: "edit", phase: "editing" } })} />);
    expect(btn.getAttribute("aria-pressed")).toBe("false");
    expect(btn.getAttribute("data-phase")).toBe("editing");
    expect(btn.style.animation).toContain("loop-dictation-pulse");
    expect(btn.style.background).not.toBe("transparent");
  });

  it("is inert during a plain dictation and when the transcriber is missing", () => {
    const { rerender } = render(<PromptEditButton d={dict({ canEdit: true, ui: { mode: "dictate", phase: "recording" } })} />);
    const btn = screen.getByTestId("prompt-edit-voice");
    expect(btn.getAttribute("aria-disabled")).toBe("true");
    expect(btn.getAttribute("data-phase")).toBe("idle");
    expect(btn.style.animation).toBe("");
    const d = dict({ canEdit: true, availability: "unavailable" });
    rerender(<PromptEditButton d={d} />);
    expect(btn.style.opacity).toBe("0.4");
    fireEvent.pointerDown(btn);
    expect(d.editButton.onPointerDown).not.toHaveBeenCalled();
  });
});

describe("DictationBanner", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.resetModules();
  });

  it("renders nothing when idle", () => {
    render(<DictationBanner d={dict()} />);
    expect(banner()).toBeNull();
  });

  it("shows an edit hold's instruction with a level meter while recording", () => {
    render(<DictationBanner d={dict({ ui: { mode: "edit", phase: "recording", instruction: "shorter", level: 0.25 } })} />);
    expect(banner()!.getAttribute("data-state")).toBe("edit-recording");
    expect(screen.getByTestId("prompt-edit-instruction").textContent).toBe("shorter");
    expect((screen.getByTestId("dictation-level").firstElementChild as HTMLElement).style.width).toBe("80%");
    expect(screen.getByText("Release to apply · Esc to cancel")).toBeTruthy();
  });

  it("prompts for the instruction while transcribing an edit hold with nothing heard", () => {
    render(<DictationBanner d={dict({ ui: { mode: "edit", phase: "transcribing" } })} />);
    expect(banner()!.getAttribute("data-state")).toBe("edit-transcribing");
    const instr = screen.getByTestId("prompt-edit-instruction");
    expect(instr.textContent).toBe("say how to change the draft…");
    expect(instr.style.fontStyle).toBe("italic");
    expect(screen.queryByTestId("dictation-level")).toBeNull();
    expect(screen.getByText("Transcribing…")).toBeTruthy();
  });

  it("shows the instruction while the edit is applied", () => {
    render(<DictationBanner d={dict({ ui: { mode: "edit", phase: "editing", instruction: "fix typos" } })} />);
    expect(banner()!.getAttribute("data-state")).toBe("editing");
    expect(screen.getByTestId("prompt-edit-instruction").textContent).toBe("fix typos");
    expect(screen.getByText("Esc to cancel")).toBeTruthy();
  });

  it("listens with a capped level meter, then transcribes", () => {
    const { rerender } = render(<DictationBanner d={dict({ ui: { phase: "recording", level: 1 } })} />);
    expect(banner()!.getAttribute("data-state")).toBe("recording");
    expect((screen.getByTestId("dictation-level").firstElementChild as HTMLElement).style.width).toBe("100%");
    rerender(<DictationBanner d={dict({ ui: { phase: "transcribing" } })} />);
    expect(banner()!.getAttribute("data-state")).toBe("transcribing");
  });

  it("offers the model download, and dismisses", async () => {
    const d = dict({ ui: { notice: "needs_model" }, status: status({ model_ready: false }) });
    render(<DictationBanner d={d} />);
    expect(banner()!.getAttribute("data-state")).toBe("needs-model");
    expect(screen.getByText("Dictation needs a one-time ~550 MB model download")).toBeTruthy();
    const user = userEvent.setup();
    await user.click(screen.getByTestId("dictation-download"));
    expect(d.startDownload).toHaveBeenCalledOnce();
    await user.click(screen.getByTitle("Dismiss"));
    expect(d.dismiss).toHaveBeenCalledOnce();
  });

  it.each([
    ["the hook's", { downloadError: "disk full" }, {}],
    ["the daemon's", {}, { error: "checksum mismatch" }],
  ])("offers a retry after %s download error", (_name, opts, download) => {
    render(<DictationBanner d={dict({ ui: { notice: "needs_model" }, status: status({ model_ready: false }, download), ...opts })} />);
    expect(screen.getByText(/^Model download failed: /)).toBeTruthy();
    expect(screen.getByTestId("dictation-download").textContent).toBe("Retry");
  });

  it("shows the download's progress", () => {
    render(<DictationBanner d={dict({ ui: { notice: "needs_model" }, status: status({ model_ready: false }, { active: true, received: 25, total: 100 }), downloading: true })} />);
    expect(screen.getByText(/^Dictation model: Downloading… 25% \(/)).toBeTruthy();
    expect(screen.queryByTestId("dictation-download")).toBeNull();
  });

  it.each([
    ["without a status", null],
    ["once the model is ready", status()],
  ])("drops the download prompt %s", (_name, s) => {
    render(<DictationBanner d={dict({ ui: { notice: "needs_model" }, status: s })} />);
    expect(banner()).toBeNull();
  });

  it("explains a missing transcriber", () => {
    render(<DictationBanner d={dict({ ui: { notice: "unavailable" } })} />);
    expect(banner()!.getAttribute("data-state")).toBe("unavailable");
    expect(screen.getByText(/whisper-server/)).toBeTruthy();
  });

  it("asks to allow the microphone", () => {
    render(<DictationBanner d={dict({ ui: { notice: "mic_denied" } })} />);
    expect(banner()!.getAttribute("data-state")).toBe("mic-denied");
    expect(screen.getByText(/Allow microphone access for this app/)).toBeTruthy();
  });

  it("points the desktop app on a Mac at System Settings", async () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
    vi.stubGlobal("loopAPI", {});
    vi.resetModules();
    const { DictationBanner: MacBanner } = await import("./DictationControls");
    render(<MacBanner d={dict({ ui: { notice: "mic_denied" } })} />);
    expect(screen.getByText(/System Settings → Privacy & Security → Microphone/)).toBeTruthy();
  });

  it("offers Undo after an edit", async () => {
    const d = dict({ ui: { undo: true, instruction: "shorter" } });
    render(<DictationBanner d={d} />);
    expect(banner()!.getAttribute("data-state")).toBe("edited");
    expect(screen.getByText("Edited: shorter")).toBeTruthy();
    await userEvent.setup().click(screen.getByTestId("prompt-edit-undo"));
    expect(d.undoEdit).toHaveBeenCalledOnce();
  });

  it("shows an error, over an Undo", () => {
    render(<DictationBanner d={dict({ ui: { undo: true, error: "The draft changed meanwhile" } })} />);
    expect(banner()!.getAttribute("data-state")).toBe("error");
    expect(screen.getByText("The draft changed meanwhile")).toBeTruthy();
  });
});
