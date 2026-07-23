import type { GateApprovalRequestedData } from "../types";
import { getApiUrl } from "./api";

export type GateDecision = "once" | "session" | "deny";

/** One pending approval as returned by GET /api/gate/approvals. */
export interface PendingApproval extends GateApprovalRequestedData {
  container_id: string;
  channel_id: string;
}

/**
 * Snapshot the gate's in-flight approvals. Used on WS reconnect to
 * rehydrate the per-channel gateApprovals map and to reconcile the
 * electron-main dock-bouncer so stale req_ids are dropped.
 */
export async function listPendingApprovals(): Promise<PendingApproval[]> {
  const res = await fetch(`${getApiUrl()}/api/gate/approvals`);
  if (!res.ok) {
    throw new Error(`Failed to list gate approvals: ${res.statusText}`);
  }
  const body = (await res.json()) as { approvals?: PendingApproval[] };
  return body.approvals ?? [];
}

export async function resolveGateApproval(reqId: string, decision: GateDecision): Promise<void> {
  const res = await fetch(`${getApiUrl()}/api/gate/approvals/${encodeURIComponent(reqId)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ decision }),
  });
  if (res.status === 404) {
    // The request is gone — resolved elsewhere, timed out, or its container
    // exited. Callers treat this as "expired", not a failure.
    throw new GateApprovalGoneError();
  }
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new Error(`Failed to resolve gate approval: ${text || res.statusText}`);
  }
}

/** Thrown by resolveGateApproval when the request no longer exists (404). */
export class GateApprovalGoneError extends Error {
  constructor() {
    super("approval request no longer exists");
    this.name = "GateApprovalGoneError";
  }
}
