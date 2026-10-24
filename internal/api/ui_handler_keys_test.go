package api

import (
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/uibridge"
)

// The daemon reads a step with encoding/json, which matches keys case
// insensitively and keeps the last match; the window reads the step's keys as
// written. A step spelling a checked key two ways would have the daemon check
// one value and the window run the other, so it's refused.
func (s *UIHandlerSuite) TestStepKeysSpelledOneWay() {
	ts := s.asAgent()
	conn := s.dial("w1")
	s.waitVersion(1)
	ran := make(chan string, 8)
	go func() {
		for {
			var cmd uibridge.Message
			if conn.ReadJSON(&cmd) != nil {
				return
			}
			ran <- string(cmd.Steps)
			_ = conn.WriteJSON(uibridge.Message{Type: uibridge.MsgResult, ID: cmd.ID, Results: cmd.Steps})
		}
	}()

	tests := []struct {
		name  string
		steps string
		code  int
		want  string
	}{
		{"another project's channel behind its own", `[{"op":"select_channel","channel_id":"other","Channel_ID":"ch-1"}]`, http.StatusBadRequest, `step key "Channel_ID"`},
		{"upper case channel", `[{"op":"select_channel","CHANNEL_ID":"ch-1"}]`, http.StatusBadRequest, `step key "CHANNEL_ID"`},
		{"a select_channel hidden behind another op", `[{"op":"select_channel","channel_id":"ch-1"},{"op":"select_channel","Op":"set_tab","channel_id":"other"}]`, http.StatusBadRequest, `step key "Op"`},
		{"a pane spelled two ways", `[{"op":"select_channel","channel_id":"ch-1"},{"op":"send_input","pane":"host-shell-0","PANE":"chat"}]`, http.StatusBadRequest, `step key "PANE"`},
		{"a panel spelled two ways", `[{"op":"select_channel","channel_id":"ch-1"},{"op":"add_pane","panel":"host-shell","Panel":"chat"}]`, http.StatusBadRequest, `step key "Panel"`},
		{"keys the daemon doesn't read", `[{"op":"select_channel","channel_id":"ch-1","Tab":"Git"}]`, http.StatusOK, `"Tab":"Git"`},
		{"each key spelled once", `[{"op":"select_channel","channel_id":"ch-1"}]`, http.StatusOK, `"channel_id":"ch-1"`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			resp, err := http.Post(ts.URL+"/api/ui/commands", "application/json", strings.NewReader(`{"steps":`+tt.steps+`}`))
			require.NoError(s.T(), err)
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			require.Equal(s.T(), tt.code, resp.StatusCode, string(body))
			require.Contains(s.T(), string(body), tt.want)
			if tt.code != http.StatusOK {
				require.Empty(s.T(), ran, "the window got a refused command")
				return
			}
			require.JSONEq(s.T(), tt.steps, <-ran)
		})
	}
}

// uiStepKeys are uiStep's keys, so a key the daemon starts reading is
// checked for other spellings too.
func (s *UIHandlerSuite) TestStepKeysAreUIStepsKeys() {
	var keys []string
	for _, f := range reflect.VisibleFields(reflect.TypeFor[uiStep]()) {
		keys = append(keys, jsonName(f))
	}
	require.ElementsMatch(s.T(), keys, uiStepKeys)
}
