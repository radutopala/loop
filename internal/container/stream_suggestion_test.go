package container

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type StreamSuggestionSuite struct {
	suite.Suite
}

func TestStreamSuggestionSuite(t *testing.T) {
	suite.Run(t, new(StreamSuggestionSuite))
}

const resultLine = `{"type":"result","result":"ok","session_id":"s1"}`

// The CLI writes its predicted next prompt after the turn's result; the run
// still returns that result.
func (s *StreamSuggestionSuite) TestPromptSuggestion() {
	tests := []struct {
		name string
		line string
		want []string
	}{
		{"suggestion", `{"type":"prompt_suggestion","suggestion":"now add tests","uuid":"u1","session_id":"s1"}`, []string{"now add tests"}},
		{"empty", `{"type":"prompt_suggestion","suggestion":""}`, nil},
		{"malformed", `{"type":"prompt_suggestion","suggestion":7}`, nil},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var got []string
			res, err := scanStreamJSON(strings.NewReader(resultLine+"\n"+tt.line), streamCallbacks{
				onSuggestion: func(text string) { got = append(got, text) },
			})
			require.NoError(s.T(), err)
			require.Equal(s.T(), "ok", res.Result)
			require.Equal(s.T(), tt.want, got)
		})
	}
}

// A run that didn't ask for a suggestion ignores one.
func (s *StreamSuggestionSuite) TestPromptSuggestionUnasked() {
	res, err := scanStreamJSON(strings.NewReader(resultLine+"\n"+`{"type":"prompt_suggestion","suggestion":"x"}`), streamCallbacks{})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", res.Result)
}
