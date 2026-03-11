import { useCallback, useRef, useState } from "react";

/**
 * Tracks elapsed seconds since the timer was started.
 * Call `start()` to begin, `stop()` to freeze, and `reset()` to zero out.
 */
export function useElapsedTimer() {
  const [elapsed, setElapsed] = useState(0);
  const startTimeRef = useRef<number | null>(null);
  const timerRef = useRef<ReturnType<typeof setInterval>>(undefined);

  const start = useCallback((fromTimestamp?: number) => {
    if (startTimeRef.current) return; // already running
    startTimeRef.current = fromTimestamp ?? Date.now();
    setElapsed(Math.floor((Date.now() - startTimeRef.current) / 1000));
    timerRef.current = setInterval(() => {
      if (startTimeRef.current) {
        setElapsed(Math.floor((Date.now() - startTimeRef.current) / 1000));
      }
    }, 1000);
  }, []);

  const stop = useCallback(() => {
    clearInterval(timerRef.current);
    timerRef.current = undefined;
  }, []);

  /** Resume the timer from where it was paused (keeps the original start time). */
  const resume = useCallback(() => {
    if (!startTimeRef.current || timerRef.current) return;
    timerRef.current = setInterval(() => {
      if (startTimeRef.current) {
        setElapsed(Math.floor((Date.now() - startTimeRef.current) / 1000));
      }
    }, 1000);
  }, []);

  const reset = useCallback(() => {
    clearInterval(timerRef.current);
    timerRef.current = undefined;
    startTimeRef.current = null;
    setElapsed(0);
  }, []);

  return { elapsed, start, stop, resume, reset };
}
