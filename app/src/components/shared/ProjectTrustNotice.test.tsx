import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchProjectTrust, ProjectTrustChangedError, type ProjectTrustStatus, trustProjectConfig } from "../../api/configApi";
import { darkColors } from "../../theme";
import { ProjectTrustNotice } from "./ProjectTrustNotice";

vi.mock("../../api/configApi", () => ({
  fetchProjectTrust: vi.fn(),
  trustProjectConfig: vi.fn(),
  ProjectTrustChangedError: class extends Error {},
}));

const fetchMock = vi.mocked(fetchProjectTrust);
const trustMock = vi.mocked(trustProjectConfig);

const untrusted: ProjectTrustStatus = { trusted: false, current: "{}", approved: "{}", diff: "-a\n+b\n", hash: "h1" };

async function renderNotice(props: Partial<Parameters<typeof ProjectTrustNotice>[0]> = {}) {
  const onTrusted = vi.fn();
  const utils = render(<ProjectTrustNotice channelId="ch-1" colors={darkColors} refreshKey={0} onTrusted={onTrusted} {...props} />);
  await screen.findByTestId("project-trust-notice");
  return { ...utils, onTrusted };
}

describe("ProjectTrustNotice", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fetchMock.mockResolvedValue(untrusted);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders nothing while trusted", async () => {
    fetchMock.mockResolvedValue({ ...untrusted, trusted: true });
    const { container } = render(<ProjectTrustNotice channelId="ch-1" colors={darkColors} refreshKey={0} />);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("ch-1"));
    expect(container.innerHTML).toBe("");
  });

  it("hides and logs when the status can't load", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    fetchMock.mockRejectedValue(new Error("down"));
    const { container } = render(<ProjectTrustNotice channelId="ch-1" colors={darkColors} refreshKey={0} />);
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching project trust:", expect.any(Error)));
    expect(container.innerHTML).toBe("");
  });

  it("shows the diff and says the last trusted version applies", async () => {
    await renderNotice();
    expect(screen.getByText(/Loop uses the last trusted version until you trust this one/)).toBeTruthy();
    expect(screen.getByTestId("project-trust-diff").textContent).toBe("-a+b");
  });

  it("says nothing applies when nothing was trusted yet", async () => {
    fetchMock.mockResolvedValue({ ...untrusted, approved: "" });
    await renderNotice();
    expect(screen.getByText(/None of them apply until you trust them/)).toBeTruthy();
  });

  it("trusts the shown version, re-checks and tells the caller", async () => {
    let finish!: () => void;
    trustMock.mockReturnValue(new Promise<void>((r) => (finish = r)));
    const { onTrusted } = await renderNotice();
    fireEvent.click(screen.getByTestId("project-trust-button"));
    expect(trustMock).toHaveBeenCalledWith("ch-1", "h1");
    expect(screen.getByTestId("project-trust-button")).toHaveProperty("disabled", true);
    fetchMock.mockResolvedValue({ ...untrusted, trusted: true });
    await act(async () => finish());
    expect(onTrusted).toHaveBeenCalledOnce();
    await waitFor(() => expect(screen.queryByTestId("project-trust-notice")).toBeNull());
  });

  it("trusts without a callback", async () => {
    trustMock.mockResolvedValue(undefined);
    await renderNotice({ onTrusted: undefined });
    await act(async () => {
      fireEvent.click(screen.getByTestId("project-trust-button"));
    });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it.each([
    ["the config changed again", new ProjectTrustChangedError("stale"), "The project config changed again. Review the new version."],
    ["a failure's message", new Error("forbidden"), "forbidden"],
    ["a fallback for a failure without one", {}, "Failed to trust"],
  ])("shows %s, re-checks, and clears it on refresh", async (_name, err, want) => {
    trustMock.mockRejectedValue(err);
    const { rerender, onTrusted } = await renderNotice();
    await act(async () => {
      fireEvent.click(screen.getByTestId("project-trust-button"));
    });
    expect(screen.getByText(want)).toBeTruthy();
    expect(onTrusted).not.toHaveBeenCalled();
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId("project-trust-button")).toHaveProperty("disabled", false);
    rerender(<ProjectTrustNotice channelId="ch-1" colors={darkColors} refreshKey={1} />);
    await waitFor(() => expect(screen.queryByText(want)).toBeNull());
  });
});
