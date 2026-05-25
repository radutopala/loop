import { getApiUrl } from "./api";

export type BuiltinKind = "workflows" | "shortcuts";

export interface RestoreBuiltinsResponse {
  kind: BuiltinKind;
  added: string[];
  skipped: string[];
}

export async function restoreBuiltins(kind: BuiltinKind): Promise<RestoreBuiltinsResponse> {
  const resp = await fetch(`${getApiUrl()}/api/builtins/restore`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ kind }),
  });
  if (!resp.ok) throw new Error(await resp.text());
  return resp.json();
}
