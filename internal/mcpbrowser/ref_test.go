package mcpbrowser

import (
	"encoding/json"
	"net/http"

	"github.com/stretchr/testify/require"
)

func (s *ServerSuite) TestRefArgUnmarshal() {
	tests := []struct {
		in      string
		want    refArg
		wantErr string
	}{
		{in: `3`, want: 3},
		{in: `"3"`, want: 3},
		{in: `"ref_12"`, want: 12},
		{in: `" ref_4 "`, want: 4},
		{in: `"button"`, wantErr: `got "button"`},
		{in: `true`, wantErr: "ref must be a number"},
	}
	for _, tt := range tests {
		s.Run(tt.in, func() {
			var r refArg
			err := json.Unmarshal([]byte(tt.in), &r)
			if tt.wantErr != "" {
				require.ErrorContains(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.want, r)
		})
	}
}

func (s *ServerSuite) TestInputSchemaForPanicsOnAnUnrepresentableType() {
	type bad struct {
		C chan int `json:"c"`
	}
	require.Panics(s.T(), func() { inputSchemaFor[bad]() })
}

func (s *ServerSuite) TestRefToolsTakeTheRefAsFindPrintsIt() {
	// find prints [ref_N]; passing that straight back must reach the tool,
	// not fail the schema check.
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{})
	})
	for tool, args := range map[string]map[string]any{
		"computer":    {"ref": "ref_9", "action": "click"},
		"form_input":  {"ref": "ref_9", "value": "x"},
		"upload_file": {"ref": "ref_9", "paths": []string{"x"}},
	} {
		res := callTool(s.T(), session, tool, args)
		require.True(s.T(), res.IsError, tool)
		require.Contains(s.T(), getText(s.T(), res), "ref 9 out of range", tool)
	}
}
