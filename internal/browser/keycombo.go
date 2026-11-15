package browser

import "strings"

// keyModifiers maps the modifier names a key combo can use to their CDP bits
// (Alt=1, Ctrl=2, Meta=4, Shift=8).
var keyModifiers = map[string]int{
	"alt": 1, "option": 1, "opt": 1,
	"ctrl": 2, "control": 2,
	"meta": 4, "cmd": 4, "command": 4, "super": 4, "win": 4,
	"shift": modShift,
}

// keyAliases maps other names for a key, lowercased, to the DOM key name
// Chrome expects. They cover xdotool's names, which computer-use agents send
// ("Return", "BackSpace", "Page_Down"), and common short forms.
var keyAliases = map[string]string{
	"return": "Enter", "enter": "Enter", "kp_enter": "Enter",
	"backspace": "Backspace",
	"tab":       "Tab",
	"escape":    "Escape", "esc": "Escape",
	"space":  " ",
	"pageup": "PageUp", "page_up": "PageUp", "prior": "PageUp",
	"pagedown": "PageDown", "page_down": "PageDown", "next": "PageDown",
	"end":  "End",
	"home": "Home",
	"left": "ArrowLeft", "arrowleft": "ArrowLeft",
	"up": "ArrowUp", "arrowup": "ArrowUp",
	"right": "ArrowRight", "arrowright": "ArrowRight",
	"down": "ArrowDown", "arrowdown": "ArrowDown",
	"delete": "Delete", "del": "Delete",
}

// ParseKeyCombo splits a combo such as "ctrl+a" or "Control+Shift+Tab" into
// the key and the CDP modifier bits of the names before it, and gives the key
// its DOM name. A single character, "+" included, is a key on its own, and a
// name that is neither a modifier nor an alias is kept as it is. Agents name
// keys this way; the browser pane sends DOM key names and modifier bits
// itself and does not go through it.
func ParseKeyCombo(combo string) (key string, modifiers int) {
	parts := strings.Split(combo, "+")
	if len(combo) <= 1 || len(parts) == 1 {
		return canonicalKey(combo), 0
	}
	key = parts[len(parts)-1]
	mods := parts[:len(parts)-1]
	if key == "" { // "ctrl++": the key is "+"
		key, mods = "+", parts[:len(parts)-2]
	}
	for _, m := range mods {
		bit, ok := keyModifiers[strings.ToLower(strings.TrimSpace(m))]
		if !ok {
			return canonicalKey(combo), 0
		}
		modifiers |= bit
	}
	return canonicalKey(strings.TrimSpace(key)), modifiers
}

// canonicalKey gives a key name its DOM spelling when it is a known alias.
func canonicalKey(key string) string {
	if len(key) <= 1 {
		return key
	}
	if k, ok := keyAliases[strings.ToLower(key)]; ok {
		return k
	}
	return key
}
