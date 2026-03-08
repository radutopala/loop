import type { Channel } from "../types";
import { colors } from "../theme";

interface ThreadItemProps {
  thread: Channel;
  selected: boolean;
  onSelect: (id: string) => void;
}

export function ThreadItem({ thread, selected, onSelect }: ThreadItemProps) {
  return (
    <button
      onClick={() => onSelect(thread.id)}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 6,
        width: "100%",
        padding: "4px 12px 4px 32px",
        border: "none",
        background: selected ? colors.selectedBg : "transparent",
        color: selected ? colors.textLight : colors.textDim,
        fontSize: 12,
        textAlign: "left",
        cursor: "pointer",
        borderRadius: 4,
      }}
    >
      <span
        style={{
          width: 5,
          height: 5,
          borderRadius: "50%",
          backgroundColor: thread.active ? colors.active : colors.textDisabled,
          flexShrink: 0,
        }}
      />
      <span
        style={{
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {thread.name}
      </span>
    </button>
  );
}
