import { type Ref, useEffect, useImperativeHandle, useRef } from "react";
import { vi } from "vitest";
import { useNotificationsState } from "../notifications/useNotifications";

/** A stand-in for a heavy child of App: an empty div that tests find by
 *  testId, with the props read from the mock's calls. */
export function stub(testId: string) {
  return vi.fn((_props: any) => <div data-testid={testId} />);
}

/** A stand-in for a child App holds a ref to: it hands the ref handle, calls
 *  onMount once per mount, and shows the notifications state it sees. */
export function refStub(testId: string, handle: object, onMount: () => void) {
  return vi.fn((props: { ref?: Ref<unknown> }) => {
    useImperativeHandle(props.ref, () => handle);
    useEffect(() => onMount(), []);
    return <div data-testid={testId} data-notifications={JSON.stringify(useNotificationsState())} />;
  });
}

/** What the mocked useChatStateStore hands App: spies, kept for the
 *  component's life like the real store's callbacks. */
export function makeChatStore() {
  return {
    getState: vi.fn((_id: string) => undefined),
    saveState: vi.fn(),
    removeState: vi.fn(),
    isRunningMapRef: { current: new Map<string, string>() },
    unreadIdsRef: { current: new Set<string>() },
    pillsRef: { current: new Map() },
    unreadCount: 0,
    markRead: vi.fn(),
    markAllRead: vi.fn(),
    registerReviewView: vi.fn(),
    clearAskUserPill: vi.fn(),
    clearPlanPill: vi.fn(),
    syncPill: vi.fn(),
    subscribeChatEvents: vi.fn(),
    subscribeChannelEvents: vi.fn(),
    wsOpens: 0,
  };
}

export function useFakeChatStore(_opts: unknown) {
  const ref = useRef<ReturnType<typeof makeChatStore>>(null);
  ref.current ??= makeChatStore();
  return ref.current;
}
