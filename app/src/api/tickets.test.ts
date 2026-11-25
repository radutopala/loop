import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch, unreadable } from "../test/hooks-c-api-fetch";
import { addTicketNote, assignTicket, createTicket, deleteTicket, fetchTicket, fetchTickets, updateTicket, updateTicketStatus } from "./tickets";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchTickets", () => {
  it("lists a dir's tickets without filters", async () => {
    fetchMock.mockResolvedValue(json([{ id: "t-1" }]));
    await expect(fetchTickets("/w")).resolves.toEqual([{ id: "t-1" }]);
    expect(sent(fetchMock).url).toBe(`${API}/api/tickets?dir=%2Fw`);
  });

  it("passes every filter", async () => {
    fetchMock.mockResolvedValue(json([]));
    await fetchTickets("/w", { status: "open", assignee: "me", tag: "ui", type: "bug", sort: "priority", reverse: true });
    expect(sent(fetchMock).url).toBe(`${API}/api/tickets?dir=%2Fw&status=open&assignee=me&tag=ui&type=bug&sort=priority&reverse=true`);
  });

  it("leaves out empty filters", async () => {
    fetchMock.mockResolvedValue(json([]));
    await fetchTickets("/w", { status: "", reverse: false });
    expect(sent(fetchMock).url).toBe(`${API}/api/tickets?dir=%2Fw`);
  });
});

describe("ticket calls", () => {
  it("fetches one ticket", async () => {
    fetchMock.mockResolvedValue(json({ id: "t/1" }));
    await expect(fetchTicket("t/1", "/w")).resolves.toEqual({ id: "t/1" });
    expect(sent(fetchMock).url).toBe(`${API}/api/tickets/t%2F1?dir=%2Fw`);
  });

  it("creates a ticket", async () => {
    fetchMock.mockResolvedValue(json({ id: "t-2" }));
    await expect(createTicket({ dir: "/w", title: "x", tags: ["a"] })).resolves.toEqual({ id: "t-2" });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/tickets`, method: "POST", body: { dir: "/w", title: "x", tags: ["a"] } });
    expect(sent(fetchMock).headers.get("Content-Type")).toBe("application/json");
  });

  it("updates the status", async () => {
    fetchMock.mockResolvedValue(json({ id: "t-1", status: "closed" }));
    await expect(updateTicketStatus("t-1", "closed", "/w")).resolves.toEqual({ id: "t-1", status: "closed" });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/tickets/t-1`, method: "PATCH", body: { dir: "/w", status: "closed" } });
  });

  it("updates fields", async () => {
    fetchMock.mockResolvedValue(json({ id: "t-1" }));
    await updateTicket("t-1", { dir: "/w", title: "y", deps: ["t-2"] });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/tickets/t-1`, method: "PATCH", body: { dir: "/w", title: "y", deps: ["t-2"] } });
  });

  it("adds a note", async () => {
    fetchMock.mockResolvedValue(json({ id: "t-1", notes: [{ timestamp: "x", content: "n" }] }));
    await addTicketNote("t-1", { dir: "/w", content: "n" });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/tickets/t-1/notes`, method: "POST", body: { dir: "/w", content: "n" } });
  });

  it("deletes a ticket", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await deleteTicket("t-1", "/w");
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/tickets/t-1?dir=%2Fw`, method: "DELETE" });
  });

  it("assigns a ticket", async () => {
    fetchMock.mockResolvedValue(json({ thread_id: "th", worktree_path: "/wt" }));
    await expect(assignTicket("t-1", { dir: "/w", channel_id: "c1", branch: "b" })).resolves.toEqual({ thread_id: "th", worktree_path: "/wt" });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/tickets/t-1/assign`, method: "POST", body: { dir: "/w", channel_id: "c1", branch: "b" } });
  });

  it.each([
    ["fetchTickets", () => fetchTickets("/w"), "Failed to fetch tickets"],
    ["fetchTicket", () => fetchTicket("t", "/w"), "Failed to fetch ticket"],
    ["createTicket", () => createTicket({ dir: "/w", title: "" }), "Failed to create ticket"],
    ["updateTicketStatus", () => updateTicketStatus("t", "open", "/w"), "Failed to update ticket"],
    ["updateTicket", () => updateTicket("t", { dir: "/w" }), "Failed to update ticket"],
    ["addTicketNote", () => addTicketNote("t", { dir: "/w", content: "" }), "Failed to add note"],
    ["deleteTicket", () => deleteTicket("t", "/w"), "Failed to delete ticket"],
    ["assignTicket", () => assignTicket("t", { dir: "/w", channel_id: "c" }), "Failed to assign ticket"],
  ])("%s throws the server's text, else the action and status text", async (_name, run, action) => {
    fetchMock.mockResolvedValueOnce(fail(400, "Bad Request", " tk failed \n"));
    await expect(run()).rejects.toThrow(/^tk failed$/);
    fetchMock.mockResolvedValueOnce(fail(400, "Bad Request"));
    await expect(run()).rejects.toThrow(`${action}: Bad Request`);
  });

  it("falls back to the status text when the body can't be read", async () => {
    fetchMock.mockResolvedValue(unreadable(500, "boom"));
    await expect(deleteTicket("t", "/w")).rejects.toThrow("Failed to delete ticket: boom");
  });
});
