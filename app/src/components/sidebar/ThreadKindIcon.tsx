import { useTheme } from "../../ThemeContext";
import type { Channel } from "../../types";
import { isTaskThread } from "./sessions";

/**
 * The icons that set a thread apart in the sidebar: a branch for a worktree
 * thread, a clock for a task thread, a return arrow for an ephemeral one.
 * Renders nothing for a plain thread or a channel.
 */
export function ThreadKindIcon({ channel }: { channel: Channel }) {
  const { colors } = useTheme();
  const isEphemeral = channel.name.startsWith("[ephemeral] ");
  const isTask = isTaskThread(channel);
  return (
    <>
      {channel.worktree && (
        <svg
          data-testid="thread-kind-worktree"
          width="12"
          height="12"
          viewBox="0 0 24 24"
          fill="none"
          stroke={colors.active}
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          style={{ flexShrink: 0 }}
        >
          <circle cx="18" cy="18" r="3" />
          <circle cx="6" cy="6" r="3" />
          <path d="M6 21V9a9 9 0 0 0 9 9" />
        </svg>
      )}
      {isTask && !isEphemeral && (
        <svg
          data-testid="thread-kind-task"
          width="12"
          height="12"
          viewBox="0 0 24 24"
          fill="none"
          stroke={colors.textDim}
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          style={{ flexShrink: 0 }}
        >
          <circle cx="12" cy="12" r="10" />
          <polyline points="12 6 12 12 16 14" />
        </svg>
      )}
      {isEphemeral && (
        <svg
          data-testid="thread-kind-ephemeral"
          width="12"
          height="12"
          viewBox="0 0 24 24"
          fill="none"
          stroke={colors.textDim}
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          style={{ flexShrink: 0, opacity: 0.6 }}
        >
          <path d="M17.7 7.7A7.5 7.5 0 1 0 5 16.6" />
          <path d="M8 22l-4-4 4-4" />
        </svg>
      )}
    </>
  );
}
