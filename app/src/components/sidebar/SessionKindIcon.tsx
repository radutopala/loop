import { useTheme } from "../../ThemeContext";
import type { Channel } from "../../types";
import { isTaskThread } from "./sessions";

/**
 * The icons that set a session apart in the sidebar: # for a channel, a
 * branch for a worktree thread, a clock for a task thread, a return arrow for
 * an ephemeral one. A plain thread gets a speech bubble only with
 * showPlainThread: the tree marks threads by indent instead, Recent can't.
 */
export function SessionKindIcon({ channel, showPlainThread = false }: { channel: Channel; showPlainThread?: boolean }) {
  const { colors } = useTheme();
  const isEphemeral = channel.name.startsWith("[ephemeral] ");
  const isTask = isTaskThread(channel);
  const isPlainThread = !!channel.parent_id && !channel.worktree && !isTask && !isEphemeral;
  return (
    <>
      {showPlainThread && isPlainThread && (
        <svg
          data-testid="session-kind-thread"
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
          <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
        </svg>
      )}
      {!channel.parent_id && (
        <span data-testid="session-kind-channel" style={{ color: colors.textDim, flexShrink: 0, fontSize: 13, lineHeight: 1 }}>
          #
        </span>
      )}
      {channel.worktree && (
        <svg
          data-testid="session-kind-worktree"
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
          data-testid="session-kind-task"
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
          data-testid="session-kind-ephemeral"
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
