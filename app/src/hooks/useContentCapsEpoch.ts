import { useSyncExternalStore } from "react";
import { contentCapsEpoch, subscribeContentCaps } from "../api/loopApi";

/** A counter that changes whenever every content link is forgotten (the
 *  daemon restarted or the token rotated). Add it to the deps of an effect
 *  that resolves a content link, so the view picks up a fresh one. */
export function useContentCapsEpoch(): number {
  return useSyncExternalStore(subscribeContentCaps, contentCapsEpoch);
}
