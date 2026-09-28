package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agent"
)

type RunErrorNoticeSuite struct {
	suite.Suite
}

func TestRunErrorNoticeSuite(t *testing.T) {
	suite.Run(t, new(RunErrorNoticeSuite))
}

func (s *RunErrorNoticeSuite) TestRunErrorNotice() {
	tests := []struct {
		name   string
		errMsg string
		want   string
	}{
		{
			name:   "docker api disk full",
			errMsg: "copying files: Error response from daemon: write /home/agent/.claude.json: no space left on device",
			want:   "⚠️ " + agent.DiskFullNotice,
		},
		{
			name:   "disk full in the agent's output",
			errMsg: "container exited with code 1: parsing claude response: no result event found; last output:\nError: ENOSPC: write failed",
			want:   "⚠️ " + agent.DiskFullNotice,
		},
		{
			name:   "other errors are shown fenced",
			errMsg: "container exited with code 137 (killed — out of memory or out of disk space): parsing claude response: no result event found",
			want:   "⚠️ The run failed:\n```\ncontainer exited with code 137 (killed — out of memory or out of disk space): parsing claude response: no result event found\n```",
		},
		{
			name:   "fences in the output can't close the block",
			errMsg: "last output:\n```\nboom",
			want:   "⚠️ The run failed:\n```\nlast output:\n'''\nboom\n```",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, runErrorNotice(tc.errMsg))
		})
	}
}
