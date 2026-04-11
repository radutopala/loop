import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import type { WorkflowNodeDef, WorkflowNodeRun } from "../../api/workflows";

// ---------------------------------------------------------------------------
// Layout constants
// ---------------------------------------------------------------------------

const NODE_W = 140;
const NODE_H = 44;
const NODE_RX = 8;
const GAP_X = 60;
const GAP_Y = 20;
const PAD = 40;

// Canvas constants (matching CanvasLayout)
const DOT_SPACING = 20;
const MIN_ZOOM = 0.25;
const MAX_ZOOM = 3;
const MINIMAP_W = 120;
const MINIMAP_H = 80;

// ---------------------------------------------------------------------------
// Status → visual mapping
// ---------------------------------------------------------------------------

const STATUS_FILLS: Record<string, string> = {
  pending:  "#2a2a3a",
  running:  "#1e2a4a",
  success:  "#1a3a2a",
  failed:   "#3a1a1a",
  skipped:  "#2a2a2a",
  paused:   "#3a2e1a",
};

const STATUS_STROKES: Record<string, string> = {
  pending:  "#555",
  running:  "#818cf8",
  success:  "#34d399",
  failed:   "#ef4444",
  skipped:  "#6b7280",
  paused:   "#fbbf24",
};

const STATUS_ICONS: Record<string, string> = {
  pending:  "\u25CB",
  running:  "\u25CF",
  success:  "\u2713",
  failed:   "\u2717",
  skipped:  "\u23ED",
  paused:   "\u23F8",
};

const TYPE_BADGES: Record<string, string> = {
  prompt:   "P",
  bash:     "B",
  loop:     "L",
  approval: "A",
};

// ---------------------------------------------------------------------------
// Simple left-to-right layered layout (topological)
// ---------------------------------------------------------------------------

interface LayoutNode {
  id: string;
  layer: number;
  index: number;
  x: number;
  y: number;
  def: WorkflowNodeDef;
  run?: WorkflowNodeRun;
}

interface LayoutEdge {
  from: LayoutNode;
  to: LayoutNode;
}

function computeLayout(
  defs: WorkflowNodeDef[],
  runs: WorkflowNodeRun[],
): { nodes: LayoutNode[]; edges: LayoutEdge[]; width: number; height: number } {
  const effectiveDefs = defs.length > 0 ? defs : runs.map((r) => ({
    id: r.node_id,
    type: "bash" as const,
    depends_on: [] as string[],
  }));

  if (effectiveDefs.length === 0) return { nodes: [], edges: [], width: 0, height: 0 };

  const runMap = new Map<string, WorkflowNodeRun>();
  for (const r of runs) runMap.set(r.node_id, r);

  const defMap = new Map<string, WorkflowNodeDef>();
  for (const d of effectiveDefs) defMap.set(d.id, d);

  const layers = new Map<string, number>();

  function getLayer(id: string, visited: Set<string>): number {
    if (layers.has(id)) return layers.get(id)!;
    if (visited.has(id)) return 0;
    visited.add(id);
    const def = defMap.get(id);
    const deps = def?.depends_on ?? [];
    if (deps.length === 0) {
      layers.set(id, 0);
      return 0;
    }
    let maxParent = 0;
    for (const dep of deps) {
      maxParent = Math.max(maxParent, getLayer(dep, visited) + 1);
    }
    layers.set(id, maxParent);
    return maxParent;
  }

  for (const d of effectiveDefs) getLayer(d.id, new Set());

  const layerGroups = new Map<number, WorkflowNodeDef[]>();
  for (const d of effectiveDefs) {
    const l = layers.get(d.id) ?? 0;
    if (!layerGroups.has(l)) layerGroups.set(l, []);
    layerGroups.get(l)!.push(d);
  }

  const maxLayer = Math.max(...layerGroups.keys(), 0);
  const layoutNodes: LayoutNode[] = [];
  const nodeMap = new Map<string, LayoutNode>();

  let maxNodesInLayer = 0;
  for (const [, group] of layerGroups) maxNodesInLayer = Math.max(maxNodesInLayer, group.length);

  for (let l = 0; l <= maxLayer; l++) {
    const group = layerGroups.get(l) ?? [];
    const totalHeight = group.length * NODE_H + (group.length - 1) * GAP_Y;
    const maxTotalHeight = maxNodesInLayer * NODE_H + (maxNodesInLayer - 1) * GAP_Y;
    const offsetY = (maxTotalHeight - totalHeight) / 2;

    for (let i = 0; i < group.length; i++) {
      const def = group[i]!;
      const x = PAD + l * (NODE_W + GAP_X);
      const y = PAD + offsetY + i * (NODE_H + GAP_Y);
      const node: LayoutNode = { id: def.id, layer: l, index: i, x, y, def, run: runMap.get(def.id) };
      layoutNodes.push(node);
      nodeMap.set(def.id, node);
    }
  }

  const layoutEdges: LayoutEdge[] = [];
  for (const d of effectiveDefs) {
    const to = nodeMap.get(d.id);
    if (!to) continue;
    for (const dep of d.depends_on ?? []) {
      const from = nodeMap.get(dep);
      if (from) layoutEdges.push({ from, to });
    }
  }

  const width = PAD * 2 + (maxLayer + 1) * NODE_W + maxLayer * GAP_X;
  const height = PAD * 2 + maxNodesInLayer * NODE_H + (maxNodesInLayer - 1) * GAP_Y;

  return { nodes: layoutNodes, edges: layoutEdges, width: Math.max(width, 200), height: Math.max(height, 100) };
}

// ---------------------------------------------------------------------------
// SVG edge path (cubic bezier, left-to-right)
// ---------------------------------------------------------------------------

function edgePath(from: LayoutNode, to: LayoutNode): string {
  const x1 = from.x + NODE_W;
  const y1 = from.y + NODE_H / 2;
  const x2 = to.x;
  const y2 = to.y + NODE_H / 2;
  const dx = (x2 - x1) * 0.5;
  return `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

interface WorkflowGraphProps {
  defs: WorkflowNodeDef[];
  nodeRuns: WorkflowNodeRun[];
  colors: ColorPalette;
  onNodeClick?: (nodeId: string) => void;
  expandedNodeId?: string | null;
}

export function WorkflowGraph({ defs, nodeRuns, colors, onNodeClick, expandedNodeId }: WorkflowGraphProps) {
  const [hoveredNode, setHoveredNode] = useState<string | null>(null);
  const [vp, setVp] = useState({ x: 0, y: 0, zoom: 1 });
  const vpRef = useRef(vp);
  vpRef.current = vp;
  const containerRef = useRef<HTMLDivElement>(null);
  const isPanning = useRef(false);
  const hasCentered = useRef(false);

  const { nodes, edges, width, height } = useMemo(
    () => computeLayout(defs, nodeRuns),
    [defs, nodeRuns],
  );

  // Auto-center graph on first render or when nodes change.
  useEffect(() => {
    if (nodes.length === 0) return;
    const el = containerRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const fitZoom = Math.min(1.2, rect.width * 0.9 / width, rect.height * 0.9 / height);
    const z = Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, fitZoom));
    const cx = (rect.width - width * z) / 2;
    const cy = (rect.height - height * z) / 2;
    setVp({ x: cx, y: cy, zoom: z });
    hasCentered.current = true;
  }, [nodes.length, width, height]);

  // --- Pan: mouse drag ---
  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    // Only pan on background (left button), not on nodes.
    if (e.button !== 0) return;
    const target = e.target as HTMLElement;
    if (target.closest?.("g[data-node]")) return;
    e.preventDefault();
    isPanning.current = true;
    const startX = e.clientX - vpRef.current.x;
    const startY = e.clientY - vpRef.current.y;
    const onMove = (ev: MouseEvent) => {
      if (!isPanning.current) return;
      setVp((v) => ({ ...v, x: ev.clientX - startX, y: ev.clientY - startY }));
    };
    const onUp = () => {
      isPanning.current = false;
      document.removeEventListener("mousemove", onMove);
      document.removeEventListener("mouseup", onUp);
    };
    document.addEventListener("mousemove", onMove);
    document.addEventListener("mouseup", onUp);
  }, []);

  // --- Zoom: scroll wheel (cursor-anchored) ---
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const handler = (e: WheelEvent) => {
      const v = vpRef.current;
      if (e.ctrlKey || e.metaKey) {
        e.preventDefault();
        const rect = el.getBoundingClientRect();
        const cx = e.clientX - rect.left;
        const cy = e.clientY - rect.top;
        const delta = -e.deltaY * 0.003;
        const newZoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, v.zoom * (1 + delta)));
        const scale = newZoom / v.zoom;
        setVp({ x: cx - (cx - v.x) * scale, y: cy - (cy - v.y) * scale, zoom: newZoom });
      } else {
        // Scroll to pan.
        setVp((prev) => ({ ...prev, x: prev.x - e.deltaX, y: prev.y - e.deltaY }));
      }
    };
    el.addEventListener("wheel", handler, { passive: false });
    return () => el.removeEventListener("wheel", handler);
  }, []);

  // Zoom helper for buttons.
  const zoomTo = useCallback((newZoom: number) => {
    const el = containerRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const cx = rect.width / 2;
    const cy = rect.height / 2;
    const z = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, newZoom));
    const scale = z / vpRef.current.zoom;
    setVp((v) => ({
      x: cx - (cx - v.x) * scale,
      y: cy - (cy - v.y) * scale,
      zoom: z,
    }));
  }, []);

  if (nodes.length === 0) {
    return (
      <div style={{ padding: 16, color: colors.textDim, fontSize: 12, textAlign: "center" }}>
        No nodes defined
      </div>
    );
  }

  const expandedNode = expandedNodeId ? nodes.find(n => n.id === expandedNodeId) : null;
  const expandedRun = expandedNode?.run;

  // Dot grid.
  const dotSpacing = DOT_SPACING * vp.zoom;
  const dotSize = Math.max(1, vp.zoom) * 1.5;
  const dotColor = "rgba(255,255,255,0.10)";

  // Minimap: compute viewport in world coords.
  const containerRect = containerRef.current?.getBoundingClientRect();
  const containerW = containerRect?.width ?? 600;
  const containerH = containerRect?.height ?? 400;
  const vpWorldX = -vp.x / vp.zoom;
  const vpWorldY = -vp.y / vp.zoom;
  const vpWorldW = containerW / vp.zoom;
  const vpWorldH = containerH / vp.zoom;
  const worldMinX = Math.min(0, vpWorldX);
  const worldMinY = Math.min(0, vpWorldY);
  const worldMaxX = Math.max(width, vpWorldX + vpWorldW);
  const worldMaxY = Math.max(height, vpWorldY + vpWorldH);
  const worldW = worldMaxX - worldMinX || 1;
  const worldH = worldMaxY - worldMinY || 1;
  const mmScale = Math.min(MINIMAP_W / worldW, MINIMAP_H / worldH);
  const mmW = worldW * mmScale;
  const mmH = worldH * mmScale;

  return (
    <div style={{ display: "flex", flexDirection: "column", flex: 1, overflow: "hidden" }}>
      {/* Canvas area */}
      <div
        ref={containerRef}
        onMouseDown={handleMouseDown}
        style={{
          flex: 1,
          minHeight: 80,
          position: "relative",
          overflow: "hidden",
          cursor: isPanning.current ? "grabbing" : "grab",
          backgroundColor: "rgba(0,0,0,0.3)",
          backgroundImage: `radial-gradient(circle, ${dotColor} ${dotSize}px, transparent ${dotSize}px)`,
          backgroundSize: `${dotSpacing}px ${dotSpacing}px`,
          backgroundPosition: `${vp.x % dotSpacing}px ${vp.y % dotSpacing}px`,
        }}
      >
        {/* Transform layer */}
        <div
          style={{
            position: "absolute",
            top: 0,
            left: 0,
            transformOrigin: "0 0",
            transform: `translate(${vp.x}px, ${vp.y}px) scale(${vp.zoom})`,
            willChange: "transform",
          }}
        >
          <svg
            width={width}
            height={height}
            viewBox={`0 0 ${width} ${height}`}
            style={{ display: "block", overflow: "visible" }}
          >
            {/* Arrowhead markers */}
            <defs>
              <marker id="wf-arrow" markerWidth="8" markerHeight="6" refX="8" refY="3" orient="auto">
                <polygon points="0 0, 8 3, 0 6" fill={colors.textDim} />
              </marker>
              {Object.entries(STATUS_STROKES).map(([status, color]) => (
                <marker key={status} id={`wf-arrow-${status}`} markerWidth="8" markerHeight="6" refX="8" refY="3" orient="auto">
                  <polygon points="0 0, 8 3, 0 6" fill={color} />
                </marker>
              ))}
            </defs>

            {/* Edges */}
            {edges.map((edge, i) => {
              const toStatus = edge.to.run?.status;
              const edgeColor = toStatus ? (STATUS_STROKES[toStatus] ?? colors.textDim) : colors.border;
              const isActive = toStatus === "running" || toStatus === "success" || toStatus === "failed" || toStatus === "paused";
              return (
                <path
                  key={i}
                  d={edgePath(edge.from, edge.to)}
                  fill="none"
                  stroke={edgeColor}
                  strokeWidth={isActive ? 2 : 1}
                  strokeDasharray={toStatus === "pending" || toStatus === "skipped" || !toStatus ? "4 3" : undefined}
                  markerEnd={toStatus ? `url(#wf-arrow-${toStatus})` : "url(#wf-arrow)"}
                  opacity={isActive ? 1 : 0.5}
                />
              );
            })}

            {/* Nodes */}
            {nodes.map((node) => {
              const status = node.run?.status ?? "pending";
              const fill = STATUS_FILLS[status] ?? STATUS_FILLS.pending;
              const stroke = STATUS_STROKES[status] ?? STATUS_STROKES.pending;
              const icon = STATUS_ICONS[status] ?? STATUS_ICONS.pending;
              const typeBadge = TYPE_BADGES[node.def.type] ?? "?";
              const isHovered = hoveredNode === node.id;
              const isExpanded = expandedNodeId === node.id;
              const isRunning = status === "running";

              return (
                <g
                  key={node.id}
                  data-node={node.id}
                  onClick={() => onNodeClick?.(node.id)}
                  onMouseEnter={() => setHoveredNode(node.id)}
                  onMouseLeave={() => setHoveredNode(null)}
                  style={{ cursor: "pointer" }}
                >
                  {isRunning && (
                    <rect
                      x={node.x - 2}
                      y={node.y - 2}
                      width={NODE_W + 4}
                      height={NODE_H + 4}
                      rx={NODE_RX + 2}
                      fill="none"
                      stroke={stroke}
                      strokeWidth={1}
                      opacity={0.3}
                    >
                      <animate attributeName="opacity" values="0.3;0.7;0.3" dur="2s" repeatCount="indefinite" />
                    </rect>
                  )}

                  <rect
                    x={node.x}
                    y={node.y}
                    width={NODE_W}
                    height={NODE_H}
                    rx={NODE_RX}
                    fill={fill}
                    stroke={isExpanded ? colors.active : isHovered ? colors.textLight : stroke}
                    strokeWidth={isExpanded ? 2 : isHovered ? 1.5 : 1}
                  />

                  <text
                    x={node.x + 12}
                    y={node.y + NODE_H / 2 + 1}
                    fill={stroke}
                    fontSize={12}
                    fontWeight={700}
                    textAnchor="middle"
                    dominantBaseline="middle"
                  >
                    {icon}
                  </text>

                  <text
                    x={node.x + 24}
                    y={node.y + NODE_H / 2 + 1}
                    fill={colors.textLight}
                    fontSize={11}
                    fontFamily={fonts.mono}
                    fontWeight={600}
                    dominantBaseline="middle"
                  >
                    {node.id.length > 12 ? node.id.slice(0, 11) + "\u2026" : node.id}
                  </text>

                  <rect
                    x={node.x + NODE_W - 22}
                    y={node.y + 4}
                    width={18}
                    height={14}
                    rx={3}
                    fill={stroke}
                    opacity={0.25}
                  />
                  <text
                    x={node.x + NODE_W - 13}
                    y={node.y + 12}
                    fill={stroke}
                    fontSize={8}
                    fontWeight={700}
                    textAnchor="middle"
                    dominantBaseline="middle"
                  >
                    {typeBadge}
                  </text>

                  {node.run && node.run.attempt > 1 && (
                    <>
                      <rect
                        x={node.x + NODE_W - 30}
                        y={node.y + NODE_H - 16}
                        width={26}
                        height={12}
                        rx={3}
                        fill="#fbbf2440"
                      />
                      <text
                        x={node.x + NODE_W - 17}
                        y={node.y + NODE_H - 10}
                        fill="#fbbf24"
                        fontSize={7}
                        fontWeight={700}
                        textAnchor="middle"
                        dominantBaseline="middle"
                      >
                        R:{node.run.attempt - 1}
                      </text>
                    </>
                  )}

                  {node.run?.started_at && (
                    <text
                      x={node.x + 6}
                      y={node.y + NODE_H - 6}
                      fill={colors.textDim}
                      fontSize={8}
                      fontFamily={fonts.mono}
                    >
                      {elapsedShort(node.run.started_at, node.run.finished_at)}
                    </text>
                  )}
                </g>
              );
            })}
          </svg>
        </div>

        {/* Minimap + zoom controls (bottom-right) */}
        <div style={{ position: "absolute", bottom: 8, right: 8, display: "flex", flexDirection: "column", alignItems: "flex-end", gap: 4 }}>
          {/* Minimap */}
          <div
            onClick={(e) => {
              const mmRect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
              const clickX = e.clientX - mmRect.left;
              const clickY = e.clientY - mmRect.top;
              const worldClickX = clickX / mmScale + worldMinX;
              const worldClickY = clickY / mmScale + worldMinY;
              setVp((v) => ({
                ...v,
                x: -(worldClickX - vpWorldW / 2) * v.zoom,
                y: -(worldClickY - vpWorldH / 2) * v.zoom,
              }));
            }}
            style={{
              width: mmW,
              height: mmH,
              backgroundColor: "rgba(0,0,0,0.6)",
              border: `1px solid ${colors.border}`,
              borderRadius: 4,
              overflow: "hidden",
              cursor: "pointer",
              position: "relative",
            }}
          >
            {/* Minimap nodes */}
            {nodes.map((node) => {
              const status = node.run?.status ?? "pending";
              const color = STATUS_STROKES[status] ?? "#555";
              return (
                <div
                  key={node.id}
                  style={{
                    position: "absolute",
                    left: (node.x - worldMinX) * mmScale,
                    top: (node.y - worldMinY) * mmScale,
                    width: NODE_W * mmScale,
                    height: NODE_H * mmScale,
                    backgroundColor: color,
                    opacity: 0.7,
                    borderRadius: 1,
                  }}
                />
              );
            })}
            {/* Viewport rectangle */}
            <div
              onClick={(e) => e.stopPropagation()}
              onMouseDown={(e) => {
                e.preventDefault();
                e.stopPropagation();
                const startX = e.clientX;
                const startY = e.clientY;
                const startVpX = vpRef.current.x;
                const startVpY = vpRef.current.y;
                const onMove = (ev: MouseEvent) => {
                  const dx = (ev.clientX - startX) / mmScale;
                  const dy = (ev.clientY - startY) / mmScale;
                  setVp((v) => ({ ...v, x: startVpX - dx * v.zoom, y: startVpY - dy * v.zoom }));
                };
                const onUp = () => {
                  document.removeEventListener("mousemove", onMove);
                  document.removeEventListener("mouseup", onUp);
                };
                document.addEventListener("mousemove", onMove);
                document.addEventListener("mouseup", onUp);
              }}
              style={{
                position: "absolute",
                left: (vpWorldX - worldMinX) * mmScale,
                top: (vpWorldY - worldMinY) * mmScale,
                width: vpWorldW * mmScale,
                height: vpWorldH * mmScale,
                border: `1.5px solid ${colors.active}`,
                backgroundColor: `${colors.active}22`,
                borderRadius: 2,
                cursor: "grab",
              }}
            />
          </div>
          {/* Zoom controls */}
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 2,
              backgroundColor: "rgba(0,0,0,0.6)",
              border: `1px solid ${colors.border}`,
              borderRadius: 4,
              padding: "1px 2px",
            }}
          >
            <button
              onClick={(e) => { e.stopPropagation(); zoomTo(vp.zoom / 1.3); }}
              title="Zoom out"
              style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: "2px 6px", fontSize: 12, lineHeight: 1, fontWeight: 700 }}
              onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; }}
              onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
            >
              −
            </button>
            <span
              onClick={(e) => { e.stopPropagation(); zoomTo(1); }}
              title="Reset zoom"
              style={{ fontSize: 9, color: colors.textDim, cursor: "pointer", padding: "0 4px", minWidth: 28, textAlign: "center" }}
            >
              {Math.round(vp.zoom * 100)}%
            </span>
            <button
              onClick={(e) => { e.stopPropagation(); zoomTo(vp.zoom * 1.3); }}
              title="Zoom in"
              style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: "2px 6px", fontSize: 12, lineHeight: 1, fontWeight: 700 }}
              onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; }}
              onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
            >
              +
            </button>
          </div>
        </div>
      </div>

      {/* Expanded node output panel below the graph */}
      {expandedNode && expandedRun && (
        <div style={{
          borderTop: `1px solid ${colors.border}`,
          padding: "8px 12px",
          flex: 1,
          overflowY: "auto",
          minHeight: 80,
        }}>
          <div style={{ display: "flex", alignItems: "center", gap: 6, marginBottom: 6 }}>
            <span style={{ color: STATUS_STROKES[expandedRun.status] ?? colors.textDim, fontWeight: 700, fontSize: 13 }}>
              {STATUS_ICONS[expandedRun.status] ?? "\u25CB"}
            </span>
            <span style={{ fontFamily: fonts.mono, fontWeight: 600, fontSize: 11, color: colors.textLight }}>
              {expandedNode.id}
            </span>
            <span style={{ fontSize: 10, color: colors.textDim, padding: "0 4px", borderRadius: 3, border: `1px solid ${colors.border}` }}>
              {expandedNode.def.type}
            </span>
            {expandedRun.started_at && (
              <span style={{ fontSize: 10, color: colors.textDim, marginLeft: "auto" }}>
                {elapsedShort(expandedRun.started_at, expandedRun.finished_at)}
              </span>
            )}
          </div>
          {expandedRun.error_text && (
            <div style={{ color: colors.error ?? "#ef4444", fontSize: 11, whiteSpace: "pre-wrap", marginBottom: 4 }}>
              {expandedRun.error_text}
            </div>
          )}
          {expandedRun.output && (
            <div style={{
              color: colors.textDim, fontSize: 11, whiteSpace: "pre-wrap",
              fontFamily: fonts.mono, background: colors.bg, padding: "6px 8px", borderRadius: 4,
            }}>
              {expandedRun.output}
            </div>
          )}
          {!expandedRun.output && !expandedRun.error_text && (
            <div style={{ color: colors.textDim, fontSize: 11 }}>No output</div>
          )}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function elapsedShort(start: string | null, end: string | null): string {
  if (!start) return "";
  const s = new Date(start).getTime();
  const e = end ? new Date(end).getTime() : Date.now();
  const secs = Math.max(0, Math.floor((e - s) / 1000));
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m`;
  return `${Math.floor(mins / 60)}h${mins % 60}m`;
}
