package browser

// InputEvent represents a user input event dispatched to Chrome via CDP.
type InputEvent struct {
	Type       string  `json:"type"` // "click", "mousemove", "scroll", "keypress", "typetext", "paste", "copy"
	X          float64 `json:"x,omitempty"`
	Y          float64 `json:"y,omitempty"`
	Button     string  `json:"button,omitempty"` // "left", "right", "middle"
	ClickCount int     `json:"click_count,omitempty"`
	DeltaX     float64 `json:"delta_x,omitempty"`
	DeltaY     float64 `json:"delta_y,omitempty"`
	Key        string  `json:"key,omitempty"`
	Text       string  `json:"text,omitempty"`
	// Modifiers is the CDP modifier bitmask: Alt=1, Ctrl=2, Meta=4, Shift=8.
	Modifiers int `json:"modifiers,omitempty"`
}
