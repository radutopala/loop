import { useCallback, useEffect, useState } from "react";
import type { DropPosition } from "./types";

const DRAG_START_EVENT = "layout-drag-start";
const DRAG_END_EVENT = "layout-drag-end";

export function emitLayoutDragStart() {
  document.dispatchEvent(new Event(DRAG_START_EVENT));
}
export function emitLayoutDragEnd() {
  document.dispatchEvent(new Event(DRAG_END_EVENT));
}

const DRAG_MIME = "application/x-panel-drag";

interface DropZoneOverlayProps {
  leafId: string;
  headerHeight: number;
  onDrop: (dragId: string, dropId: string, position: DropPosition) => void;
}

export function DropZoneOverlay({ leafId, headerHeight, onDrop }: DropZoneOverlayProps) {
  const [activeZone, setActiveZone] = useState<DropPosition | null>(null);
  const [isDragOver, setIsDragOver] = useState(false);
  const [isDragging, setIsDragging] = useState(false);

  useEffect(() => {
    const onStart = () => setIsDragging(true);
    const onEnd = () => {
      setIsDragging(false);
      setIsDragOver(false);
      setActiveZone(null);
    };
    document.addEventListener(DRAG_START_EVENT, onStart);
    document.addEventListener(DRAG_END_EVENT, onEnd);
    document.addEventListener("dragend", onEnd);
    document.addEventListener("drop", onEnd);
    return () => {
      document.removeEventListener(DRAG_START_EVENT, onStart);
      document.removeEventListener(DRAG_END_EVENT, onEnd);
      document.removeEventListener("dragend", onEnd);
      document.removeEventListener("drop", onEnd);
    };
  }, []);

  const getDropPosition = useCallback((e: React.DragEvent<HTMLDivElement>): DropPosition => {
    const rect = e.currentTarget.getBoundingClientRect();
    const x = (e.clientX - rect.left) / rect.width;
    const y = (e.clientY - rect.top) / rect.height;
    if (y < 0.25) return "top";
    if (y > 0.75) return "bottom";
    if (x < 0.25) return "left";
    if (x > 0.75) return "right";
    return "center";
  }, []);

  const handleDragOver = useCallback(
    (e: React.DragEvent<HTMLDivElement>) => {
      if (!e.dataTransfer.types.includes(DRAG_MIME)) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
      setIsDragOver(true);
      setActiveZone(getDropPosition(e));
    },
    [getDropPosition],
  );

  const handleDragLeave = useCallback((e: React.DragEvent<HTMLDivElement>) => {
    if (e.currentTarget.contains(e.relatedTarget as Node)) return;
    setIsDragOver(false);
    setActiveZone(null);
  }, []);

  const handleDrop = useCallback(
    (e: React.DragEvent<HTMLDivElement>) => {
      e.preventDefault();
      const dragId = e.dataTransfer.getData(DRAG_MIME);
      if (dragId && dragId !== leafId) {
        onDrop(dragId, leafId, getDropPosition(e));
      }
      setIsDragOver(false);
      setActiveZone(null);
      emitLayoutDragEnd();
    },
    [leafId, onDrop, getDropPosition],
  );

  const active = isDragging;

  return (
    <div
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
      style={{
        position: "absolute",
        top: headerHeight,
        left: 0,
        right: 0,
        bottom: 0,
        zIndex: active ? 10 : -1,
        pointerEvents: active ? "auto" : "none",
      }}
    >
      {isDragOver && activeZone && (
        <div
          style={{
            position: "absolute",
            ...(activeZone === "top"
              ? { top: 0, left: 0, right: 0, height: "50%" }
              : activeZone === "bottom"
                ? { bottom: 0, left: 0, right: 0, height: "50%" }
                : activeZone === "left"
                  ? { top: 0, left: 0, bottom: 0, width: "50%" }
                  : activeZone === "right"
                    ? { top: 0, right: 0, bottom: 0, width: "50%" }
                    : { top: 0, left: 0, right: 0, bottom: 0 }),
            backgroundColor: "rgba(96, 165, 250, 0.15)",
            border: "2px solid rgba(96, 165, 250, 0.5)",
            borderRadius: 4,
            pointerEvents: "none",
          }}
        />
      )}
    </div>
  );
}

export { DRAG_MIME };
