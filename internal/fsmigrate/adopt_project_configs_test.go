package fsmigrate

import (
	"context"
	"errors"

	"github.com/stretchr/testify/require"
)

func (s *FSMigrateSuite) TestAdoptProjectConfigs() {
	boom := errors.New("boom")

	tests := []struct {
		name     string
		adopt    bool
		failOn   string
		expected []string
		err      string
	}{
		{name: "no adopter skips", expected: nil},
		{name: "every project dir", adopt: true, expected: []string{"/p1", "/p2"}},
		{name: "stops on a failure", adopt: true, failOn: "/p1", expected: []string{"/p1"}, err: "trusting the project config of /p1: boom"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var got []string
			c := &Ctx{ProjectDirs: []string{"/p1", "/p2"}}
			if tt.adopt {
				c.AdoptProjectConfig = func(dir string) error {
					got = append(got, dir)
					if dir == tt.failOn {
						return boom
					}
					return nil
				}
			}
			err := adoptProjectConfigs(context.Background(), c)
			if tt.err != "" {
				require.EqualError(s.T(), err, tt.err)
			} else {
				require.NoError(s.T(), err)
			}
			require.Equal(s.T(), tt.expected, got)
		})
	}
}
