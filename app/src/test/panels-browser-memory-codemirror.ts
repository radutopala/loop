// jsdom's Range has no layout methods, so CodeMirror's measure pass throws
// "getClientRects is not a function" from its animation frame. This installs
// empty-rect versions for a test and returns a function that removes them.
export function installRangeRects(): () => void {
  const proto = Range.prototype as unknown as Record<string, unknown>;
  const added: string[] = [];
  const empty = { x: 0, y: 0, width: 0, height: 0, top: 0, right: 0, bottom: 0, left: 0, toJSON: () => ({}) };
  if (!("getClientRects" in proto)) {
    proto.getClientRects = () => ({ length: 0, item: () => null, [Symbol.iterator]: [][Symbol.iterator] });
    added.push("getClientRects");
  }
  if (!("getBoundingClientRect" in proto)) {
    proto.getBoundingClientRect = () => empty;
    added.push("getBoundingClientRect");
  }
  return () => {
    for (const name of added) delete proto[name];
  };
}
