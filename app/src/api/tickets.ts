import { getApiUrl } from "./api";

async function throwIfNotOk(res: Response, action: string): Promise<void> {
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(body.trim() || `${action}: ${res.statusText}`);
  }
}

export interface Ticket {
  id: string;
  title: string;
  description?: string;
  status: "open" | "in_progress" | "closed";
  type?: string;
  priority: number;
  assignee?: string;
  tags: string[];
  deps: string[];
  links: string[];
  parent?: string;
  external_ref?: string;
  pr?: string;
  design?: string;
  acceptance?: string;
  created: string;
}

export async function fetchTickets(
  dir: string,
  filters?: { status?: string; assignee?: string; tag?: string; type?: string; sort?: string; reverse?: boolean },
): Promise<Ticket[]> {
  const params = new URLSearchParams({ dir });
  if (filters?.status) params.set("status", filters.status);
  if (filters?.assignee) params.set("assignee", filters.assignee);
  if (filters?.tag) params.set("tag", filters.tag);
  if (filters?.type) params.set("type", filters.type);
  if (filters?.sort) params.set("sort", filters.sort);
  if (filters?.reverse) params.set("reverse", "true");
  const res = await fetch(`${getApiUrl()}/api/tickets?${params}`);
  await throwIfNotOk(res, "Failed to fetch tickets");
  return res.json();
}

export async function fetchTicket(id: string, dir: string): Promise<Ticket> {
  const res = await fetch(`${getApiUrl()}/api/tickets/${encodeURIComponent(id)}?dir=${encodeURIComponent(dir)}`);
  await throwIfNotOk(res, "Failed to fetch ticket");
  return res.json();
}

export async function createTicket(data: {
  dir: string;
  title: string;
  description?: string;
  type?: string;
  priority?: number;
  assignee?: string;
  tags?: string[];
  parent?: string;
  external_ref?: string;
  pr?: string;
  design?: string;
  acceptance?: string;
}): Promise<Ticket> {
  const res = await fetch(`${getApiUrl()}/api/tickets`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  await throwIfNotOk(res, "Failed to create ticket");
  return res.json();
}

export async function updateTicketStatus(id: string, status: string, dir: string): Promise<Ticket> {
  const res = await fetch(`${getApiUrl()}/api/tickets/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ dir, status }),
  });
  await throwIfNotOk(res, "Failed to update ticket");
  return res.json();
}

export async function updateTicket(
  id: string,
  data: {
    dir: string;
    title?: string;
    description?: string;
    type?: string;
    priority?: number;
    assignee?: string;
    tags?: string[];
    deps?: string[];
    parent?: string;
    external_ref?: string;
    pr?: string;
    design?: string;
    acceptance?: string;
  },
): Promise<Ticket> {
  const res = await fetch(`${getApiUrl()}/api/tickets/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  await throwIfNotOk(res, "Failed to update ticket");
  return res.json();
}

export async function deleteTicket(id: string, dir: string): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/tickets/${encodeURIComponent(id)}?dir=${encodeURIComponent(dir)}`, {
    method: "DELETE",
  });
  await throwIfNotOk(res, "Failed to delete ticket");
}

export async function assignTicket(
  id: string,
  data: { dir: string; channel_id: string; branch?: string },
): Promise<{ thread_id: string; worktree_path: string }> {
  const res = await fetch(`${getApiUrl()}/api/tickets/${encodeURIComponent(id)}/assign`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  await throwIfNotOk(res, "Failed to assign ticket");
  return res.json();
}
