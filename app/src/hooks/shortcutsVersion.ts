import { createContext } from "react";

/**
 * Bumped when the channel's prompt or bash shortcuts change under the
 * pickers that list them (the Learn pane applied a shortcut proposal), so
 * they fetch them again. Provided by the workspace layout; 0 elsewhere.
 */
export const ShortcutsVersionContext = createContext(0);
