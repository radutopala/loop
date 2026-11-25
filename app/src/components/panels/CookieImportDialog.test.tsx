import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CookieSource } from "../../api/loopApi";
import { CookieImportDialog } from "./CookieImportDialog";

const mockListCookieSources = vi.fn<() => Promise<CookieSource[]>>();
const mockImportCookies = vi.fn<() => Promise<{ imported: number }>>();

vi.mock("../../api/loopApi", () => ({
  listCookieSources: (...args: unknown[]) => mockListCookieSources(...(args as [])),
  importCookies: (...args: unknown[]) => mockImportCookies(...(args as [])),
}));

const SOURCES: CookieSource[] = [
  {
    id: "chrome-default",
    browser: "chrome",
    name: "Default",
    domains: [
      { domain: "example.com", count: 1 },
      { domain: "app.example.com", count: 0 },
      { domain: "solo.org", count: 1 },
      { domain: "other.net", count: 5 },
    ],
  },
  {
    id: "edge-work",
    browser: "edge",
    name: "Work",
    domains: [
      { domain: "corp.io", count: 3 },
      { domain: "auth.corp.io", count: 2 },
    ],
  },
  {
    id: "ff-main",
    browser: "firefox",
    name: "Main",
    domains: [],
  },
  {
    id: "brave-1",
    browser: "brave",
    name: "Personal",
    error: "keychain locked",
    domains: [],
  },
];

function renderDialog(over: Partial<Parameters<typeof CookieImportDialog>[0]> = {}) {
  const props = {
    channelId: "ch-1",
    onClose: vi.fn(),
    onImported: vi.fn(),
    ...over,
  };
  return { props, ...render(<CookieImportDialog {...props} />) };
}

describe("CookieImportDialog", () => {
  beforeEach(() => {
    localStorage.clear();
    mockListCookieSources.mockReset();
    mockImportCookies.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("shows loading state, renders discovered profiles with labels, and handles empty list", async () => {
    let resolveList!: (list: CookieSource[]) => void;
    mockListCookieSources.mockReturnValue(
      new Promise<CookieSource[]>((res) => {
        resolveList = res;
      }),
    );

    const { props } = renderDialog();
    expect(screen.getByText("Looking for browsers…")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Choose sites" }) as HTMLButtonElement).disabled).toBe(true);

    await act(async () => {
      resolveList([]);
    });

    expect(screen.getByText("No Chrome, Edge or Firefox profile found on this machine.")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Choose sites" }) as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Not now" }));
    expect(props.onClose).toHaveBeenCalledTimes(1);
  });

  it("shows error when listing cookie sources fails", async () => {
    mockListCookieSources.mockRejectedValue(new Error("daemon unreachable"));
    renderDialog();

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe("daemon unreachable");
  });

  it("restores saved sourceId if still present and falls back to first source otherwise", async () => {
    localStorage.setItem("cookieImportSource:ch-1", "edge-work");
    mockListCookieSources.mockResolvedValue(SOURCES);

    const { unmount } = renderDialog();
    const select = (await screen.findByLabelText("Browser profile")) as HTMLSelectElement;
    expect(select.value).toBe("edge-work");
    expect(screen.getByText("Google Chrome — Default")).toBeTruthy();
    expect(screen.getByText("Microsoft Edge — Work")).toBeTruthy();
    expect(screen.getByText("Firefox — Main")).toBeTruthy();
    expect(screen.getByText("brave — Personal (unreadable)")).toBeTruthy();
    unmount();

    localStorage.setItem("cookieImportSource:ch-1", "deleted-profile");
    renderDialog();
    const select2 = (await screen.findByLabelText("Browser profile")) as HTMLSelectElement;
    expect(select2.value).toBe("chrome-default");
  });

  it("navigates to sites step, filters, expands/collapses groups, toggles items/groups/all, and imports", async () => {
    localStorage.setItem("cookieImportDomains:ch-1:chrome-default", JSON.stringify(["solo.org"]));
    mockListCookieSources.mockResolvedValue(SOURCES);
    mockImportCookies.mockResolvedValue({ imported: 4 });

    const { props } = renderDialog();
    await screen.findByLabelText("Browser profile");

    fireEvent.click(screen.getByRole("button", { name: "Choose sites" }));
    expect(screen.getByText("Choose sites to bring over")).toBeTruthy();
    expect(screen.getByText("1 of 4 selected")).toBeTruthy();

    // Select all checkbox should be indeterminate ("some")
    const selectAll = screen.getByLabelText("Select all") as HTMLInputElement;
    expect(selectAll.checked).toBe(false);
    expect(selectAll.indeterminate).toBe(true);

    // Group row for example.com (+1 child) with total count 1 ("1 cookie across 2 sites")
    const groupExpandBtn = screen.getByRole("button", { name: /example\.com/ });
    expect(groupExpandBtn.getAttribute("title")).toBe("1 cookie across 2 sites");
    // Expand then collapse
    fireEvent.click(groupExpandBtn);
    expect(screen.getByText("app.example.com")).toBeTruthy();
    fireEvent.click(groupExpandBtn);
    expect(screen.queryByText("app.example.com")).toBeNull();

    // Toggle group checkbox (selects both example.com and app.example.com)
    const groupCheckbox = screen.getByLabelText("example.com and 1 sites under it") as HTMLInputElement;
    expect(groupCheckbox.checked).toBe(false);
    fireEvent.click(groupCheckbox);
    expect(groupCheckbox.checked).toBe(true);
    expect(screen.getByText("3 of 4 selected")).toBeTruthy();

    // Expand and uncheck one child -> group becomes indeterminate ("some")
    fireEvent.click(groupExpandBtn);
    const appCheckbox = screen.getByLabelText("app.example.com") as HTMLInputElement;
    fireEvent.click(appCheckbox);
    expect(groupCheckbox.indeterminate).toBe(true);
    expect(screen.getByText("2 of 4 selected")).toBeTruthy();

    // Check child back -> adds back to selected
    fireEvent.click(appCheckbox);
    expect(groupCheckbox.checked).toBe(true);

    // Toggle standalone domain (solo.org) off and on
    const soloCheckbox = screen.getByLabelText("solo.org") as HTMLInputElement;
    expect(soloCheckbox.checked).toBe(true);
    fireEvent.click(soloCheckbox);
    expect(soloCheckbox.checked).toBe(false);

    // Filter sites: auto-expands matching groups, shows empty filter message when no match
    const filterInput = screen.getByLabelText("Filter sites");
    fireEvent.change(filterInput, { target: { value: "nomatch-xyz" } });
    expect(screen.getByText("No sites match that filter.")).toBeTruthy();

    fireEvent.change(filterInput, { target: { value: "other" } });
    expect(screen.getByText("other.net")).toBeTruthy();
    fireEvent.change(filterInput, { target: { value: "" } });

    // Select all -> all checked, then unselect all -> none checked, import disabled
    fireEvent.click(selectAll);
    expect(selectAll.checked).toBe(true);
    expect(screen.getByText("4 of 4 selected")).toBeTruthy();
    fireEvent.click(selectAll);
    expect(selectAll.checked).toBe(false);
    expect((screen.getByRole("button", { name: "Import" }) as HTMLButtonElement).disabled).toBe(true);

    // Select all again and import
    fireEvent.click(selectAll);
    fireEvent.click(screen.getByRole("button", { name: "Import" }));

    await waitFor(() => {
      expect(props.onImported).toHaveBeenCalledWith(4);
    });
    expect(mockImportCookies).toHaveBeenCalledWith("ch-1", "chrome-default", expect.arrayContaining(["example.com", "app.example.com", "solo.org", "other.net"]));
    expect(localStorage.getItem("cookieImportSource:ch-1")).toBe("chrome-default");
    expect(JSON.parse(localStorage.getItem("cookieImportDomains:ch-1:chrome-default")!)).toHaveLength(4);
  });

  it("handles Back button, switching profiles, empty profile domains, and import error", async () => {
    mockListCookieSources.mockResolvedValue(SOURCES);
    mockImportCookies.mockRejectedValue(new Error("permission denied"));

    renderDialog();
    const select = (await screen.findByLabelText("Browser profile")) as HTMLSelectElement;

    // Switch to Firefox profile with 0 domains
    fireEvent.change(select, { target: { value: "ff-main" } });
    fireEvent.click(screen.getByRole("button", { name: "Choose sites" }));
    expect(screen.getByText("No cookies in this profile.")).toBeTruthy();

    // Go back to consent step and switch to Edge profile (multi-cookie group count > 1)
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    fireEvent.change(screen.getByLabelText("Browser profile"), { target: { value: "edge-work" } });
    fireEvent.click(screen.getByRole("button", { name: "Choose sites" }));

    const corpBtn = screen.getByRole("button", { name: /corp\.io/ });
    expect(corpBtn.getAttribute("title")).toBe("5 cookies across 2 sites");

    // Select group and trigger failed import
    fireEvent.click(screen.getByLabelText("corp.io and 1 sites under it"));
    fireEvent.click(screen.getByRole("button", { name: "Import" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe("permission denied");
  });

  it("handles source becoming undefined while on the sites step when channel changes", async () => {
    mockListCookieSources.mockResolvedValueOnce(SOURCES).mockResolvedValueOnce([]);
    const { rerender } = renderDialog({ channelId: "ch-1" });
    await screen.findByLabelText("Browser profile");

    fireEvent.click(screen.getByRole("button", { name: "Choose sites" }));
    expect(screen.getByText(/Google Chrome/)).toBeTruthy();

    rerender(<CookieImportDialog channelId="ch-2" onClose={vi.fn()} onImported={vi.fn()} />);
    await waitFor(() => {
      expect(screen.getByText("No cookies in this profile.")).toBeTruthy();
    });
  });
});
