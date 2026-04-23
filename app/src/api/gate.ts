import { getApiUrl } from "./api";

export type GateDecision = "once" | "session" | "deny";

export async function resolveGateApproval(
  reqId: string,
  decision: GateDecision,
): Promise<void> {
  const res = await fetch(
    `${getApiUrl()}/api/gate/approvals/${encodeURIComponent(reqId)}`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ decision }),
    },
  );
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new Error(`Failed to resolve gate approval: ${text || res.statusText}`);
  }
}
