package types

import (
	"strings"

	"github.com/stretchr/testify/require"
)

func (s *TypesSuite) TestNormalizeTicketURL() {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{name: "empty clears", raw: "  ", want: ""},
		{name: "trims", raw: " https://tracker.example.com/T-1 ", want: "https://tracker.example.com/T-1"},
		{name: "http", raw: "http://tracker.example.com/T-1", want: "http://tracker.example.com/T-1"},
		{name: "too long", raw: "https://x.com/" + strings.Repeat("a", MaxTicketURLLen), wantErr: "longer than 2048 characters"},
		{name: "not a url", raw: "%zz", wantErr: "absolute http(s) URL"},
		{name: "wrong scheme", raw: "ftp://x.com/1", wantErr: "absolute http(s) URL"},
		{name: "no host", raw: "https:///1", wantErr: "absolute http(s) URL"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got, err := NormalizeTicketURL(tc.raw)
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
		})
	}
}
