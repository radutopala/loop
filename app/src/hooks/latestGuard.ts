/** Tracks overlapping requests so only the newest one's answer is used. Each
 *  call starts a request and returns a check that stays true until the next
 *  one starts. */
export function latestGuard(): () => () => boolean {
  let latest = 0;
  return () => {
    const mine = ++latest;
    return () => mine === latest;
  };
}
