package mcpbrowser

import (
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

func (s *ServerSuite) TestMatchRefs() {
	page := []browser.ElementRef{
		{RefID: "ref_1", Role: "textbox", Name: "Email address"},
		{RefID: "ref_2", Role: "combobox", Name: "Country", Value: "Romania"},
		{RefID: "ref_3", Role: "button", Name: "Subscribe"},
		{RefID: "ref_4", Role: "button", Name: "Cancel"},
		{RefID: "ref_5", Role: "searchbox", Description: "Search the docs"},
	}
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "a word in a name", query: "subscribe", want: []string{"ref_3"}},
		{name: "name and role together", query: "Subscribe button", want: []string{"ref_3"}},
		{name: "a role keeps page order", query: "button", want: []string{"ref_3", "ref_4"}},
		{name: "the value counts", query: "country romania", want: []string{"ref_2"}},
		{name: "the description counts", query: "search docs", want: []string{"ref_5"}},
		{name: "part of a word", query: "mail", want: []string{"ref_1"}},
		{name: "every word must match", query: "login button", want: nil},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var got []string
			for _, r := range matchRefs(page, tt.query) {
				got = append(got, r.RefID)
			}
			require.Equal(s.T(), tt.want, got)
		})
	}
}
