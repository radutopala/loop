import { useCallback, useRef, useState } from "react";

/**
 * Tracks elapsed seconds since the timer was started.
 * Call `start()` to begin, `stop()` to freeze, and `reset()` to zero out.
 */
export function useElapsedTimer() {
  const [elapsed, setElapsed] = useState(0);
  const startTimeRef = useRef<number | null>(null);
  const timerRef = useRef<ReturnType<typeof setInterval>>(undefined);

  const start = useCallback(() => {
    if (startTimeRef.current) return; // already running
    startTimeRef.current = Date.now();
    timerRef.current = setInterval(() => {
      if (startTimeRef.current) {
        setElapsed(Math.floor((Date.now() - startTimeRef.current) / 1000));
      }
    }, 1000);
  }, []);

  const stop = useCallback(() => {
    clearInterval(timerRef.current);
  }, []);

  const reset = useCallback(() => {
    clearInterval(timerRef.current);
    startTimeRef.current = null;
    setElapsed(0);
  }, []);

  return { elapsed, start, stop, reset };
}
