import { getApiUrl } from "./api";

export interface QualityMetric {
  name: string;
  score: number;
  raw: number;
}

export interface QualityCitation {
  path: string;
  note?: string;
}

export interface QualityRule {
  name: string;
  severity: string;
  message: string;
  citations?: QualityCitation[];
}

export interface QualityRules {
  passed: QualityRule[];
  failed: QualityRule[];
}

export interface QualityFileTile {
  path: string;
  loc: number;
  deficit: number;
  metric_deficits: Record<string, number>;
  top_reason: string;
}

export interface QualityScanReport {
  dir_path: string;
  branch: string;
  signal: number;
  // previous_signal is the prior scan's headline for this (channel,
  // branch). Sentinel -1 means "no prior scan" — render the absolute
  // value, not a delta.
  previous_signal: number;
  geo_mean: number;
  file_count: number;
  parse_failed: number;
  scanned_at: string;
  metrics: QualityMetric[];
  tiles: QualityFileTile[];
  rules: QualityRules;
}

export interface QualitySnapshot {
  dir_path: string;
  branch: string;
  current_branch: string;
  branch_mismatch: boolean;
  signal: number;
  previous_signal: number;
  geo_mean: number;
  scanned_at: string;
  metrics: QualityMetric[];
  tiles: QualityFileTile[];
}

// NoPreviousSignal mirrors snapshot.NoPreviousValue on the wire.
export const NoPreviousSignal = -1;

export interface QualityScanResponse {
  status: "started" | "in_progress";
}

export interface QualityCyclesResponse {
  cycles: string[][];
  largest_cycle_size: number;
  total_nodes_in_cycles: number;
}

export interface QualityCouplingPair {
  file_a: string;
  file_b: string;
  co_change_count: number;
  jaccard: number;
  cross_module: boolean;
}

export interface QualityChurnHotspot {
  file: string;
  change_count: number;
  last_changed_at: string;
}

export interface QualityBusFactorRisk {
  file: string;
  sole_author: string;
  sole_author_ratio: number;
  total_commits: number;
  days_since_last_other_author: number;
  last_other_author_at?: string;
}

export interface QualityEvolutionResponse {
  commits_scanned: number;
  shallow_warning: boolean;
  coupling_pairs: QualityCouplingPair[];
  churn_hotspots: QualityChurnHotspot[];
  bus_factor: QualityBusFactorRisk[];
}

export type QualityWhatifOp = "delete" | "split";

export interface QualityMutation {
  op: QualityWhatifOp;
  path: string;
  parts?: number;
}

export interface QualityWhatifResponse {
  mutations: QualityMutation[];
  baseline_signal: number;
  predicted_signal: number;
  delta_signal: number;
  baseline_metrics: QualityMetric[];
  predicted_metrics: QualityMetric[];
}

export interface QualityC4Response {
  mermaid: string;
  component_count: number;
  edge_count: number;
}

export interface QualityComplexityFunction {
  path: string;
  name: string;
  start_line: number;
  cyclomatic: number;
  cognitive: number;
  max_nesting: number;
  param_count: number;
  loc: number;
  score: number;
}

export interface QualityComplexityResponse {
  score: number;
  raw: number;
  total_functions: number;
  over_threshold: number;
  histogram: Record<string, Record<string, number>>;
  functions: QualityComplexityFunction[];
  offset: number;
  limit: number;
  returned: number;
}

export interface QualityCloneMember {
  path: string;
  name: string;
  start_line: number;
  end_line: number;
  loc: number;
}

export interface QualityCloneCluster {
  members: QualityCloneMember[];
  loc: number;
  max_distance: number;
}

export interface QualityClonesResponse {
  score: number;
  raw: number;
  duplicated_loc: number;
  total_loc: number;
  cluster_count: number;
  clusters: QualityCloneCluster[];
  offset: number;
  limit: number;
  returned: number;
}

const base = (channelId: string) => `${getApiUrl()}/api/channels/${encodeURIComponent(channelId)}/quality`;

export async function triggerQualityScan(channelId: string): Promise<QualityScanResponse> {
  const res = await fetch(`${base(channelId)}/scan`, { method: "POST" });
  if (!res.ok) throw new Error(`Failed to trigger scan: ${res.statusText}`);
  return (await res.json()) as QualityScanResponse;
}

export async function fetchQualitySnapshot(channelId: string): Promise<QualitySnapshot | null> {
  const res = await fetch(`${base(channelId)}/snapshot`);
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`Failed to fetch snapshot: ${res.statusText}`);
  return (await res.json()) as QualitySnapshot;
}

export async function fetchQualityCycles(channelId: string): Promise<QualityCyclesResponse> {
  const res = await fetch(`${base(channelId)}/cycles`);
  if (!res.ok) throw new Error(await readErr(res, "cycles"));
  return (await res.json()) as QualityCyclesResponse;
}

export async function fetchQualityRules(channelId: string): Promise<QualityRules> {
  const res = await fetch(`${base(channelId)}/rules`);
  if (!res.ok) throw new Error(await readErr(res, "rules"));
  return (await res.json()) as QualityRules;
}

export async function fetchQualityEvolution(channelId: string): Promise<QualityEvolutionResponse | null> {
  const res = await fetch(`${base(channelId)}/evolution`);
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(await readErr(res, "evolution"));
  return (await res.json()) as QualityEvolutionResponse;
}

export async function fetchQualityC4(channelId: string): Promise<QualityC4Response> {
  const res = await fetch(`${base(channelId)}/c4`);
  if (!res.ok) throw new Error(await readErr(res, "c4"));
  return (await res.json()) as QualityC4Response;
}

export async function fetchQualityComplexity(channelId: string, opts: { limit?: number; offset?: number } = {}): Promise<QualityComplexityResponse> {
  const q = new URLSearchParams();
  if (opts.limit !== undefined) q.set("limit", String(opts.limit));
  if (opts.offset !== undefined) q.set("offset", String(opts.offset));
  const url = `${base(channelId)}/complexity${q.toString() ? `?${q}` : ""}`;
  const res = await fetch(url);
  if (!res.ok) throw new Error(await readErr(res, "complexity"));
  return (await res.json()) as QualityComplexityResponse;
}

export async function fetchQualityClones(channelId: string, opts: { limit?: number; offset?: number } = {}): Promise<QualityClonesResponse> {
  const q = new URLSearchParams();
  if (opts.limit !== undefined) q.set("limit", String(opts.limit));
  if (opts.offset !== undefined) q.set("offset", String(opts.offset));
  const url = `${base(channelId)}/clones${q.toString() ? `?${q}` : ""}`;
  const res = await fetch(url);
  if (!res.ok) throw new Error(await readErr(res, "clones"));
  return (await res.json()) as QualityClonesResponse;
}

export async function simulateQualityWhatif(channelId: string, mutations: QualityMutation[]): Promise<QualityWhatifResponse> {
  const res = await fetch(`${base(channelId)}/whatif`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ mutations }),
  });
  if (!res.ok) throw new Error(await readErr(res, "whatif"));
  return (await res.json()) as QualityWhatifResponse;
}

async function readErr(res: Response, label: string): Promise<string> {
  try {
    const text = (await res.text()).trim();
    if (text) return `${label}: ${text}`;
  } catch {
    // fall through
  }
  return `${label}: ${res.statusText}`;
}
