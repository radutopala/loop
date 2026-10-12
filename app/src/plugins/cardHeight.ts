// How tall a plugin's card is: what its page asks for with ui.resize,
// within the bounds chat components keep to.
import { componentMaxHeight, componentMinHeight } from "../components/chat/componentFence";

/** A card's height until its page sizes it. */
export const cardInitialHeight = componentMinHeight * 2;

export function cardHeight(h: number): number {
  return Math.min(componentMaxHeight, Math.max(componentMinHeight, Math.ceil(h)));
}
