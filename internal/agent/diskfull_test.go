package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type DiskFullSuite struct {
	suite.Suite
}

func TestDiskFullSuite(t *testing.T) {
	suite.Run(t, new(DiskFullSuite))
}

func (s *DiskFullSuite) TestIsDiskFull() {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{"docker create error", "creating container: Error response from daemon: mkdir /var/lib/docker/overlay2/abc: no space left on device", true},
		{"copy error", "copying files: Error response from daemon: write /home/agent/.claude.json: No Space Left On Device", true},
		{"node errno", "container exited with code 1: parsing claude response: no result event found; last output:\nError: ENOSPC: write failed", true},
		{"lowercase errno", "enospc", true},
		{"unrelated", "creating container: Error response from daemon: conflict", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, IsDiskFull(tc.msg))
		})
	}
}
